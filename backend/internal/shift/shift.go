// Package shift owns the cash session: opening a till, recording what moves
// through the drawer, and reconciling it at the end of a shift.
//
// # Why this is not a reporting feature
//
// Cash is the only tender that leaves no independent trace. A card sale is
// corroborated by the acquirer, a transfer by the bank; a banknote that never
// reaches the drawer is invisible unless the drawer is counted against what the
// system expected. That single comparison is what makes cash handling
// accountable, and it is why an X/Z report is part of trading rather than part
// of month end.
//
// # Expected cash is derived
//
// Nothing here stores a running drawer balance. It is computed from the
// session's own movements every time it is asked for. A stored total would
// drift the moment a contributing row was written outside the code maintaining
// it, and a drift is indistinguishable from theft — which is the one thing this
// package exists to tell apart.
package shift

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Service manages cash sessions.
type Service struct {
	pool *db.Pool
}

func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Session is a till's shift.
type Session struct {
	ID        uuid.UUID `json:"id"`
	SessionNo int64     `json:"session_no"`
	DeviceID  uuid.UUID `json:"device_id"`
	StoreID   uuid.UUID `json:"store_id"`
	State     string    `json:"state"`

	OpenedAt     string `json:"opened_at"`
	OpeningFloat string `json:"opening_float"`
	BlindClose   bool   `json:"blind_close"`
}

// Report is the X or Z reckoning of a session.
//
// The same shape serves both. An X report is this taken mid-shift and changes
// nothing; a Z report is this taken at close, with the counted figure and the
// variance filled in. Two shapes would let the two drift apart, and a
// supervisor comparing an X against the later Z needs them to be the same
// arithmetic.
type Report struct {
	SessionNo int64  `json:"session_no"`
	State     string `json:"state"`
	OpenedAt  string `json:"opened_at"`
	ClosedAt  string `json:"closed_at,omitempty"`

	OpeningFloat string `json:"opening_float"`
	InvoiceCount int64  `json:"invoice_count"`

	GrossSales  string `json:"gross_sales"`
	NetSales    string `json:"net_sales"`
	TaxTotal    string `json:"tax_total"`
	RefundTotal string `json:"refund_total"`

	// The three takings figures are omitted alongside ExpectedCash on a blind
	// close. See withholdTheDrawer for why hiding the total alone did not work.
	CashTakings    string `json:"cash_takings,omitempty"`
	NonCashTakings string `json:"non_cash_takings,omitempty"`
	CashMovements  string `json:"cash_movements,omitempty"`

	// ExpectedCash is omitted on a blind close until the count is committed, so
	// a cashier cannot make the drawer agree with the screen.
	ExpectedCash string `json:"expected_cash,omitempty"`
	CountedCash  string `json:"counted_cash,omitempty"`
	Variance     string `json:"variance,omitempty"`
}

// Open starts a session on a till.
func (s *Service) Open(
	ctx context.Context, tenantID, deviceID, userID uuid.UUID,
	openingFloat decimal.Decimal, blindClose bool,
) (Session, error) {
	if openingFloat.IsNegative() {
		return Session{}, errs.New(errs.CodeInvalidInput,
			"An opening float cannot be negative.")
	}

	var out Session
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var companyID, storeID uuid.UUID
		var status string
		e := tx.QueryRow(ctx, `
			SELECT company_id, store_id, status::text FROM device WHERE id = $1`,
			deviceID).Scan(&companyID, &storeID, &status)

		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That terminal is not registered.")
		}
		if e != nil {
			return e
		}
		if status != "active" {
			return errs.Newf(errs.CodeForbidden,
				"This terminal is %s, so a till session cannot be opened on it.",
				status)
		}

		// Checked here so the refusal says what to do. The partial unique index
		// is still the guarantee — it catches two tills opening at the same
		// instant, which this check cannot — but an index violation surfaces as
		// "that record already exists", which tells a cashier nothing.
		var openNo int64
		e = tx.QueryRow(ctx,
			`SELECT session_no FROM cash_session
			 WHERE device_id = $1 AND state = 'open'`, deviceID).Scan(&openNo)
		if e == nil {
			return errs.Newf(errs.CodeConflict,
				"This till already has an open session (number %d). Close it with "+
					"a Z report before starting another.", openNo)
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		var sessionNo int64
		if e := tx.QueryRow(ctx,
			`SELECT claim_session_no($1)`, deviceID).Scan(&sessionNo); e != nil {
			return e
		}

		e = tx.QueryRow(ctx, `
			INSERT INTO cash_session
			  (tenant_id, company_id, store_id, device_id, session_no,
			   opened_by, opening_float, blind_close)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			RETURNING id, session_no, device_id, store_id, state,
			          to_char(opened_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'),
			          opening_float, blind_close`,
			tenantID, companyID, storeID, deviceID, sessionNo,
			userID, openingFloat, blindClose).
			Scan(&out.ID, &out.SessionNo, &out.DeviceID, &out.StoreID, &out.State,
				&out.OpenedAt, &out.OpeningFloat, &out.BlindClose)

		if e != nil {
			// The partial unique index is what makes two open sessions on one
			// till impossible. Saying so plainly beats surfacing an index name.
			return db.Translate(e,
				"This till already has an open session. Close it with a Z report "+
					"before starting another.")
		}
		return nil
	})
	return out, err
}

// Current returns the open session on a till, if there is one.
func (s *Service) Current(
	ctx context.Context, tenantID, deviceID uuid.UUID,
) (Session, error) {
	var out Session
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			SELECT id, session_no, device_id, store_id, state,
			       to_char(opened_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'),
			       opening_float, blind_close
			FROM cash_session WHERE device_id = $1 AND state = 'open'`, deviceID).
			Scan(&out.ID, &out.SessionNo, &out.DeviceID, &out.StoreID, &out.State,
				&out.OpenedAt, &out.OpeningFloat, &out.BlindClose)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"This till has no open session. Open one before ringing up sales.")
		}
		return e
	})
	return out, err
}

// XReport is a mid-shift snapshot. It changes nothing and may be taken as often
// as a supervisor likes.
//
// The expected figure is shown even on a blind-close till, because an X report
// is for the supervisor rather than the cashier and the permission gating it
// says so.
func (s *Service) XReport(
	ctx context.Context, tenantID, sessionID uuid.UUID,
) (Report, error) {
	return s.report(ctx, tenantID, sessionID, true)
}

// Peek is what the CASHIER sees before counting.
//
// On a blind-close till the expected figure is withheld. Blueprint B7 requires
// this: a cashier who can see the target can make the drawer agree with it, and
// then the variance — the only signal there is — reads zero on every shift.
func (s *Service) Peek(
	ctx context.Context, tenantID, sessionID uuid.UUID,
) (Report, error) {
	return s.report(ctx, tenantID, sessionID, false)
}

func (s *Service) report(
	ctx context.Context, tenantID, sessionID uuid.UUID, mayRevealExpected bool,
) (Report, error) {
	var out Report
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var blind bool
		if e := tx.QueryRow(ctx,
			`SELECT blind_close FROM cash_session WHERE id = $1`, sessionID).
			Scan(&blind); errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That till session was not found.")
		} else if e != nil {
			return e
		}

		r, e := readReport(ctx, tx, sessionID)
		if e != nil {
			return e
		}
		if blind && !mayRevealExpected && r.State == "open" {
			// Withheld only while the session is open. Once closed, the count is
			// committed and hiding the figures would stop anyone reconciling it.
			withholdTheDrawer(&r)
		}
		out = r
		return nil
	})
	return out, err
}

// withholdTheDrawer removes every figure a cashier could add up to the total
// they are about to be asked to count blind.
//
// Hiding ExpectedCash alone did not do it, and the arithmetic says why:
//
//	expected = opening_float + cash_takings + cash_movements
//
// which is the definition cash_session_expected is written from. All three
// addends were on the cashier's own screen — the POS shift panel listed
// "Opening float", "Cash takings" and "Cash moved" one under the other — under
// a comment explaining that the expected drawer was deliberately not shown. A
// cashier who is short can add three numbers and put back the difference, and
// then the variance, which is the only signal there is, reads zero on every
// shift for ever. Blueprint B7 asks for a blind close and design 11 §9 says
// sales are "tracked silently"; a total withheld from a screen that shows its
// parts is neither.
//
// NonCashTakings goes too. gross_sales less refund_total less non-cash takings
// is the cash takings exactly, for a shop that sells only for cash and card —
// which is most shops. Leaving it would swap one subtraction for another.
//
// What remains is what a cashier legitimately needs and cannot reconstruct the
// drawer from: how many sales they rang, what those sales came to, the tax on
// them, what was refunded, and the float they counted in themselves at open.
//
// A supervisor's X report is not touched — it carries report.view, which is the
// whole reason the two routes have different permissions — and neither is a
// closed session, where the count is already committed and hiding the figures
// would stop anyone reconciling the variance they are being asked to explain.
func withholdTheDrawer(r *Report) {
	r.CashTakings = ""
	r.NonCashTakings = ""
	r.CashMovements = ""
	r.ExpectedCash = ""
}

func readReport(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (Report, error) {
	var r Report
	var closedAt *string
	var opening, gross, net, tax, refunds, cash, nonCash, movements, expected decimal.Decimal
	var counted, variance *decimal.Decimal

	err := tx.QueryRow(ctx, `
		SELECT session_no, state,
		       to_char(opened_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'),
		       to_char(closed_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'),
		       opening_float, invoice_count, gross_sales, net_sales, tax_total,
		       refund_total, cash_takings, non_cash_takings, cash_movements,
		       expected_cash, counted_cash, variance
		FROM cash_session_report($1)`, sessionID).
		Scan(&r.SessionNo, &r.State, &r.OpenedAt, &closedAt,
			&opening, &r.InvoiceCount, &gross, &net, &tax,
			&refunds, &cash, &nonCash, &movements,
			&expected, &counted, &variance)

	if errors.Is(err, pgx.ErrNoRows) {
		return Report{}, errs.New(errs.CodeNotFound, "That till session was not found.")
	}
	if err != nil {
		return Report{}, err
	}

	if closedAt != nil {
		r.ClosedAt = *closedAt
	}
	r.OpeningFloat = opening.String()
	r.GrossSales, r.NetSales, r.TaxTotal = gross.String(), net.String(), tax.String()
	r.RefundTotal = refunds.String()
	r.CashTakings, r.NonCashTakings = cash.String(), nonCash.String()
	r.CashMovements = movements.String()
	r.ExpectedCash = expected.String()
	if counted != nil {
		r.CountedCash = counted.String()
	}
	if variance != nil {
		r.Variance = variance.String()
	}
	return r, nil
}

// Close performs the Z report: it records the count, freezes the expected
// figure, and shuts the session.
//
// Exactly once. A second Z would either double-count the takings or overwrite
// the count someone signed for, and the database refuses it — a closed session
// is final by trigger, not by convention.
func (s *Service) Close(
	ctx context.Context, tenantID, sessionID, userID uuid.UUID,
	countedCash decimal.Decimal, note string,
) (Report, error) {
	if countedCash.IsNegative() {
		return Report{}, errs.New(errs.CodeInvalidInput,
			"A drawer cannot hold less than nothing. Enter what was counted.")
	}

	var out Report
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// Lock the session first, so two cashiers closing the same till at once
		// cannot both read 'open' and both proceed.
		var state string
		e := tx.QueryRow(ctx,
			`SELECT state FROM cash_session WHERE id = $1 FOR UPDATE`, sessionID).
			Scan(&state)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That till session was not found.")
		}
		if e != nil {
			return e
		}
		if state == "closed" {
			return errs.New(errs.CodeConflict,
				"This till session has already been closed. Its Z report stands; "+
					"record a cash adjustment against the next session instead.")
		}

		// The expected figure is computed and frozen in the same statement that
		// closes the session, so nothing can land in between and change what the
		// variance was measured against.
		var expected decimal.Decimal
		if e := tx.QueryRow(ctx,
			`SELECT cash_session_expected($1)`, sessionID).Scan(&expected); e != nil {
			return e
		}

		// The casts are load-bearing: without them Postgres cannot infer a type
		// for `$3 - $4` and refuses the statement outright.
		if _, e := tx.Exec(ctx, `
			UPDATE cash_session
			SET state = 'closed', closed_at = now(), closed_by = $2,
			    counted_cash = $3::numeric, expected_cash = $4::numeric,
			    variance = $3::numeric - $4::numeric,
			    note = nullif(btrim($5::text), '')
			WHERE id = $1`,
			sessionID, userID, countedCash, expected, note); e != nil {
			return db.Translate(e, "That till session could not be closed.")
		}

		// In the same transaction that froze the count, so a drawer can never be
		// declared closed with its difference missing from the books. If the
		// posting fails — a closed period, an unmapped role — the close fails
		// with it and the session stays open, which is the honest outcome: a Z
		// report that reconciled nothing is worse than one that was refused.
		if e := s.postVariance(ctx, tx, tenantID, sessionID, userID,
			countedCash.Sub(expected)); e != nil {
			return e
		}

		r, e := readReport(ctx, tx, sessionID)
		if e != nil {
			return e
		}
		out = r
		return nil
	})
	return out, err
}

// postVariance puts a drawer difference in the ledger.
//
// Design 11 §9: the variance "posts to a Cash Over/Short account rather than
// being absorbed silently". Until 0052 it did not — the figure was written onto
// cash_session and went no further, so a shop could run short every day for a
// month while Cash carried a balance the drawer had never held and the loss
// appeared nowhere in the P&L.
//
// Two rules rather than one signed rule, exactly as 0025 and 0026 arranged the
// costing variance: the amount is always positive and the sides swap. A single
// rule taking a signed figure would write a negative debit where a credit
// belongs, and a trial balance carrying negative debits is one an accountant
// cannot read.
//
// Idempotent through the engine's own key. `journal_entry_source_uq` is unique
// on (source_type, source_id, rule_key), so a retry of the same close cannot
// post twice — though it should never get the chance, since a second close is
// refused above with the session still locked.
func (s *Service) postVariance(
	ctx context.Context, tx pgx.Tx,
	tenantID, sessionID, userID uuid.UUID, variance decimal.Decimal,
) error {
	if variance.IsZero() {
		// The drawer reconciled. There is no entry to make, and a run of
		// zero-value journal entries would bury the shifts that went wrong.
		return nil
	}

	// Counted less than expected: the cash is not there, so the asset comes
	// down and the shop wears the difference.
	ruleKey, direction := "cash.shortage", "short"
	if variance.IsPositive() {
		ruleKey, direction = "cash.overage", "over"
	}

	var companyID, storeID uuid.UUID
	var sessionNo int64
	var country string
	if e := tx.QueryRow(ctx, `
		SELECT s.company_id, s.store_id, s.session_no, c.country
		FROM cash_session s JOIN company c ON c.id = s.company_id
		WHERE s.id = $1`, sessionID).
		Scan(&companyID, &storeID, &sessionNo, &country); e != nil {
		return e
	}

	// Dated now rather than at the shift's opening. A shift that ran past
	// midnight is reconciled on the day it was counted, and closed-period
	// protection then applies to the day the money was actually declared.
	_, err := accounting.PostByRule(ctx, tx, accounting.Entry{
		TenantID: tenantID, CompanyID: companyID,
		Date:       time.Now().UTC(),
		SourceType: "cash_session", SourceID: sessionID,
		StoreID: &storeID,
		RuleKey: ruleKey, PostedBy: &userID,
		Memo: fmt.Sprintf("Till session %d closed %s by %s",
			sessionNo, direction, variance.Abs().StringFixed(2)),
	}, country, accounting.Transaction{
		Amounts: map[string]decimal.Decimal{"variance": variance.Abs()},
	})
	return err
}

// RecordMovement notes cash in or out of the drawer other than a sale.
func (s *Service) RecordMovement(
	ctx context.Context, tenantID, sessionID, userID uuid.UUID,
	amount decimal.Decimal, reason, note string,
) error {
	if amount.IsZero() {
		return errs.New(errs.CodeInvalidInput,
			"A cash movement of nothing is not a movement.")
	}
	if len(note) < 3 {
		return errs.New(errs.CodeInvalidInput,
			"Every cash movement needs an explanation. An unexplained hand in "+
				"the till is exactly what this record exists to make visible.")
	}

	return s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// FOR SHARE, and the reason is the whole point of this record.
		//
		// Close takes FOR UPDATE, computes the expected cash and freezes it in
		// one transaction. An unlocked read here would return 'open' from its
		// own snapshot while that close was in flight, and the movement would
		// land AFTER the figure it was supposed to be part of: a safe drop of
		// 500 recorded at the moment of the Z report shows up in the report's
		// cash_movements but not in the expected_cash it is compared against,
		// so the drawer reads 500 short and the cashier is asked to explain a
		// difference that is an artefact of two clocks.
		//
		// FOR SHARE conflicts with Close's FOR UPDATE, so one of two things
		// happens and both are correct: the movement commits first and the
		// close counts it, or the close commits first and this read — which
		// PostgreSQL re-evaluates against the committed row once the lock is
		// granted — sees 'closed' and refuses below.
		var state string
		e := tx.QueryRow(ctx,
			`SELECT state FROM cash_session WHERE id = $1 FOR SHARE`,
			sessionID).Scan(&state)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That till session was not found.")
		}
		if e != nil {
			return e
		}
		if state == "closed" {
			return errs.New(errs.CodeConflict,
				"That till session is closed. Record this against the open session.")
		}

		_, e = tx.Exec(ctx, `
			INSERT INTO cash_movement
			  (tenant_id, session_id, amount, reason, note, recorded_by)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			tenantID, sessionID, amount, reason, note, userID)
		return db.Translate(e, "That cash movement could not be recorded.")
	})
}
