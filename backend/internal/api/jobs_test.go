//go:build integration

// Background jobs.
//
// The queue exists for work that must survive a restart and retry on its own —
// above all ZATCA submission, which blueprint E1.2 says must never be silently
// dropped. These tests hold it to that: that a sale queues its own obligation
// atomically, that ordering per terminal is real, that a rejection does not
// retry while a timeout does, and above all that nothing is ever marked as
// reported when it was not.
package api

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/jobs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{
		Level: slog.LevelError + 1,
	}))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// fakeZATCA is a TEST DOUBLE for the transport, never for the cryptography.
//
// It returns whatever outcome a test asks for. It signs nothing, produces no
// hash and fabricates no stamp — the document it is handed was signed on the
// terminal, and the production client refuses outright until the format is
// verified. What is exercised here is the retry, ordering and alerting policy
// around submission, which is real code and independent of the format.
type fakeZATCA struct {
	available bool
	outcome   zatca.Outcome
	status    int
	message   string
	calls     int
	seenICVs  []int64
}

func (f *fakeZATCA) Available() bool { return f.available }

func (f *fakeZATCA) Submit(_ context.Context, s zatca.Submission) (zatca.Response, error) {
	f.calls++
	f.seenICVs = append(f.seenICVs, s.ICV)
	return zatca.Response{
		Outcome: f.outcome, HTTPStatus: f.status, Error: f.message,
	}, nil
}

// workerWith builds a worker whose queue holds only this test's work.
//
// The job table is platform-wide by design — one worker serves every tenant —
// so a test that drains it would otherwise process whatever earlier tests left
// behind and never reach its own. Everything belonging to another tenant is
// removed first. Not a product concern: in production there is one queue and
// draining all of it is exactly the point.
func (h *harness) workerWith(
	t *testing.T, f *shopFixture, client zatca.Submitter,
) (*jobs.Worker, *jobs.Queue) {
	t.Helper()
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`DELETE FROM job WHERE tenant_id IS DISTINCT FROM $1`, f.tenantID)
		return e
	}); err != nil {
		t.Fatalf("isolate the queue: %v", err)
	}

	q := jobs.NewQueue(h.pool)
	w := jobs.NewWorker(q, quietLogger(), "test-worker")
	w.Register(jobs.KindZATCASubmit, jobs.NewZATCASubmitter(h.pool, client))
	w.Register(jobs.KindZATCAStaleness, jobs.NewStalenessSweeper(h.pool))
	return w, q
}

// A sale queues its own obligation to report, in the same transaction.
//
// A crash between writing the invoice and queuing its submission would leave a
// document nobody knew to send — an exposure no queue, alert or dashboard would
// ever mention.
func TestASaleQueuesItsOwnSubmission(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}
	invoiceID := decodeJSON(t, resp)["invoice_id"].(string)

	ctx := t.Context()
	var kind, queueKey, state string
	var maxAttempts int
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT kind, queue_key, state::text, max_attempts
			FROM job WHERE payload->>'invoice_id' = $1`, invoiceID).
			Scan(&kind, &queueKey, &state, &maxAttempts)
	}); err != nil {
		t.Fatalf("no submission job was queued for the sale: %v", err)
	}

	if kind != "zatca.submit" {
		t.Errorf("kind = %s", kind)
	}
	// Per device: the chain must be submitted in ICV order (E1.3 RULE 4).
	if !strings.HasPrefix(queueKey, "device:") {
		t.Errorf("queue key = %q; submission must be serialised per terminal",
			queueKey)
	}
	// Unlimited: an unreported invoice does not stop being a legal exposure
	// after twenty-five tries.
	if maxAttempts != 0 {
		t.Errorf("max attempts = %d, want 0 (never gives up)", maxAttempts)
	}
}

// The gate. Nothing may be marked reported while the format is unverified.
func TestNothingIsReportedWhileSubmissionIsUnavailable(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}
	invoiceID := decodeJSON(t, resp)["invoice_id"].(string)

	// The production client, which refuses.
	w, _ := h.workerWith(t, f, zatca.SubmitterFor(true))
	ctx := t.Context()
	if _, err := w.Drain(ctx, 10); err != nil {
		t.Fatalf("drain: %v", err)
	}

	var state, jobState string
	var outcome string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT state FROM sales_invoice WHERE id = $1`, invoiceID).
			Scan(&state); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT outcome FROM zatca_submission_attempt WHERE invoice_id = $1`,
			invoiceID).Scan(&outcome)
	}); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT state::text FROM job WHERE payload->>'invoice_id' = $1`,
			invoiceID).Scan(&jobState)
	}); err != nil {
		t.Fatalf("read job: %v", err)
	}

	if state != "signed_pending_report" {
		t.Errorf("the invoice moved to %q while submission was unavailable; "+
			"nothing may be marked reported without verified integration", state)
	}
	if outcome != "not_attempted" {
		t.Errorf("attempt recorded as %q, want not_attempted — no request left "+
			"this machine", outcome)
	}
	// Still queued, not failed. The document is valid and the obligation
	// stands; it simply cannot be sent yet.
	if jobState != "pending" {
		t.Errorf("job state = %q, want pending; the invoice must stay queued",
			jobState)
	}
}

// A transport failure retries. A rejection does not.
func TestTransportFailuresRetryAndRejectionsDoNot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		outcome   zatca.Outcome
		wantJob   string
		wantState string
	}{
		{"a timeout retries", zatca.OutcomeTransportFailure, "pending", "signed_pending_report"},
		{"a rejection does not", zatca.OutcomeRejected, "failed", "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			f := h.seedShop(t, "cashier")

			resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
				oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
			if resp.StatusCode != 201 {
				t.Fatalf("sale: %s", readBody(t, resp))
			}
			invoiceID := decodeJSON(t, resp)["invoice_id"].(string)
			stampInvoice(t, h, f, invoiceID)

			client := &fakeZATCA{
				available: true, outcome: tc.outcome,
				status: 400, message: "the buyer identifier is malformed",
			}
			w, _ := h.workerWith(t, f, client)
			ctx := t.Context()
			if _, err := w.Drain(ctx, 5); err != nil {
				t.Fatalf("drain: %v", err)
			}

			var jobState, invState string
			if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx,
					`SELECT state::text FROM job WHERE payload->>'invoice_id' = $1`,
					invoiceID).Scan(&jobState)
			}); err != nil {
				t.Fatalf("read job: %v", err)
			}
			if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx,
					`SELECT state FROM sales_invoice WHERE id = $1`, invoiceID).
					Scan(&invState)
			}); err != nil {
				t.Fatalf("read invoice: %v", err)
			}

			if jobState != tc.wantJob {
				t.Errorf("job state = %q, want %q", jobState, tc.wantJob)
			}
			if invState != tc.wantState {
				t.Errorf("invoice state = %q, want %q", invState, tc.wantState)
			}
		})
	}
}

// A rejected invoice keeps its ICV and raises a critical alert. Deleting it
// would create the gap tamper detection looks for.
func TestARejectedInvoiceKeepsItsNumberAndAlerts(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}
	body := decodeJSON(t, resp)
	invoiceID := body["invoice_id"].(string)
	icvBefore := int64(body["zatca"].(map[string]any)["icv"].(float64))
	stampInvoice(t, h, f, invoiceID)

	w, _ := h.workerWith(t, f, &fakeZATCA{
		available: true, outcome: zatca.OutcomeRejected, status: 400,
		message: "XSD validation failed",
	})
	ctx := t.Context()
	if _, err := w.Drain(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}

	var icvAfter int64
	var level, detail string
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx,
			`SELECT icv FROM zatca_invoice WHERE invoice_id = $1`, invoiceID).
			Scan(&icvAfter); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT level, detail FROM compliance_alert
			WHERE kind = 'zatca.rejected' AND cleared_at IS NULL`).
			Scan(&level, &detail)
	}); err != nil {
		t.Fatalf("read after rejection: %v", err)
	}

	if icvAfter != icvBefore {
		t.Errorf("the ICV changed from %d to %d; a rejected invoice keeps its "+
			"number or the chain gains a gap", icvBefore, icvAfter)
	}
	if level != "critical" {
		t.Errorf("alert level = %q, want critical", level)
	}
	if !strings.Contains(detail, "XSD validation failed") {
		t.Errorf("the alert does not carry ZATCA's own words: %q", detail)
	}
}

// An accepted invoice settles, and the route decides which state.
func TestAnAcceptedInvoiceSettlesByRoute(t *testing.T) {
	for _, tc := range []struct{ docType, want string }{
		{"simplified", "reported"},
		{"standard", "cleared"},
	} {
		t.Run(tc.docType, func(t *testing.T) {
			h := newHarness(t)
			f := h.seedShop(t, "cashier")

			sale := oneItemSale(f, newUUID(), "1", "115.00", "115.00")
			sale["doc_type"] = tc.docType
			resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, sale)
			if resp.StatusCode != 201 {
				t.Fatalf("sale: %s", readBody(t, resp))
			}
			invoiceID := decodeJSON(t, resp)["invoice_id"].(string)
			stampInvoice(t, h, f, invoiceID)

			w, _ := h.workerWith(t, f, &fakeZATCA{
				available: true, outcome: zatca.OutcomeAccepted, status: 200,
			})
			if _, err := w.Drain(t.Context(), 5); err != nil {
				t.Fatalf("drain: %v", err)
			}

			var state string
			if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
				return tx.QueryRow(t.Context(),
					`SELECT state FROM sales_invoice WHERE id = $1`, invoiceID).
					Scan(&state)
			}); err != nil {
				t.Fatalf("read invoice: %v", err)
			}
			if state != tc.want {
				t.Errorf("a %s invoice settled as %q, want %q",
					tc.docType, state, tc.want)
			}
		})
	}
}

// Every attempt is recorded, including the ones that failed. "Never silently
// dropped" needs somewhere the attempt is written down.
func TestEveryAttemptIsRecorded(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}
	invoiceID := decodeJSON(t, resp)["invoice_id"].(string)
	stampInvoice(t, h, f, invoiceID)

	client := &fakeZATCA{available: true, outcome: zatca.OutcomeTransportFailure, status: 503}
	w, q := h.workerWith(t, f, client)
	ctx := t.Context()

	// Three sweeps, forcing each retry to become due.
	for i := 0; i < 3; i++ {
		if _, err := w.Drain(ctx, 3); err != nil {
			t.Fatalf("drain: %v", err)
		}
		makeJobsDue(t, h)
	}
	_ = q

	var attempts int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM zatca_submission_attempt WHERE invoice_id = $1`,
			invoiceID).Scan(&attempts)
	}); err != nil {
		t.Fatalf("count attempts: %v", err)
	}

	if attempts < 3 {
		t.Errorf("%d attempts recorded after 3 sweeps; a failed submission must "+
			"leave a trace of every try", attempts)
	}
	if client.calls != attempts {
		t.Errorf("%d calls made but %d recorded", client.calls, attempts)
	}
}

// Submission is serialised per terminal, so the chain goes out in ICV order.
func TestSubmissionsGoOutInICVOrderPerTerminal(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")

	for i := 0; i < 4; i++ {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
			oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
		if resp.StatusCode != 201 {
			t.Fatalf("sale %d: %s", i, readBody(t, resp))
		}
		stampInvoice(t, h, f, decodeJSON(t, resp)["invoice_id"].(string))
	}

	client := &fakeZATCA{available: true, outcome: zatca.OutcomeAccepted, status: 200}
	w, _ := h.workerWith(t, f, client)
	if _, err := w.Drain(t.Context(), 20); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if len(client.seenICVs) != 4 {
		t.Fatalf("%d submissions, want 4: %v", len(client.seenICVs), client.seenICVs)
	}
	for i := 1; i < len(client.seenICVs); i++ {
		if client.seenICVs[i] < client.seenICVs[i-1] {
			t.Fatalf("submitted out of ICV order: %v — submitting %d before %d "+
				"breaks the chain", client.seenICVs,
				client.seenICVs[i], client.seenICVs[i-1])
		}
	}
}

// Escalation at 12, 24 and 72 hours, highest level only.
func TestUnsubmittedInvoicesEscalate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		age   time.Duration
		level string
	}{
		{"under 12 hours is quiet", 6 * time.Hour, ""},
		{"over 12 hours is a notice", 13 * time.Hour, "notice"},
		{"over 24 hours is a warning", 30 * time.Hour, "warning"},
		{"over 72 hours is critical", 80 * time.Hour, "critical"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			f := h.seedShop(t, "cashier")
			ctx := t.Context()

			resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
				oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
			if resp.StatusCode != 201 {
				t.Fatalf("sale: %s", readBody(t, resp))
			}
			invoiceID := decodeJSON(t, resp)["invoice_id"].(string)

			// Age the invoice.
			// Tenant context, not platform. sales_invoice is under row-level
			// security, so the platform plane sees no rows and the update
			// silently affects nothing — which is what made this test read
			// zero alerts while the code was correct.
			if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
				_, e := tx.Exec(ctx,
					`UPDATE sales_invoice SET issued_at = now() - $2::interval
					 WHERE id = $1`, invoiceID, tc.age.String())
				return e
			}); err != nil {
				t.Fatalf("age the invoice: %v", err)
			}

			sweeper := jobs.NewStalenessSweeper(h.pool)
			if err := sweeper.Run(ctx, jobs.Job{TenantID: &f.tenantID}); err != nil {
				t.Fatalf("sweep: %v", err)
			}

			var levels []string
			if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
				rows, e := tx.Query(ctx, `
					SELECT level FROM compliance_alert
					WHERE kind = 'zatca.unsubmitted' AND cleared_at IS NULL`)
				if e != nil {
					return e
				}
				defer rows.Close()
				for rows.Next() {
					var l string
					if e := rows.Scan(&l); e != nil {
						return e
					}
					levels = append(levels, l)
				}
				return rows.Err()
			}); err != nil {
				t.Fatalf("read alerts: %v", err)
			}

			if tc.level == "" {
				if len(levels) != 0 {
					t.Errorf("an invoice %v old raised %v; nothing is due under 12 hours",
						tc.age, levels)
				}
				return
			}
			// Highest only: three alerts about one problem is how an Owner
			// learns to ignore all of them.
			if len(levels) != 1 {
				t.Fatalf("%d alerts raised, want exactly 1: %v", len(levels), levels)
			}
			if levels[0] != tc.level {
				t.Errorf("alert level = %q, want %q", levels[0], tc.level)
			}
		})
	}
}

// The sweep clears its alert once the backlog is gone. An alert that stays
// raised after the problem trains people to ignore it.
func TestTheStalenessAlertClearsWhenTheBacklogDoes(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}
	invoiceID := decodeJSON(t, resp)["invoice_id"].(string)
	stampInvoice(t, h, f, invoiceID)

	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE sales_invoice SET issued_at = now() - interval '80 hours'
			 WHERE id = $1`, invoiceID)
		return e
	}); err != nil {
		t.Fatalf("age: %v", err)
	}

	sweeper := jobs.NewStalenessSweeper(h.pool)
	if err := sweeper.Run(ctx, jobs.Job{TenantID: &f.tenantID}); err != nil {
		t.Fatalf("first sweep: %v", err)
	}

	// The invoice reaches ZATCA.
	w, _ := h.workerWith(t, f, &fakeZATCA{
		available: true, outcome: zatca.OutcomeAccepted, status: 200,
	})
	if _, err := w.Drain(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if err := sweeper.Run(ctx, jobs.Job{TenantID: &f.tenantID}); err != nil {
		t.Fatalf("second sweep: %v", err)
	}

	var open int
	if err := h.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM compliance_alert
			WHERE kind = 'zatca.unsubmitted' AND cleared_at IS NULL`).Scan(&open)
	}); err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if open != 0 {
		t.Errorf("%d staleness alerts still open after the backlog cleared", open)
	}
}

// A job a dead worker was holding is released. A running job blocks its whole
// queue key, so one crashed worker would otherwise stop every submission for
// that terminal forever.
func TestAnAbandonedJobIsReleased(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	ctx := t.Context()

	resp := h.do(t, "POST", "/api/v1/pos/sales", f.token,
		oneItemSale(f, newUUID(), "1", "115.00", "115.00"))
	if resp.StatusCode != 201 {
		t.Fatalf("sale: %s", readBody(t, resp))
	}

	q := jobs.NewQueue(h.pool)
	claimed, found, err := q.Claim(ctx, "worker-that-will-die")
	if err != nil || !found {
		t.Fatalf("claim: %v found=%v", err, found)
	}

	// Backdate the lock, as if the worker died some time ago.
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE job SET locked_at = now() - interval '1 hour' WHERE id = $1`,
			claimed.ID)
		return e
	}); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	released, err := q.Reap(ctx, 15*time.Minute)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if released != 1 {
		t.Fatalf("%d jobs released, want 1", released)
	}

	_, found, err = q.Claim(ctx, "the-next-worker")
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if !found {
		t.Error("the released job could not be claimed again; that terminal's " +
			"queue is stuck forever")
	}
}

// The backoff schedule is design document 08 §4, and QA gate M2 depends on it:
// a 24-hour outage must recover without anyone intervening.
func TestTheBackoffScheduleCoversATwentyFourHourOutage(t *testing.T) {
	want := []time.Duration{
		0, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour,
	}
	for i, w := range want {
		if got := jobs.Backoff(i + 1); got != w {
			t.Errorf("attempt %d waits %v, want %v", i+1, got, w)
		}
	}
	if got := jobs.Backoff(9); got != 6*time.Hour {
		t.Errorf("later attempts wait %v, want 6h", got)
	}

	// Roughly eight attempts across a day, then success on reconnection.
	var elapsed time.Duration
	attempts := 0
	for elapsed < 24*time.Hour && attempts < 100 {
		attempts++
		elapsed += jobs.Backoff(attempts)
	}
	if attempts < 6 || attempts > 12 {
		t.Errorf("%d attempts across a 24-hour outage; the schedule should give "+
			"roughly eight", attempts)
	}
}

// One submission per invoice however many times the sale path runs.
func TestAnInvoiceIsQueuedForSubmissionOnce(t *testing.T) {
	h := newHarness(t)
	f := h.seedShop(t, "cashier")
	body := oneItemSale(f, newUUID(), "1", "115.00", "115.00")

	for i := 0; i < 3; i++ {
		resp := h.do(t, "POST", "/api/v1/pos/sales", f.token, body)
		resp.Body.Close()
	}

	var count int
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		// Scoped to this tenant: job is platform-wide, so a bare count picks
		// up every other test's queue.
		return tx.QueryRow(t.Context(),
			`SELECT count(*) FROM job WHERE kind = 'zatca.submit' AND tenant_id = $1`,
			f.tenantID).Scan(&count)
	}); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Errorf("%d submission jobs for one invoice retried 3 times", count)
	}
}

// stampInvoice stands in for the TERMINAL having signed the document.
//
// All three artefacts, because they are three different things: the signed
// document, the stamp over it, and the QR derived from that. The submitter
// needs the document; earlier it was handed the stamp, which would have posted
// a signature with nothing attached.
//
// Obvious placeholders, never plausible cryptography — inventing that is
// exactly what the P1 gate exists to prevent.
func stampInvoice(t *testing.T, h *harness, f *shopFixture, invoiceID string) {
	t.Helper()
	if err := h.pool.TxAsTenant(t.Context(), f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `
			UPDATE zatca_invoice
			SET xml = $2, stamp = $3, qr_tlv = $4
			WHERE invoice_id = $1`,
			invoiceID,
			"<placeholder-signed-document/>",
			"terminal-signed-placeholder",
			"placeholder-qr-tlv")
		return e
	}); err != nil {
		t.Fatalf("record a terminal signed document: %v", err)
	}
}

// makeJobsDue brings every pending retry forward so a test need not wait.
func makeJobsDue(t *testing.T, h *harness) {
	t.Helper()
	if err := h.pool.TxAsPlatform(t.Context(), func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(),
			`UPDATE job SET run_after = now() - interval '1 second'
			 WHERE state = 'pending'`)
		return e
	}); err != nil {
		t.Fatalf("bring retries forward: %v", err)
	}
}
