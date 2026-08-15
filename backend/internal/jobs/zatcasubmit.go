package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/zatca"
)

// KindZATCASubmit is the job that must never give up.
const KindZATCASubmit = "zatca.submit"

// SubmitPayload names the invoice to send.
//
// Ids only. The document itself is read from the database at run time, because
// a payload written days ago could otherwise submit a stale version of an
// invoice that has since been superseded.
type SubmitPayload struct {
	InvoiceID uuid.UUID `json:"invoice_id"`
	DeviceID  uuid.UUID `json:"device_id"`
}

// QueueSubmission enqueues an invoice for ZATCA, inside the caller's
// transaction.
//
// Called from the sale path so the invoice and its obligation to report are
// written together. A crash between them would leave an invoice nobody knew to
// submit, which is precisely the exposure E1.2 exists to prevent.
func QueueSubmission(
	ctx context.Context, tx pgx.Tx, tenantID, invoiceID, deviceID uuid.UUID,
) error {
	return EnqueueIn(ctx, tx, Spec{
		TenantID: &tenantID,
		Kind:     KindZATCASubmit,
		Payload:  SubmitPayload{InvoiceID: invoiceID, DeviceID: deviceID},

		// Per device: the hash chain must be submitted in ICV order (E1.3 RULE
		// 4), and jobs sharing a queue key run strictly in sequence. Submitting
		// 4,183 before 4,182 breaks the chain, which is the exact tamper signal
		// ZATCA looks for.
		QueueKey: "device:" + deviceID.String(),
		Priority: 10,

		// Unlimited. There is no dead-letter path that discards an unreported
		// invoice, because the legal exposure does not expire after
		// twenty-five tries.
		MaxAttempts: 0,

		// One outstanding submission per invoice, however many times the sale
		// path is replayed.
		DedupeKey: "zatca.submit:" + invoiceID.String(),
	})
}

// ZATCASubmitter runs the submission job.
type ZATCASubmitter struct {
	pool   *db.Pool
	client zatca.Submitter
}

func NewZATCASubmitter(pool *db.Pool, client zatca.Submitter) *ZATCASubmitter {
	return &ZATCASubmitter{pool: pool, client: client}
}

// invoiceForSubmission is what the job needs to know about the document.
type invoiceForSubmission struct {
	uuid      uuid.UUID
	docType   string
	state     string
	companyID uuid.UUID
	icv       int64
	stamp     string
	attempts  int
}

// Run submits one invoice.
func (z *ZATCASubmitter) Run(ctx context.Context, j Job) error {
	var p SubmitPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return Permanent{errs.New(errs.CodeInternal,
			"This submission job names no invoice.")}
	}
	if j.TenantID == nil {
		return Permanent{errs.New(errs.CodeInternal,
			"This submission job names no tenant.")}
	}
	tenantID := *j.TenantID

	inv, err := z.read(ctx, tenantID, p.InvoiceID)
	if err != nil {
		return err
	}

	// Already settled by an earlier attempt. At-least-once delivery means this
	// is normal traffic, not an incident.
	if isSettled(inv.state) {
		return nil
	}

	// The gate. Nothing is sent and nothing is marked as sent — the attempt is
	// recorded as not_attempted so an Owner can see exactly why an invoice is
	// outstanding, and the job returns a retryable error so it stays queued.
	if !z.client.Available() {
		if err := z.record(ctx, tenantID, inv, p.InvoiceID, zatca.Response{
			Outcome: zatca.OutcomeNotAttempted,
			Error: "The document format has not been verified against ZATCA's " +
				"published standard, so nothing was sent.",
		}); err != nil {
			return err
		}
		return errs.New(errs.CodeComplianceBlocked,
			"E-invoicing submission is not yet available on this installation. "+
				"This invoice remains queued and has NOT been reported.")
	}

	// A document with no stamp cannot be submitted. The stamp is made on the
	// terminal; its absence means the terminal has not finished with this
	// invoice, not that the server should invent one.
	if len(inv.stamp) == 0 {
		return errs.New(errs.CodeComplianceBlocked,
			"This invoice has not been signed by its terminal yet, so there is "+
				"nothing to submit.")
	}

	route := zatca.RouteFor(inv.docType)
	resp, submitErr := z.client.Submit(ctx, zatca.Submission{
		InvoiceUUID: inv.uuid, ICV: inv.icv, Route: route, SignedXML: []byte(inv.stamp),
	})

	if err := z.record(ctx, tenantID, inv, p.InvoiceID, resp); err != nil {
		return err
	}

	switch resp.Outcome {
	case zatca.OutcomeAccepted, zatca.OutcomeAcceptedWithWarnings:
		return z.settle(ctx, tenantID, p.InvoiceID, route, resp)

	case zatca.OutcomeRejected:
		// Business rejection. Keeps its ICV — deleting it would create the gap
		// tamper detection looks for — moves to rejected, raises a critical
		// alert, and is corrected by credit note. Retrying would never succeed
		// and would keep the queue busy enough to bury the alert.
		if err := z.markRejected(ctx, tenantID, p.InvoiceID, resp); err != nil {
			return err
		}
		return Permanent{errs.Newf(errs.CodeComplianceBlocked,
			"ZATCA rejected this invoice: %s. It keeps its number and must be "+
				"corrected by credit note.", resp.Error)}

	default:
		// Transport failure: retries on the backoff schedule, indefinitely.
		if submitErr != nil {
			return submitErr
		}
		return errs.Newf(errs.CodeUnavailable,
			"ZATCA could not be reached (%d). This invoice is still queued.",
			resp.HTTPStatus)
	}
}

func (z *ZATCASubmitter) read(
	ctx context.Context, tenantID, invoiceID uuid.UUID,
) (invoiceForSubmission, error) {
	var inv invoiceForSubmission
	err := z.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			SELECT i.uuid, i.doc_type, i.state, i.company_id,
			       z.icv, coalesce(z.stamp, ''),
			       (SELECT count(*) FROM zatca_submission_attempt a
			        WHERE a.invoice_id = i.id)
			FROM sales_invoice i
			JOIN zatca_invoice z ON z.invoice_id = i.id
			WHERE i.id = $1`, invoiceID).
			Scan(&inv.uuid, &inv.docType, &inv.state, &inv.companyID,
				&inv.icv, &inv.stamp, &inv.attempts)
		if errors.Is(e, pgx.ErrNoRows) {
			return Permanent{errs.New(errs.CodeNotFound,
				"That invoice no longer exists, so there is nothing to submit.")}
		}
		return e
	})
	return inv, err
}

// record writes the attempt. Every attempt, successful or not: "never silently
// dropped" needs somewhere the attempt is written down.
func (z *ZATCASubmitter) record(
	ctx context.Context, tenantID uuid.UUID, inv invoiceForSubmission,
	invoiceID uuid.UUID, resp zatca.Response,
) error {
	var body any
	if len(resp.Body) > 0 && json.Valid(resp.Body) {
		body = resp.Body
	}

	return z.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO zatca_submission_attempt
			  (tenant_id, invoice_id, attempt_no, route, outcome, http_status,
			   response, error)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			tenantID, invoiceID, inv.attempts+1,
			string(zatca.RouteFor(inv.docType)), string(resp.Outcome),
			nullInt(resp.HTTPStatus), body, nullText(resp.Error))
		return err
	})
}

// settle moves an accepted invoice to its final state.
func (z *ZATCASubmitter) settle(
	ctx context.Context, tenantID, invoiceID uuid.UUID,
	route zatca.Route, resp zatca.Response,
) error {
	state := "reported"
	if route == zatca.RouteClearance {
		state = "cleared"
	}
	if resp.Outcome == zatca.OutcomeAcceptedWithWarnings {
		// A distinct state, not a footnote. The invoice is valid and stamped,
		// but a warning ZATCA raised has to stay visible until someone acts on
		// it — folding it into 'cleared' loses it.
		state = "accepted_with_warnings"
	}

	return z.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE sales_invoice SET state = $2 WHERE id = $1`, invoiceID, state)
		return err
	})
}

func (z *ZATCASubmitter) markRejected(
	ctx context.Context, tenantID, invoiceID uuid.UUID, resp zatca.Response,
) error {
	return z.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE sales_invoice SET state = 'rejected' WHERE id = $1`,
			invoiceID); err != nil {
			return err
		}

		var companyID uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT company_id FROM sales_invoice WHERE id = $1`, invoiceID).
			Scan(&companyID); err != nil {
			return err
		}
		return raiseAlert(ctx, tx, tenantID, companyID, "zatca.rejected", "critical",
			fmt.Sprintf("ZATCA rejected an invoice: %s", resp.Error))
	})
}

func isSettled(state string) bool {
	switch state {
	case "reported", "cleared", "accepted_with_warnings", "rejected":
		return true
	}
	return false
}

// raiseAlert records an alert once per kind and level per company.
//
// ON CONFLICT DO NOTHING against the open-alert index: an Owner who receives
// the same critical alert every minute stops reading them, which defeats the
// alert. It stays open until cleared.
func raiseAlert(
	ctx context.Context, tx pgx.Tx, tenantID, companyID uuid.UUID,
	kind, level, detail string,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO compliance_alert (tenant_id, company_id, kind, level, detail)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (company_id, kind, level) WHERE cleared_at IS NULL
		DO NOTHING`,
		tenantID, companyID, kind, level, detail)
	return err
}

func nullInt(i int) any {
	if i == 0 {
		return nil
	}
	return i
}

// --- staleness -----------------------------------------------------------

// KindZATCAStaleness sweeps for invoices that have not reached ZATCA.
const KindZATCAStaleness = "zatca.staleness"

// Escalation thresholds, from design document 08 §5 and blueprint E1.3 RULE 6.
//
// Here rather than in the schema, so changing them is a change in one place
// rather than a migration.
var escalations = []struct {
	after time.Duration
	level string
}{
	{72 * time.Hour, "critical"},
	{24 * time.Hour, "warning"},
	{12 * time.Hour, "notice"},
}

// StalenessSweeper raises alerts about unsubmitted invoices.
type StalenessSweeper struct{ pool *db.Pool }

func NewStalenessSweeper(pool *db.Pool) *StalenessSweeper {
	return &StalenessSweeper{pool: pool}
}

// Run evaluates every company and raises the highest applicable alert.
//
// Highest only. A company 80 hours behind gets one critical alert, not a
// critical plus a warning plus a notice — three alerts about one problem is
// how an Owner learns to ignore all of them.
func (s *StalenessSweeper) Run(ctx context.Context, j Job) error {
	if j.TenantID == nil {
		return Permanent{errs.New(errs.CodeInternal,
			"This staleness sweep names no tenant.")}
	}
	tenantID := *j.TenantID

	return s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM company`)
		if err != nil {
			return err
		}
		defer rows.Close()

		var companies []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			companies = append(companies, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		for _, companyID := range companies {
			if err := s.evaluate(ctx, tx, tenantID, companyID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *StalenessSweeper) evaluate(
	ctx context.Context, tx pgx.Tx, tenantID, companyID uuid.UUID,
) error {
	var invoiceID *uuid.UUID
	var issuedAt *time.Time
	var age *time.Duration
	var count *int64

	var ageInterval *string
	err := tx.QueryRow(ctx, `
		SELECT invoice_id, issued_at, age::text, unsubmitted_count
		FROM oldest_unsubmitted_invoice($1)`, companyID).
		Scan(&invoiceID, &issuedAt, &ageInterval, &count)

	if errors.Is(err, pgx.ErrNoRows) || invoiceID == nil {
		// Nothing outstanding: clear any open staleness alerts. An alert that
		// stays raised after the problem is gone trains people to ignore it.
		_, err := tx.Exec(ctx, `
			UPDATE compliance_alert SET cleared_at = now()
			WHERE company_id = $1 AND kind = 'zatca.unsubmitted'
			  AND cleared_at IS NULL`, companyID)
		return err
	}
	if err != nil {
		return err
	}
	_ = age

	elapsed := time.Since(*issuedAt)
	for _, e := range escalations {
		if elapsed < e.after {
			continue
		}
		outstanding := int64(0)
		if count != nil {
			outstanding = *count
		}
		return raiseAlert(ctx, tx, tenantID, companyID, "zatca.unsubmitted", e.level,
			fmt.Sprintf(
				"%d invoice(s) have not reached ZATCA. The oldest was issued %s ago.",
				outstanding, elapsed.Round(time.Hour)))
	}
	return nil
}
