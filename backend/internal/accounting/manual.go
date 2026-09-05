// Journal entries written by hand (blueprint C10).
//
// Every other entry in this ledger is posted by a rule from a document — a
// sale, a bill, a payroll run. That is the safer design and the reason it was
// built that way: a shop cannot mistype its way into a wrong ledger, because
// nobody types the ledger.
//
// C10 asks for the exception, and names what it is for: "accounting
// adjustments". An accountant closing a year has to "post adjusting entries"
// before revenue and expense are swept into retained earnings — an accrual for
// a bill that has not arrived, a write-off, a correction of a misposting. None
// of those has a document to derive them from, and without this they had
// nowhere to go.
//
// # What it does not relax
//
// A manual journal is an ordinary entry with a reason attached. It goes through
// `Post` like everything else, so it balances or it is refused, it is written
// into the period covering its date or refused if that period is closed, its
// accounts are checked against the company, and the trial balance and the
// statements pick it up without knowing it was typed.
//
// What is added here is validation of the one thing that is now user input: the
// lines themselves. `Post` refuses an unbalanced entry with an internal error,
// which is right when a posting rule produced it — a rule that does not balance
// is a bug in the rule. When a person typed it, the same fact is a validation
// failure and has to say which way and by how much.
package accounting

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

// JournalService writes and reads manual journals.
type JournalService struct{ pool *db.Pool }

// NewJournalService builds it.
func NewJournalService(pool *db.Pool) *JournalService {
	return &JournalService{pool: pool}
}

// JournalScope is who is writing and for which company.
//
// Never taken from the request body: the tenant and the company come from the
// authenticated caller, and the company has already been checked against the
// companies that caller may reach.
type JournalScope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// NewJournalLine is one leg as the person entering it describes it.
type NewJournalLine struct {
	AccountID uuid.UUID
	Debit     decimal.Decimal
	Credit    decimal.Decimal
	Memo      string
	StoreID   *uuid.UUID
}

// NewJournal is an adjustment somebody is asking to post.
type NewJournal struct {
	// UUID is the client's own identifier, so a retry that lost its response
	// finds the journal it already posted rather than posting a second one.
	UUID uuid.UUID

	Date   time.Time
	Reason string
	Memo   string
	Lines  []NewJournalLine
}

// Journal is a posted adjustment as a screen reads it.
type Journal struct {
	ID         uuid.UUID     `json:"id"`
	JournalNo  string        `json:"journal_no"`
	EntryID    uuid.UUID     `json:"journal_entry_id"`
	EntryNo    int64         `json:"entry_no"`
	Date       string        `json:"entry_date"`
	Reason     string        `json:"reason"`
	Memo       string        `json:"memo,omitempty"`
	Currency   string        `json:"currency"`
	Total      string        `json:"total"`
	Lines      []JournalLine `json:"lines"`
	ReversesID *uuid.UUID    `json:"reverses_id,omitempty"`
	ReversedBy *uuid.UUID    `json:"reversed_by,omitempty"`
	CreatedBy  string        `json:"created_by,omitempty"`
	CreatedAt  string        `json:"created_at"`

	// AlreadyRecorded is set when this UUID had been posted before and the
	// stored entry is being returned instead of a second one.
	//
	// The route answered 201 either way, which says "created" of something it
	// did not create. Every other idempotent path in the product answers 200
	// with `Idempotency-Replayed` on a repeat -- a sale, an exchange, a
	// purchase return -- and a client that branched on the status to tell
	// "posted" from "already posted" was told wrongly here.
	AlreadyRecorded bool `json:"already_recorded"`
}

// JournalLine is one posted leg.
type JournalLine struct {
	AccountID uuid.UUID `json:"account_id"`
	Code      string    `json:"account_code"`
	Name      string    `json:"account_name"`
	Debit     string    `json:"debit"`
	Credit    string    `json:"credit"`
	Memo      string    `json:"memo,omitempty"`
}

// maxJournalLines caps one adjustment.
//
// Not a business rule — an adjustment with hundreds of legs is a data import
// wearing a journal's clothes, and `internal/portability` is where that
// belongs. The cap keeps one request from holding a transaction open across
// thousands of account lookups.
const maxJournalLines = 200

// Record posts an adjustment.
func (s *JournalService) Record(
	ctx context.Context, scope JournalScope, in NewJournal,
) (Journal, error) {
	if err := validateJournal(in); err != nil {
		return Journal{}, err
	}

	var out Journal
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		// Already written. Answered rather than refused, so a retry that lost
		// its response gets the same journal instead of a conflict.
		var existing uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT id FROM manual_journal
			WHERE company_id = $1 AND uuid = $2`,
			scope.CompanyID, in.UUID).Scan(&existing)
		if e == nil {
			var readErr error
			out, readErr = s.read(ctx, tx, scope.CompanyID, existing)
			out.AlreadyRecorded = true
			return readErr
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		var currency string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return errs.New(errs.CodeNotFound, "That company was not found.")
			}
			return e
		}

		var journalNo string
		if e := tx.QueryRow(ctx, `SELECT claim_journal_no($1)`,
			scope.CompanyID).Scan(&journalNo); e != nil {
			return db.Translate(e, "A journal number could not be issued.")
		}

		journalID := uuid.New()
		lines := make([]Line, 0, len(in.Lines))
		for _, l := range in.Lines {
			side, amount := Debit, l.Debit
			if l.Credit.IsPositive() {
				side, amount = Credit, l.Credit
			}
			accountID := l.AccountID
			lines = append(lines, Line{
				AccountID: &accountID,
				Side:      side,
				Amount:    amount,
				Memo:      strings.TrimSpace(l.Memo),
				StoreID:   l.StoreID,
			})
		}

		// Through Post like every other entry: it balances or it is refused,
		// the period is resolved and locked, and the accounts are checked
		// against this company. `manual_journal` is the source, and its id the
		// idempotency key, so a journal cannot post two entries.
		result, e := Post(ctx, tx, Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date:       in.Date,
			SourceType: "manual_journal", SourceID: journalID,
			Currency: currency, BaseCurrency: currency,
			FXRate:   decimal.NewFromInt(1),
			Memo:     firstNonBlank(in.Memo, in.Reason),
			PostedBy: &scope.UserID,
			Lines:    lines,
		})
		if e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO manual_journal
			  (id, tenant_id, company_id, uuid, journal_no, entry_date,
			   reason, memo, journal_entry_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6::date,$7,nullif($8,''),$9,$10)`,
			journalID, scope.TenantID, scope.CompanyID, in.UUID, journalNo,
			in.Date, strings.TrimSpace(in.Reason), strings.TrimSpace(in.Memo),
			result.EntryID, scope.UserID); e != nil {
			return db.Translate(e, "That journal could not be recorded.")
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "manual_journal_posted",
			EntityType: "manual_journal", EntityID: &journalID,
			After: map[string]any{
				"journal_no": journalNo,
				"entry_date": in.Date.Format("2006-01-02"),
				"reason":     strings.TrimSpace(in.Reason),
				"lines":      len(lines),
			},
		}); e != nil {
			return e
		}

		var readErr error
		out, readErr = s.read(ctx, tx, scope.CompanyID, journalID)
		return readErr
	})
	return out, db.Translate(err, "")
}

// Reverse posts the opposite of a journal already written.
//
// The lines come from the ENTRY rather than from the request, so the reversal
// undoes what was actually posted. Re-deriving from the original request would
// be the same mistake the customer-receipt reversal carried: it looks
// equivalent until the entry carries something the request did not describe.
func (s *JournalService) Reverse(
	ctx context.Context, scope JournalScope, journalID uuid.UUID,
	reason string, noteUUID uuid.UUID,
) (Journal, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Journal{}, errs.Validation("Say why this is being reversed.").
			WithField("reason",
				"The ledger will carry an opposite entry, and this is the "+
					"only place that says what it was for.")
	}

	var out Journal
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var entryID uuid.UUID
		var originalNo string
		var alreadyReverses *uuid.UUID
		e := tx.QueryRow(ctx, `
			SELECT journal_entry_id, journal_no, reverses_id
			FROM manual_journal
			WHERE id = $1 AND company_id = $2
			FOR UPDATE`, journalID, scope.CompanyID).
			Scan(&entryID, &originalNo, &alreadyReverses)
		if errors.Is(e, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound, "That journal was not found.")
		}
		if e != nil {
			return e
		}
		if alreadyReverses != nil {
			return errs.New(errs.CodeConflict,
				"That journal is itself a reversal, so it cannot be reversed "+
					"again. Post a new adjustment instead.")
		}

		var reversalID uuid.UUID
		e = tx.QueryRow(ctx,
			`SELECT id FROM manual_journal WHERE reverses_id = $1`,
			journalID).Scan(&reversalID)
		if e == nil {
			var readErr error
			out, readErr = s.read(ctx, tx, scope.CompanyID, reversalID)
			return readErr
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		lines, e := LinesOf(ctx, tx, entryID)
		if e != nil {
			return e
		}

		var currency string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency); e != nil {
			return e
		}
		var journalNo string
		if e := tx.QueryRow(ctx, `SELECT claim_journal_no($1)`,
			scope.CompanyID).Scan(&journalNo); e != nil {
			return db.Translate(e, "A journal number could not be issued.")
		}

		// Dated today, not the day the original was posted: the correction
		// happens now, and the period the original belongs to may be closed.
		on := time.Now().UTC()
		newID := uuid.New()
		result, e := Post(ctx, tx, Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date:       on,
			SourceType: "manual_journal", SourceID: newID,
			Currency: currency, BaseCurrency: currency,
			FXRate:     decimal.NewFromInt(1),
			Memo:       "Reversal of " + originalNo,
			PostedBy:   &scope.UserID,
			ReversesID: &entryID,
			Lines:      FlipSides(lines),
		})
		if e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO manual_journal
			  (id, tenant_id, company_id, uuid, journal_no, entry_date,
			   reason, memo, journal_entry_id, reverses_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6::date,$7,$8,$9,$10,$11)`,
			newID, scope.TenantID, scope.CompanyID, noteUUID, journalNo, on,
			reason, "Reversal of "+originalNo, result.EntryID, journalID,
			scope.UserID); e != nil {
			return db.Translate(e, "That reversal could not be recorded.")
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "manual_journal_reversed",
			EntityType: "manual_journal", EntityID: &newID,
			Before: map[string]any{"journal_no": originalNo},
			After: map[string]any{
				"journal_no": journalNo, "reason": reason,
				"reverses": journalID.String(),
			},
		}); e != nil {
			return e
		}

		var readErr error
		out, readErr = s.read(ctx, tx, scope.CompanyID, newID)
		return readErr
	})
	return out, db.Translate(err, "")
}

// validateJournal checks what a person typed, before any of it reaches the
// ledger.
func validateJournal(in NewJournal) error {
	if strings.TrimSpace(in.Reason) == "" {
		return errs.Validation("Say why this adjustment is being made.").
			WithField("reason",
				"C10 requires a reason on every manual journal. It is what "+
					"somebody reading the ledger a year from now has to go on.")
	}
	if in.Date.IsZero() {
		return errs.Validation("Say which date this journal belongs to.").
			WithField("entry_date",
				"The entry is written into the accounting period covering "+
					"this date.")
	}
	if len(in.Lines) < 2 {
		return errs.Validation(
			"A journal needs at least two lines.").
			WithField("lines",
				"Every entry has something debited and something credited.")
	}
	if len(in.Lines) > maxJournalLines {
		return errs.Newf(errs.CodeInvalidInput,
			"A journal can carry at most %d lines. Anything larger is an "+
				"import rather than an adjustment.", maxJournalLines)
	}

	debits, credits := decimal.Zero, decimal.Zero
	for i, l := range in.Lines {
		if l.AccountID == uuid.Nil {
			return errs.Newf(errs.CodeInvalidInput,
				"Line %d does not name an account.", i+1)
		}
		if l.Debit.IsNegative() || l.Credit.IsNegative() {
			return errs.Newf(errs.CodeInvalidInput,
				"Line %d has a negative amount. Move it to the other side "+
					"instead: a negative debit is a credit.", i+1)
		}
		if l.Debit.IsPositive() == l.Credit.IsPositive() {
			// Both set, or neither. Both is ambiguous and neither is empty.
			return errs.Newf(errs.CodeInvalidInput,
				"Line %d must have either a debit or a credit, and not both.",
				i+1)
		}
		debits = debits.Add(l.Debit)
		credits = credits.Add(l.Credit)
	}

	if !debits.Equal(credits) {
		// Said as a difference rather than as two totals, because the number
		// the person needs is the one they have to find.
		return errs.Newf(errs.CodeInvalidInput,
			"This journal does not balance: debits come to %s and credits to "+
				"%s, a difference of %s.",
			debits.StringFixed(2), credits.StringFixed(2),
			debits.Sub(credits).Abs().StringFixed(2))
	}
	if !debits.IsPositive() {
		return errs.New(errs.CodeInvalidInput,
			"A journal of nothing changes nothing. Enter the amounts being "+
				"adjusted.")
	}
	return nil
}

func firstNonBlank(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// --- reading --------------------------------------------------------------

// read assembles one journal, lines included.
func (s *JournalService) read(
	ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
) (Journal, error) {
	var j Journal
	var memo, createdBy *string
	var currency string
	err := tx.QueryRow(ctx, `
		SELECT m.id, m.journal_no, m.journal_entry_id, e.entry_no,
		       to_char(m.entry_date, 'YYYY-MM-DD'), m.reason, m.memo,
		       co.base_currency,
		       m.reverses_id,
		       (SELECT r.id FROM manual_journal r WHERE r.reverses_id = m.id),
		       u.full_name,
		       to_char(m.created_at, 'YYYY-MM-DD"T"HH24:MI:SSZ')
		FROM manual_journal m
		JOIN journal_entry e ON e.id = m.journal_entry_id
		JOIN company co ON co.id = m.company_id
		LEFT JOIN app_user u ON u.id = m.created_by
		WHERE m.id = $1 AND m.company_id = $2`, id, companyID).
		Scan(&j.ID, &j.JournalNo, &j.EntryID, &j.EntryNo, &j.Date, &j.Reason,
			&memo, &currency, &j.ReversesID, &j.ReversedBy, &createdBy,
			&j.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Journal{}, errs.New(errs.CodeNotFound,
			"That journal was not found.")
	}
	if err != nil {
		return Journal{}, err
	}
	if memo != nil {
		j.Memo = *memo
	}
	if createdBy != nil {
		j.CreatedBy = *createdBy
	}
	j.Currency = currency

	rows, err := tx.Query(ctx, `
		SELECT l.account_id, a.code, a.name, l.debit::text, l.credit::text,
		       coalesce(l.memo, '')
		FROM journal_line l
		JOIN account a ON a.id = l.account_id
		WHERE l.entry_id = $1
		ORDER BY l.line_no`, j.EntryID)
	if err != nil {
		return Journal{}, err
	}
	defer rows.Close()

	j.Lines = []JournalLine{}
	total := decimal.Zero
	for rows.Next() {
		var l JournalLine
		if err := rows.Scan(&l.AccountID, &l.Code, &l.Name, &l.Debit,
			&l.Credit, &l.Memo); err != nil {
			return Journal{}, err
		}
		if d, e := decimal.NewFromString(l.Debit); e == nil {
			total = total.Add(d)
		}
		j.Lines = append(j.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return Journal{}, err
	}
	// The journal's size, which is the debit total: it balances, so either
	// side says the same thing and the debit side is the conventional one.
	j.Total = total.StringFixed(2)
	return j, nil
}

// Journal reads one by id.
func (s *JournalService) Journal(
	ctx context.Context, scope JournalScope, id uuid.UUID,
) (Journal, error) {
	var out Journal
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var e error
		out, e = s.read(ctx, tx, scope.CompanyID, id)
		return e
	})
	return out, db.Translate(err, "")
}

// Journals lists them, newest first.
//
// Bounded: an adjustment register grows for the life of the company, and an
// unbounded list is a query that works in the first year and times out in the
// fifth.
func (s *JournalService) Journals(
	ctx context.Context, scope JournalScope, from, to string, limit int,
) ([]Journal, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []Journal{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT m.id
			FROM manual_journal m
			WHERE m.company_id = $1
			  AND ($2 = '' OR m.entry_date >= $2::date)
			  AND ($3 = '' OR m.entry_date <= $3::date)
			ORDER BY m.entry_date DESC, m.journal_no DESC
			LIMIT $4`, scope.CompanyID, from, to, limit)
		if e != nil {
			return e
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if e := rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		for _, id := range ids {
			j, e := s.read(ctx, tx, scope.CompanyID, id)
			if e != nil {
				return e
			}
			out = append(out, j)
		}
		return nil
	})
	return out, db.Translate(err, "")
}
