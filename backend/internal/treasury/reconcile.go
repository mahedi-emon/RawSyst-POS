package treasury

// Bank reconciliation (blueprint C11).
//
//	Bank statement -> import -> auto-match -> manual match -> reconcile & lock
//	                                       -> difference / exception report
//
// # What is actually being proved
//
// C11 opens with the sentence: "Proves that what the software says is in the
// bank is actually what the bank says." Everything here serves that one claim,
// and the claim has a precise arithmetic form:
//
//	opening balance
//	  + everything the bank recorded in the period
//	  = closing balance          <- the statement is internally consistent
//
//	closing balance
//	  - the ledger balance on that account at that date
//	  = the unmatched items      <- and nothing else
//
// If the second identity does not hold, something is wrong that neither list
// explains, and the reconciliation cannot be signed. `Reconcile` refuses in
// that case, which is the whole value of the feature: a reconciliation that can
// be signed off with an unexplained difference means nothing at all.
//
// # Auto-matching is a suggestion, and says so
//
// A match on amount and date within a window is a guess. It is usually right
// and it is occasionally very wrong — two identical supplier payments on the
// same day are indistinguishable to any rule. So every match records whether a
// person made it or a rule did, and a person can undo either.
//
// The rule is deliberately narrow: exact amount, and a date within three days.
// A looser rule matches more lines and produces a reconciliation nobody
// checked, which is worse than one with more work left in it.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// MatchWindow is how far apart a bank line and a ledger line may be dated and
// still be matched automatically.
//
// Three days. A card settlement lands two working days after the sale and a
// cheque clears when it clears; anything wider starts pairing a payment with
// the following week's identical one.
const MatchWindow = 3

// NewStatement is a bank statement being imported.
type NewStatement struct {
	AccountID uuid.UUID
	StartsOn  time.Time
	EndsOn    time.Time
	Opening   decimal.Decimal
	Closing   decimal.Decimal
	Reference string
	Lines     []NewStatementLine
}

// NewStatementLine is one line as the bank stated it.
type NewStatementLine struct {
	ValueDate   time.Time
	Description string
	Reference   string
	// Amount is signed from the BANK's point of view: positive is money
	// arriving in the account.
	Amount decimal.Decimal
}

// StatementLine is one line, with whatever it was matched to.
type StatementLine struct {
	ID          uuid.UUID `json:"id"`
	ValueDate   string    `json:"value_date"`
	Description string    `json:"description"`
	Reference   string    `json:"reference,omitempty"`
	Amount      string    `json:"amount"`

	MatchedTo   string `json:"matched_to,omitempty"`
	MatchedKind string `json:"match_kind,omitempty"`
	MatchedBy   string `json:"matched_by,omitempty"`
}

// LedgerLine is a line in the books that no statement line has claimed.
type LedgerLine struct {
	ID     uuid.UUID `json:"id"`
	Date   string    `json:"entry_date"`
	Entry  string    `json:"entry_no"`
	Memo   string    `json:"memo,omitempty"`
	Amount string    `json:"amount"`
	Source string    `json:"source_type,omitempty"`
}

// Statement is a reconciliation in progress or finished.
type Statement struct {
	ID       uuid.UUID `json:"id"`
	Account  string    `json:"account"`
	Currency string    `json:"currency"`
	StartsOn string    `json:"starts_on"`
	EndsOn   string    `json:"ends_on"`
	Opening  string    `json:"opening_balance"`
	Closing  string    `json:"closing_balance"`
	Status   string    `json:"status"`

	Reference    string `json:"reference,omitempty"`
	ReconciledBy string `json:"reconciled_by,omitempty"`
	ReconciledAt string `json:"reconciled_at,omitempty"`

	// LedgerBalance is what the books say the account held at `ends_on`.
	LedgerBalance string `json:"ledger_balance"`

	// Difference is the closing balance less the ledger balance, less the
	// unmatched items on both sides. It must be nil to sign off, and it is the
	// figure the whole exercise is about.
	Difference string `json:"difference"`
	Reconciled bool   `json:"reconciled"`

	Lines []StatementLine `json:"lines,omitempty"`

	// Unmatched is what the books hold and the bank has not seen. C11's
	// exception report, and the more useful of the two lists: it holds the
	// cheque that never cleared and the payment recorded twice.
	Unmatched []LedgerLine `json:"unmatched_in_books,omitempty"`
}

// Import records a statement and its lines.
func (s *Service) Import(
	ctx context.Context, scope Scope, in NewStatement,
) (Statement, error) {
	if len(in.Lines) == 0 {
		return Statement{}, errs.New(errs.CodeInvalidInput,
			"A statement with no lines on it proves nothing. Import the lines "+
				"the bank sent.")
	}
	if in.EndsOn.Before(in.StartsOn) {
		return Statement{}, errs.New(errs.CodeInvalidInput,
			"The statement ends before it starts.")
	}

	// The bank's own arithmetic, checked before anything is stored. A statement
	// whose lines do not add up to its own closing balance was mistyped or
	// truncated, and reconciling against it would chase a difference that is
	// not in the books at all.
	total := decimal.Zero
	for _, l := range in.Lines {
		total = total.Add(l.Amount)
	}
	if got := in.Opening.Add(total); !got.Equal(in.Closing) {
		return Statement{}, errs.Newf(errs.CodeInvalidInput,
			"The statement does not add up: %s plus the lines comes to %s, and "+
				"it says it closes at %s. Check that every line was imported.",
			in.Opening.StringFixed(2), got.StringFixed(2),
			in.Closing.StringFixed(2))
	}

	var out Statement
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var kind string
		if e := tx.QueryRow(ctx,
			`SELECT kind FROM money_account WHERE id = $1 AND company_id = $2`,
			in.AccountID, scope.CompanyID).Scan(&kind); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound,
					"That account is not this business's.")
			}
			return e
		}
		if !bankLike(kind) {
			return errs.New(errs.CodeInvalidInput,
				"Only a bank, card-settlement or gateway account has a "+
					"statement to reconcile against.")
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO bank_statement
			  (tenant_id, company_id, account_id, starts_on, ends_on,
			   opening_balance, closing_balance, reference, imported_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
			scope.TenantID, scope.CompanyID, in.AccountID,
			in.StartsOn, in.EndsOn, in.Opening, in.Closing,
			nullIfBlank(in.Reference), scope.UserID).Scan(&id); e != nil {
			return db.Translate(e, "That statement could not be imported.")
		}

		for _, l := range in.Lines {
			if _, e := tx.Exec(ctx, `
				INSERT INTO bank_statement_line
				  (tenant_id, statement_id, value_date, description, reference, amount)
				VALUES ($1,$2,$3,$4,$5,$6)`,
				scope.TenantID, id, l.ValueDate, strings.TrimSpace(l.Description),
				nullIfBlank(l.Reference), l.Amount); e != nil {
				return db.Translate(e, "One of those statement lines could not be read.")
			}
		}

		if _, e := s.autoMatch(ctx, tx, scope, id); e != nil {
			return e
		}

		read, e := s.readStatement(ctx, tx, scope, id, true)
		out = read
		return e
	})
	return out, err
}

// autoMatch pairs statement lines with ledger lines on amount and date.
//
// One at a time, in date order, and each ledger line can be claimed once —
// enforced by a unique index rather than by this loop, so two callers racing
// cannot both claim the same line.
func (s *Service) autoMatch(
	ctx context.Context, tx pgx.Tx, scope Scope, statementID uuid.UUID,
) (int, error) {
	var accountID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT m.account_id FROM bank_statement b
		JOIN money_account m ON m.id = b.account_id
		WHERE b.id = $1`, statementID).Scan(&accountID); err != nil {
		return 0, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, value_date, amount FROM bank_statement_line
		WHERE statement_id = $1 AND journal_line_id IS NULL
		ORDER BY value_date, id`, statementID)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id     uuid.UUID
		date   time.Time
		amount decimal.Decimal
	}
	var lines []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.date, &p.amount); err != nil {
			rows.Close()
			return 0, err
		}
		lines = append(lines, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	matched := 0
	for _, l := range lines {
		// The ledger's sign convention is the opposite way round from the
		// bank's for a moment: money ARRIVING in the account is a debit in the
		// books and a positive figure on the statement, so the two agree once
		// the ledger line is read as debit-less-credit.
		var candidate uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT l.id
			FROM journal_line l
			JOIN journal_entry e ON e.id = l.entry_id
			WHERE l.account_id = $1
			  AND (l.base_debit - l.base_credit) = $2
			  AND abs(e.entry_date - $3::date) <= $4
			  AND NOT EXISTS (
			    SELECT 1 FROM bank_statement_line b WHERE b.journal_line_id = l.id)
			ORDER BY abs(e.entry_date - $3::date), l.id
			LIMIT 1`,
			accountID, l.amount, l.date, MatchWindow).Scan(&candidate)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return matched, err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE bank_statement_line
			SET journal_line_id = $2, match_kind = 'automatic', matched_at = now()
			WHERE id = $1`, l.id, candidate); err != nil {
			return matched, err
		}
		matched++
	}
	return matched, nil
}

// Match pairs one statement line with one ledger line, by hand.
func (s *Service) Match(
	ctx context.Context, scope Scope, lineID, journalLineID uuid.UUID,
) error {
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// Both ends must belong to this company AND to this account. A match
		// against a line on a different account would reconcile the bank
		// against somebody else's money.
		var ok bool
		if e := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1
			  FROM bank_statement_line b
			  JOIN bank_statement st ON st.id = b.statement_id
			  JOIN money_account m ON m.id = st.account_id
			  JOIN journal_line l ON l.id = $2
			  WHERE b.id = $1 AND st.company_id = $3
			    AND l.account_id = m.account_id)`,
			lineID, journalLineID, scope.CompanyID).Scan(&ok); e != nil {
			return e
		}
		if !ok {
			return errs.New(errs.CodeInvalidInput,
				"That entry is not on the same account as the statement line.")
		}

		tag, e := tx.Exec(ctx, `
			UPDATE bank_statement_line
			SET journal_line_id = $2, match_kind = 'manual',
			    matched_at = now(), matched_by = $3
			WHERE id = $1`, lineID, journalLineID, scope.UserID)
		if e != nil {
			return db.Translate(e,
				"That entry has already been matched to another statement line.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That statement line was not found.")
		}
		return nil
	})
}

// Unmatch undoes a match, whoever or whatever made it.
func (s *Service) Unmatch(
	ctx context.Context, scope Scope, lineID uuid.UUID,
) error {
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE bank_statement_line b
			SET journal_line_id = NULL, match_kind = NULL,
			    matched_at = NULL, matched_by = NULL
			FROM bank_statement st
			WHERE b.id = $1 AND st.id = b.statement_id AND st.company_id = $2`,
			lineID, scope.CompanyID)
		if err != nil {
			// Translated, not returned raw. A reconciled statement is frozen by
			// a trigger, and undoing a match on one is a refusal the person
			// needs to read -- "Reopen it first, which is recorded." Returned
			// as it comes back, it reaches them as "Something went wrong on our
			// side", which says the fault is ours and invites a retry that will
			// fail the same way.
			return db.Translate(err, "That statement line was not found.")
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That statement line was not found.")
		}
		return nil
	})
}

// Reconcile signs a statement off.
//
// Refused while anything is unexplained. That refusal is the feature: a
// reconciliation that can be signed with a difference nobody accounts for is a
// piece of paper, and the auditor who relies on it has been misled by a screen.
func (s *Service) Reconcile(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Statement, error) {
	var out Statement
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		st, e := s.readStatement(ctx, tx, scope, id, true)
		if e != nil {
			return e
		}
		if st.Status == "reconciled" {
			return errs.New(errs.CodeConflict,
				"That statement has already been reconciled.")
		}
		if !st.Reconciled {
			return errs.Newf(errs.CodeConflict,
				"There is %s left unexplained. Match the remaining lines, or "+
					"record what the bank charged and the books do not have, "+
					"before signing this off.", st.Difference)
		}

		if _, e := tx.Exec(ctx, `
			UPDATE bank_statement
			SET status = 'reconciled', reconciled_by = $2, reconciled_at = now()
			WHERE id = $1`, id, scope.UserID); e != nil {
			return db.Translate(e, "That statement could not be reconciled.")
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "bank_reconciled",
			EntityType: "bank_statement", EntityID: &id,
			After: map[string]any{
				"account": st.Account, "to": st.EndsOn,
				"closing_balance": st.Closing,
			},
		}); e != nil {
			return e
		}

		read, e := s.readStatement(ctx, tx, scope, id, true)
		out = read
		return e
	})
	return out, err
}

// Statements lists them for one account, newest first.
func (s *Service) Statements(
	ctx context.Context, scope Scope, accountID *uuid.UUID,
) ([]Statement, error) {
	out := []Statement{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT b.id FROM bank_statement b
			WHERE b.company_id = $1 AND ($2::uuid IS NULL OR b.account_id = $2)
			ORDER BY b.ends_on DESC, b.imported_at DESC
			LIMIT 100`, scope.CompanyID, accountID)
		if err != nil {
			return err
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, id := range ids {
			// Without the lines: a list of statements is a list of headings,
			// and loading every line of every one turns opening the screen into
			// a report.
			st, e := s.readStatement(ctx, tx, scope, id, false)
			if e != nil {
				return e
			}
			out = append(out, st)
		}
		return nil
	})
	return out, err
}

// Statement reads one, with its lines and its exceptions.
func (s *Service) Statement(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Statement, error) {
	var out Statement
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		st, e := s.readStatement(ctx, tx, scope, id, true)
		out = st
		return e
	})
	return out, err
}

func (s *Service) readStatement(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID, detail bool,
) (Statement, error) {
	var st Statement
	var opening, closing decimal.Decimal
	var accountID uuid.UUID
	var endsOn time.Time

	var moneyAccountID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT b.id, m.name, m.currency, m.account_id, b.account_id,
		       to_char(b.starts_on, 'YYYY-MM-DD'), b.ends_on,
		       b.opening_balance, b.closing_balance, b.status,
		       coalesce(b.reference, ''), coalesce(u.full_name, ''),
		       coalesce(to_char(b.reconciled_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00'), '')
		FROM bank_statement b
		JOIN money_account m ON m.id = b.account_id
		LEFT JOIN app_user u ON u.id = b.reconciled_by
		WHERE b.id = $1 AND b.company_id = $2`,
		id, scope.CompanyID).
		Scan(&st.ID, &st.Account, &st.Currency, &accountID, &moneyAccountID,
			&st.StartsOn, &endsOn, &opening, &closing, &st.Status, &st.Reference,
			&st.ReconciledBy, &st.ReconciledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Statement{}, errs.New(errs.CodeNotFound,
			"That statement is not this business's.")
	}
	if err != nil {
		return Statement{}, err
	}
	st.EndsOn = endsOn.Format("2006-01-02")
	st.Opening = opening.StringFixed(2)
	st.Closing = closing.StringFixed(2)

	// The ledger's own answer for the same account on the same date.
	var ledger decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
		FROM journal_line l
		JOIN journal_entry e ON e.id = l.entry_id
		WHERE l.account_id = $1 AND e.entry_date <= $2`,
		accountID, endsOn).Scan(&ledger); err != nil {
		return Statement{}, err
	}
	st.LedgerBalance = ledger.StringFixed(2)

	// The two lists of exceptions, and what they come to.
	//
	//   bank lines nobody matched   — charges and interest the books lack
	//   ledger lines nobody matched — cheques not cleared, entries duplicated
	//
	// The statement reconciles when the closing balance, less the ledger
	// balance, is explained exactly by those two and nothing else.
	var unmatchedBank, unmatchedLedger decimal.Decimal

	// Across EVERY statement on this account, not just this one.
	//
	// This was scoped to the current statement, and the arithmetic only came
	// out right on a company's very first one. The closing balance a bank
	// states is its running total including every earlier statement, and the
	// ledger balance below is cumulative too — so a bank charge left unmatched
	// in March is still explaining part of the gap in April, and counting only
	// April's unmatched lines makes April fail to reconcile by exactly March's
	// unexplained item.
	//
	// The bug would have shown up on the second statement any customer ever
	// imported, which is the kind that gets found in production rather than in
	// a demo.
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(b.amount), 0)
		FROM bank_statement_line b
		JOIN bank_statement st ON st.id = b.statement_id
		WHERE st.account_id = $1 AND b.journal_line_id IS NULL
		  AND b.value_date <= $2`, moneyAccountID, endsOn).
		Scan(&unmatchedBank); err != nil {
		return Statement{}, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(l.base_debit - l.base_credit), 0)
		FROM journal_line l
		JOIN journal_entry e ON e.id = l.entry_id
		WHERE l.account_id = $1 AND e.entry_date <= $2
		  AND NOT EXISTS (
		    SELECT 1 FROM bank_statement_line b WHERE b.journal_line_id = l.id)`,
		accountID, endsOn).Scan(&unmatchedLedger); err != nil {
		return Statement{}, err
	}

	difference := closing.Sub(ledger).Sub(unmatchedBank).Add(unmatchedLedger)
	st.Difference = difference.StringFixed(2)
	st.Reconciled = difference.IsZero()

	if !detail {
		return st, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT b.id, to_char(b.value_date, 'YYYY-MM-DD'), b.description,
		       coalesce(b.reference, ''), b.amount,
		       coalesce(e.entry_no::text, ''), coalesce(b.match_kind, ''),
		       coalesce(u.full_name, '')
		FROM bank_statement_line b
		LEFT JOIN journal_line l ON l.id = b.journal_line_id
		LEFT JOIN journal_entry e ON e.id = l.entry_id
		LEFT JOIN app_user u ON u.id = b.matched_by
		WHERE b.statement_id = $1
		ORDER BY b.value_date, b.id`, id)
	if err != nil {
		return Statement{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var l StatementLine
		var amount decimal.Decimal
		if err := rows.Scan(&l.ID, &l.ValueDate, &l.Description, &l.Reference,
			&amount, &l.MatchedTo, &l.MatchedKind, &l.MatchedBy); err != nil {
			return Statement{}, err
		}
		l.Amount = amount.StringFixed(2)
		st.Lines = append(st.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return Statement{}, err
	}

	unmatched, err := tx.Query(ctx, `
		SELECT l.id, to_char(e.entry_date, 'YYYY-MM-DD'), e.entry_no::text,
		       coalesce(e.memo, ''), (l.base_debit - l.base_credit),
		       coalesce(e.source_type, '')
		FROM journal_line l
		JOIN journal_entry e ON e.id = l.entry_id
		WHERE l.account_id = $1 AND e.entry_date <= $2
		  AND NOT EXISTS (
		    SELECT 1 FROM bank_statement_line b WHERE b.journal_line_id = l.id)
		ORDER BY e.entry_date DESC, l.id
		LIMIT 200`, accountID, endsOn)
	if err != nil {
		return Statement{}, err
	}
	defer unmatched.Close()

	for unmatched.Next() {
		var l LedgerLine
		var amount decimal.Decimal
		if err := unmatched.Scan(&l.ID, &l.Date, &l.Entry, &l.Memo,
			&amount, &l.Source); err != nil {
			return Statement{}, err
		}
		l.Amount = amount.StringFixed(2)
		st.Unmatched = append(st.Unmatched, l)
	}
	return st, unmatched.Err()
}
