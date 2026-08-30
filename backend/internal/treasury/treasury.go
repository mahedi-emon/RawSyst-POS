// Package treasury is where the money physically is (blueprint C2 and C11).
//
// Cash accounts, bank accounts, the transfers between them, and the
// reconciliation that proves the books agree with what the bank says.
//
// # The balance always comes from the ledger
//
// `money_account` carries an IBAN, a bank name and a kind. It does not carry a
// balance, and nothing here ever writes one. Every figure this package reports
// is summed from `journal_line` at read time.
//
// That is not laziness. A stored balance is a second copy of a number the
// ledger already holds, and two copies of a number are two numbers that can
// disagree — at which point somebody has to decide which is right, and the
// honest answer is always the ledger, because the ledger is what the financial
// statements are drawn from. The moment a cached balance exists, so does the
// possibility of a bank screen and a balance sheet that do not match.
//
// # Reconciliation proves a negative
//
// The valuable output of C11 is not the matched lines. It is the two lists of
// things that did NOT match: bank lines with no ledger entry, which are the
// charges and interest nobody keyed, and ledger entries with no bank line,
// which are the cheques that never cleared and the payments recorded twice.
//
// So `Reconcile` reports both sides and refuses to sign off while the
// difference is not nil. A reconciliation that can be signed with an unexplained
// difference is a reconciliation that means nothing.
package treasury

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/accounting"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The kinds of place money sits, matching 0081's CHECK.
const (
	KindCash           = "cash"
	KindPettyCash      = "petty_cash"
	KindBank           = "bank"
	KindCardSettlement = "card_settlement"
	KindGateway        = "gateway"
)

var kinds = map[string]bool{
	KindCash: true, KindPettyCash: true, KindBank: true,
	KindCardSettlement: true, KindGateway: true,
}

// bankLike reports whether a kind may carry bank detail. The same three the
// schema allows it on.
func bankLike(kind string) bool {
	return kind == KindBank || kind == KindCardSettlement || kind == KindGateway
}

// Service manages money accounts, transfers and reconciliations.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on behalf of which legal entity.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Account is one place money sits.
type Account struct {
	ID       uuid.UUID `json:"id"`
	Kind     string    `json:"kind"`
	Name     string    `json:"name"`
	NameAr   string    `json:"name_ar,omitempty"`
	Currency string    `json:"currency"`
	Store    string    `json:"store,omitempty"`
	Active   bool      `json:"is_active"`

	AccountID   uuid.UUID `json:"account_id"`
	AccountCode string    `json:"account_code"`

	BankName      string `json:"bank_name,omitempty"`
	AccountNumber string `json:"account_number,omitempty"`
	IBAN          string `json:"iban,omitempty"`
	SWIFT         string `json:"swift,omitempty"`

	// Balance is summed from the ledger every time. See the package note on why
	// it is never stored.
	Balance string `json:"balance"`

	// Unreconciled is how many ledger lines on this account have never been
	// seen on a statement. Only meaningful on a bank-like account, and the
	// number a person actually opens this screen for.
	Unreconciled int `json:"unreconciled,omitempty"`
}

// NewAccount is a place being added.
type NewAccount struct {
	Kind     string
	Name     string
	NameAr   string
	Currency string
	StoreID  *uuid.UUID

	// AccountID is the chart account this money lands in. Chosen rather than
	// created: which ledger account a bank account posts to is a decision about
	// the chart, and a module that invented accounts would put two authorities
	// over the chart of accounts.
	AccountID uuid.UUID

	BankName      string
	AccountNumber string
	IBAN          string
	SWIFT         string
}

// Accounts lists where the company's money is.
func (s *Service) Accounts(
	ctx context.Context, scope Scope, includeRetired bool,
) ([]Account, error) {
	out := []Account{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT m.id, m.kind, m.name, coalesce(m.name_ar, ''), m.currency,
			       coalesce(st.name, ''), m.is_active,
			       m.account_id, a.code,
			       coalesce(m.bank_name, ''), coalesce(m.account_number, ''),
			       coalesce(m.iban, ''), coalesce(m.swift, ''),
			       coalesce((SELECT sum(l.base_debit - l.base_credit)
			                 FROM journal_line l WHERE l.account_id = m.account_id), 0),
			       (SELECT count(*)
			        FROM journal_line l
			        WHERE l.account_id = m.account_id
			          AND NOT EXISTS (
			            SELECT 1 FROM bank_statement_line b
			            WHERE b.journal_line_id = l.id))
			FROM money_account m
			JOIN account a ON a.id = m.account_id
			LEFT JOIN store st ON st.id = m.store_id
			WHERE m.company_id = $1 AND ($2 OR m.is_active)
			ORDER BY m.kind, a.code`,
			scope.CompanyID, includeRetired)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var a Account
			var balance decimal.Decimal
			if err := rows.Scan(&a.ID, &a.Kind, &a.Name, &a.NameAr, &a.Currency,
				&a.Store, &a.Active, &a.AccountID, &a.AccountCode,
				&a.BankName, &a.AccountNumber, &a.IBAN, &a.SWIFT,
				&balance, &a.Unreconciled); err != nil {
				return err
			}
			a.Balance = balance.StringFixed(2)
			if !bankLike(a.Kind) {
				// Only meaningful where there is a statement to reconcile
				// against. A count of unreconciled lines on the petty cash tin
				// would be a number with no question behind it.
				a.Unreconciled = 0
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

// CreateAccount adds a place money sits.
func (s *Service) CreateAccount(
	ctx context.Context, scope Scope, in NewAccount,
) (Account, error) {
	kind := strings.TrimSpace(in.Kind)
	name := strings.TrimSpace(in.Name)

	if !kinds[kind] {
		return Account{}, errs.Newf(errs.CodeInvalidInput,
			"%q is not a kind of money account.", in.Kind)
	}
	if name == "" {
		return Account{}, errs.Validation("Give the account a name.").
			WithField("name", "What the people who use it call it.")
	}
	if !bankLike(kind) && (in.IBAN != "" || in.BankName != "" || in.AccountNumber != "") {
		return Account{}, errs.New(errs.CodeInvalidInput,
			"A cash account has no bank details. Choose a bank account if that "+
				"is what this is.")
	}

	var out Account
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		currency := strings.ToUpper(strings.TrimSpace(in.Currency))
		if currency == "" {
			if e := tx.QueryRow(ctx,
				`SELECT base_currency FROM company WHERE id = $1`,
				scope.CompanyID).Scan(&currency); e != nil {
				return e
			}
		}

		var id uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO money_account
			  (tenant_id, company_id, account_id, store_id, kind, name, name_ar,
			   currency, bank_name, account_number, iban, swift)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			RETURNING id`,
			scope.TenantID, scope.CompanyID, in.AccountID, in.StoreID,
			kind, name, nullIfBlank(in.NameAr), currency,
			nullIfBlank(in.BankName), nullIfBlank(in.AccountNumber),
			nullIfBlank(strings.ToUpper(in.IBAN)), nullIfBlank(in.SWIFT)).
			Scan(&id); e != nil {
			return db.Translate(e, "That account could not be added.")
		}

		read, e := s.readAccount(ctx, tx, scope, id)
		out = read
		return e
	})
	return out, err
}

// SetAccountActive retires an account or brings it back.
//
// Retiring one that still holds money is refused, for the reason retiring a
// stock location that holds stock is: hiding it does not empty it, and the
// balance sheet keeps counting what the screens stop showing.
func (s *Service) SetAccountActive(
	ctx context.Context, scope Scope, id uuid.UUID, active bool,
) error {
	return s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var name string
		var balance decimal.Decimal
		err := tx.QueryRow(ctx, `
			SELECT m.name,
			       coalesce((SELECT sum(l.base_debit - l.base_credit)
			                 FROM journal_line l WHERE l.account_id = m.account_id), 0)
			FROM money_account m
			WHERE m.id = $1 AND m.company_id = $2`,
			id, scope.CompanyID).Scan(&name, &balance)
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.New(errs.CodeNotFound,
				"That account is not this business's.")
		}
		if err != nil {
			return err
		}

		if !active && !balance.IsZero() {
			return errs.Newf(errs.CodeConflict,
				"%s still holds %s. Move it somewhere else first — retiring the "+
					"account would hide the money rather than move it.",
				name, balance.StringFixed(2))
		}

		_, err = tx.Exec(ctx,
			`UPDATE money_account SET is_active = $3 WHERE id = $1 AND company_id = $2`,
			id, scope.CompanyID, active)
		return err
	})
}

func (s *Service) readAccount(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Account, error) {
	var a Account
	var balance decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT m.id, m.kind, m.name, coalesce(m.name_ar, ''), m.currency,
		       coalesce(st.name, ''), m.is_active, m.account_id, ac.code,
		       coalesce(m.bank_name, ''), coalesce(m.account_number, ''),
		       coalesce(m.iban, ''), coalesce(m.swift, ''),
		       coalesce((SELECT sum(l.base_debit - l.base_credit)
		                 FROM journal_line l WHERE l.account_id = m.account_id), 0)
		FROM money_account m
		JOIN account ac ON ac.id = m.account_id
		LEFT JOIN store st ON st.id = m.store_id
		WHERE m.id = $1 AND m.company_id = $2`,
		id, scope.CompanyID).
		Scan(&a.ID, &a.Kind, &a.Name, &a.NameAr, &a.Currency, &a.Store,
			&a.Active, &a.AccountID, &a.AccountCode, &a.BankName,
			&a.AccountNumber, &a.IBAN, &a.SWIFT, &balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, errs.New(errs.CodeNotFound,
			"That account is not this business's.")
	}
	a.Balance = balance.StringFixed(2)
	return a, err
}

func nullIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

// --- transfers ------------------------------------------------------------

// NewTransfer is money moving between two of the company's own accounts.
type NewTransfer struct {
	// UUID is assigned by the caller, so a retry after a lost response returns
	// the original rather than moving the money twice.
	UUID uuid.UUID

	FromAccountID uuid.UUID
	ToAccountID   uuid.UUID
	Amount        decimal.Decimal
	MovedOn       time.Time
	Reference     string
	Note          string
}

// Transfer is a recorded movement.
type Transfer struct {
	ID        uuid.UUID `json:"id"`
	Number    string    `json:"transfer_no"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Amount    string    `json:"amount"`
	Currency  string    `json:"currency"`
	MovedOn   string    `json:"moved_on"`
	Reference string    `json:"reference,omitempty"`
	Note      string    `json:"note,omitempty"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt string    `json:"created_at"`

	AlreadyRecorded bool `json:"already_recorded,omitempty"`
}

// Move records a transfer and posts it through rule 9.
func (s *Service) Move(
	ctx context.Context, scope Scope, in NewTransfer,
) (Transfer, error) {
	if in.UUID == uuid.Nil {
		return Transfer{}, errs.New(errs.CodeInvalidInput,
			"A transfer must carry an identifier so a retry does not move the "+
				"money twice.")
	}
	if !in.Amount.IsPositive() {
		return Transfer{}, errs.New(errs.CodeInvalidInput,
			"Say how much is being moved.")
	}
	if in.FromAccountID == in.ToAccountID {
		return Transfer{}, errs.New(errs.CodeInvalidInput,
			"Money cannot be moved to where it already is.")
	}

	var out Transfer
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var exists bool
		if e := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM money_transfer WHERE id = $1 AND company_id = $2)`,
			in.UUID, scope.CompanyID).Scan(&exists); e != nil {
			return e
		}
		if exists {
			read, e := s.readTransfer(ctx, tx, scope, in.UUID)
			if e != nil {
				return e
			}
			read.AlreadyRecorded = true
			out = read
			return nil
		}

		from, e := s.accountForPosting(ctx, tx, scope, in.FromAccountID)
		if e != nil {
			return e
		}
		to, e := s.accountForPosting(ctx, tx, scope, in.ToAccountID)
		if e != nil {
			return e
		}

		// Two accounts in different currencies is a foreign-exchange
		// transaction, and C9.4 requires an exchange gain or loss to be posted
		// on it. That rule is not built, and inventing one here would produce a
		// journal entry that balances and misstates the gain.
		if from.currency != to.currency {
			return errs.Newf(errs.CodeConflict,
				"%s is held in %s and %s in %s. Moving money between "+
					"currencies posts an exchange difference, which this "+
					"product does not calculate yet.",
				from.name, from.currency, to.name, to.currency)
		}

		number, e := claimTransferNo(ctx, tx, scope.CompanyID)
		if e != nil {
			return e
		}

		movedOn := in.MovedOn
		if movedOn.IsZero() {
			movedOn = time.Now().UTC()
		}

		var country string
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
			Scan(&country); e != nil {
			return e
		}

		// Rule 9. Both ends are named by the TRANSACTION rather than by the
		// rule, which is why it could not be called until something knew what
		// the two ends were.
		fromAccount, toAccount := from.accountID, to.accountID
		result, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date: movedOn, SourceType: "money_transfer", SourceID: in.UUID,
			RuleKey: "transfer.internal", PostedBy: &scope.UserID,
			Memo: transferMemo(number, in.Note),
		}, country, accounting.Transaction{
			Groups: map[string]accounting.Group{
				"destination": {{AccountID: &toAccount, Amount: in.Amount, Memo: to.name}},
				"source":      {{AccountID: &fromAccount, Amount: in.Amount, Memo: from.name}},
			},
		})
		if e != nil {
			return e
		}

		// The row is written ONCE, with its entry already on it.
		//
		// It was an insert followed by an update that attached the entry, and
		// 0081's immutability trigger refused the update — correctly. A record
		// of money that moved does not change, and that has to include the
		// moment it is being written: a row that is complete only after a
		// second statement is a row that can exist incomplete.
		if _, e := tx.Exec(ctx, `
			INSERT INTO money_transfer
			  (id, tenant_id, company_id, transfer_no, from_account_id,
			   to_account_id, amount, moved_on, reference, note,
			   journal_entry_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			in.UUID, scope.TenantID, scope.CompanyID, number,
			in.FromAccountID, in.ToAccountID, in.Amount, movedOn,
			nullIfBlank(in.Reference), nullIfBlank(in.Note),
			result.EntryID, scope.UserID); e != nil {
			return db.Translate(e, "That transfer could not be recorded.")
		}

		// C2: "every transfer creates its own audit entry".
		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "money_transferred",
			EntityType: "money_transfer", EntityID: &in.UUID,
			After: map[string]any{
				"voucher": number, "amount": in.Amount.StringFixed(2),
				"from": from.name, "to": to.name,
			},
		}); e != nil {
			return e
		}

		read, e := s.readTransfer(ctx, tx, scope, in.UUID)
		out = read
		return e
	})
	return out, err
}

type postingAccount struct {
	accountID uuid.UUID
	name      string
	currency  string
}

func (s *Service) accountForPosting(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (postingAccount, error) {
	var a postingAccount
	var active bool
	err := tx.QueryRow(ctx, `
		SELECT account_id, name, currency, is_active
		FROM money_account WHERE id = $1 AND company_id = $2`,
		id, scope.CompanyID).Scan(&a.accountID, &a.name, &a.currency, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return postingAccount{}, errs.New(errs.CodeNotFound,
			"One of those accounts is not this business's.")
	}
	if err != nil {
		return postingAccount{}, err
	}
	if !active {
		return postingAccount{}, errs.Newf(errs.CodeConflict,
			"%s has been retired, so money cannot be moved through it.", a.name)
	}
	return a, nil
}

func (s *Service) readTransfer(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID,
) (Transfer, error) {
	var t Transfer
	var amount decimal.Decimal
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT tr.id, tr.transfer_no, f.name, d.name, tr.amount, f.currency,
		       to_char(tr.moved_on, 'YYYY-MM-DD'),
		       coalesce(tr.reference, ''), coalesce(tr.note, ''),
		       coalesce(u.full_name, ''), tr.created_at
		FROM money_transfer tr
		JOIN money_account f ON f.id = tr.from_account_id
		JOIN money_account d ON d.id = tr.to_account_id
		LEFT JOIN app_user u ON u.id = tr.created_by
		WHERE tr.id = $1 AND tr.company_id = $2`,
		id, scope.CompanyID).
		Scan(&t.ID, &t.Number, &t.From, &t.To, &amount, &t.Currency,
			&t.MovedOn, &t.Reference, &t.Note, &t.CreatedBy, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, errs.New(errs.CodeNotFound,
			"That transfer is not this business's.")
	}
	t.Amount = amount.StringFixed(2)
	t.CreatedAt = createdAt.Format(time.RFC3339)
	return t, err
}

// Transfers lists them, newest first.
func (s *Service) Transfers(
	ctx context.Context, scope Scope, limit int,
) ([]Transfer, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	out := []Transfer{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tr.id, tr.transfer_no, f.name, d.name, tr.amount, f.currency,
			       to_char(tr.moved_on, 'YYYY-MM-DD'),
			       coalesce(tr.reference, ''), coalesce(tr.note, ''),
			       coalesce(u.full_name, ''), tr.created_at
			FROM money_transfer tr
			JOIN money_account f ON f.id = tr.from_account_id
			JOIN money_account d ON d.id = tr.to_account_id
			LEFT JOIN app_user u ON u.id = tr.created_by
			WHERE tr.company_id = $1
			ORDER BY tr.moved_on DESC, tr.created_at DESC
			LIMIT $2`, scope.CompanyID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var t Transfer
			var amount decimal.Decimal
			var createdAt time.Time
			if err := rows.Scan(&t.ID, &t.Number, &t.From, &t.To, &amount,
				&t.Currency, &t.MovedOn, &t.Reference, &t.Note,
				&t.CreatedBy, &createdAt); err != nil {
				return err
			}
			t.Amount = amount.StringFixed(2)
			t.CreatedAt = createdAt.Format(time.RFC3339)
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

func claimTransferNo(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (string, error) {
	var n string
	err := tx.QueryRow(ctx,
		`SELECT claim_transfer_voucher_no($1)`, companyID).Scan(&n)
	return n, err
}

func transferMemo(number, note string) string {
	if n := strings.TrimSpace(note); n != "" {
		return n
	}
	return "Transfer " + number
}
