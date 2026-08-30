// Package wallet is store credit and gift cards (blueprint B16).
//
// # A balance is a sum, never a column
//
// There is no `balance` anywhere in this package's tables. A customer's wallet
// is the sum of their entries and a gift card's balance is the sum of its own,
// and both tie to account 2300 by construction. A stored balance would be a
// second copy of a number the general ledger already holds, and the two would
// disagree the first time a write half-succeeded.
//
// # Nothing here can be spent twice
//
// `Draw` takes the row it is spending against FOR UPDATE and re-reads the
// balance inside that lock. Two tills settling the same gift card at the same
// moment is not a hypothetical: a card is a piece of plastic that can be handed
// to two people, and the whole point of a balance check is that it holds when
// somebody tries.
//
// # Issuing credit and taking money are different acts
//
// Selling a gift card takes money and owes goods: no revenue, no VAT, straight
// into the liability. Giving somebody credit as a goodwill gesture takes no
// money and costs the shop, so it lands in Sales Discounts. Two rules, because
// they are two different things that happen to end in the same account.
package wallet

import (
	"context"
	"crypto/rand"
	"math/big"
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

// Why an entry exists.
const (
	ReasonIssued   = "issued"
	ReasonRefunded = "refunded"
	ReasonRedeemed = "redeemed"
	ReasonExpired  = "expired"
	ReasonVoided   = "voided"
	ReasonAdjusted = "adjusted"
)

// Service reads and moves store credit.
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

// Entry is one movement of store credit.
type Entry struct {
	ID       uuid.UUID `json:"id"`
	Amount   string    `json:"amount"`
	Currency string    `json:"currency"`
	Reason   string    `json:"reason"`

	CustomerID *uuid.UUID `json:"customer_id,omitempty"`
	Customer   string     `json:"customer,omitempty"`
	CardID     *uuid.UUID `json:"gift_card_id,omitempty"`
	CardCode   string     `json:"gift_card_code,omitempty"`

	InvoiceID *uuid.UUID `json:"invoice_id,omitempty"`
	InvoiceNo string     `json:"invoice_no,omitempty"`
	ExpiresOn string     `json:"expires_on,omitempty"`
	Note      string     `json:"note,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
	CreatedAt string     `json:"created_at"`
}

// Wallet is what a customer has to spend.
type Wallet struct {
	CustomerID uuid.UUID `json:"customer_id"`
	Customer   string    `json:"customer,omitempty"`
	Balance    string    `json:"balance"`
	Currency   string    `json:"currency"`
	Entries    []Entry   `json:"entries,omitempty"`
}

// Card is a gift card and what is left on it.
type Card struct {
	ID        uuid.UUID `json:"id"`
	Code      string    `json:"code"`
	FaceValue string    `json:"face_value"`
	Balance   string    `json:"balance"`
	Currency  string    `json:"currency"`

	ExpiresOn string `json:"expires_on,omitempty"`
	// Expired is derived from the date rather than stored: a card does not
	// become a different row at midnight.
	Expired bool   `json:"expired,omitempty"`
	Void    bool   `json:"is_void,omitempty"`
	VoidWhy string `json:"void_reason,omitempty"`

	CustomerID *uuid.UUID `json:"customer_id,omitempty"`
	Customer   string     `json:"customer,omitempty"`

	Note     string  `json:"note,omitempty"`
	IssuedBy string  `json:"issued_by,omitempty"`
	IssuedAt string  `json:"issued_at"`
	Entries  []Entry `json:"entries,omitempty"`
}

// NewCard is a gift card being sold.
type NewCard struct {
	// Code is what is printed on the card. Empty means the product generates
	// one, which is what a shop selling cards off a roll wants.
	Code       string
	FaceValue  decimal.Decimal
	ExpiresOn  *time.Time
	CustomerID *uuid.UUID
	Note       string

	// Proceeds is how the card was paid for — cash, a card machine — as the
	// money accounts it landed in. Empty means the card was given away, which
	// a shop does for a complaint and which costs it rather than earning it.
	Proceeds []Payment
}

// Payment is one way money arrived, and where it landed.
type Payment struct {
	// Role is the account role the money went into: "cash", "bank",
	// "card_clearing". Named as a role rather than an account so a shop that
	// renamed its Cash account does not break the posting.
	Role   string
	Amount decimal.Decimal
}

// CustomerBalance is what a customer has left to spend.
//
// Takes a tx because the till calls it inside the sale transaction: a balance
// read on its own connection could be stale by the time the sale posts.
func CustomerBalance(
	ctx context.Context, tx pgx.Tx, customerID uuid.UUID,
) (decimal.Decimal, error) {
	var balance decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(amount), 0)
		FROM store_credit_entry WHERE customer_id = $1`,
		customerID).Scan(&balance)
	return balance, err
}

// CardBalance is what is left on a gift card.
func CardBalance(
	ctx context.Context, tx pgx.Tx, cardID uuid.UUID,
) (decimal.Decimal, error) {
	var balance decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT coalesce(sum(amount), 0)
		FROM store_credit_entry WHERE gift_card_id = $1`,
		cardID).Scan(&balance)
	return balance, err
}

// Draw spends store credit, and refuses to spend more than there is.
//
// Called from inside the sale transaction, which is the only place it can
// safely be called from: a balance checked in one transaction and spent in
// another is a balance that was true once.
//
// The customer row is locked before the balance is read. Two tills settling
// against the same customer at the same moment would otherwise both see the
// full balance and both spend it.
func Draw(
	ctx context.Context, tx pgx.Tx,
	tenantID, companyID, customerID uuid.UUID,
	amount decimal.Decimal, currency string,
	invoiceID *uuid.UUID, userID *uuid.UUID,
) error {
	if !amount.IsPositive() {
		return errs.New(errs.CodeInvalidInput,
			"Store credit has to be spent in a positive amount.")
	}

	var name string
	if err := tx.QueryRow(ctx,
		`SELECT name FROM customer WHERE id = $1 FOR UPDATE`,
		customerID).Scan(&name); err != nil {
		if err == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound,
				"That customer is not on this company's books.")
		}
		return err
	}

	balance, err := CustomerBalance(ctx, tx, customerID)
	if err != nil {
		return err
	}
	if balance.LessThan(amount) {
		// The numbers, not "insufficient funds". A cashier standing in front of
		// a customer has to say how much there is.
		return errs.Newf(errs.CodeConflict,
			"%s has %s of store credit and this sale wants to use %s.",
			name, balance.StringFixed(2), amount.StringFixed(2))
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO store_credit_entry
		  (tenant_id, company_id, customer_id, amount, currency, reason,
		   invoice_id, created_by)
		VALUES ($1,$2,$3,$4,$5,'redeemed',$6,$7)`,
		tenantID, companyID, customerID, amount.Neg(), strings.ToUpper(currency),
		invoiceID, userID)
	return db.Translate(err, "That store credit could not be spent.")
}

// DrawCard spends a gift card, and refuses to spend more than is on it.
func DrawCard(
	ctx context.Context, tx pgx.Tx,
	tenantID, companyID, cardID uuid.UUID,
	amount decimal.Decimal, currency string,
	invoiceID *uuid.UUID, userID *uuid.UUID,
) error {
	if !amount.IsPositive() {
		return errs.New(errs.CodeInvalidInput,
			"A gift card has to be spent in a positive amount.")
	}

	var code string
	var void bool
	var expires *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT code, is_void, expires_on FROM gift_card
		WHERE id = $1 FOR UPDATE`,
		cardID).Scan(&code, &void, &expires); err != nil {
		if err == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound, "That gift card does not exist.")
		}
		return err
	}
	if void {
		return errs.Newf(errs.CodeConflict,
			"Card %s has been cancelled and cannot be used.", code)
	}
	// Checked against the database's own clock rather than the server's, so a
	// terminal with a wrong date cannot spend an expired card.
	if expires != nil {
		var past bool
		if err := tx.QueryRow(ctx,
			`SELECT $1::date < current_date`, *expires).Scan(&past); err != nil {
			return err
		}
		if past {
			return errs.Newf(errs.CodeConflict,
				"Card %s expired on %s.", code, expires.Format("2 January 2006"))
		}
	}

	balance, err := CardBalance(ctx, tx, cardID)
	if err != nil {
		return err
	}
	if balance.LessThan(amount) {
		return errs.Newf(errs.CodeConflict,
			"Card %s has %s left and this sale wants to use %s.",
			code, balance.StringFixed(2), amount.StringFixed(2))
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO store_credit_entry
		  (tenant_id, company_id, gift_card_id, amount, currency, reason,
		   invoice_id, created_by)
		VALUES ($1,$2,$3,$4,$5,'redeemed',$6,$7)`,
		tenantID, companyID, cardID, amount.Neg(), strings.ToUpper(currency),
		invoiceID, userID)
	return db.Translate(err, "That gift card could not be spent.")
}

// FindCard resolves a code a cashier typed to a card the till can spend.
//
// Case-insensitive and space-tolerant, because the code is read off a piece of
// plastic by somebody with a queue behind them.
func FindCard(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, code string,
) (uuid.UUID, error) {
	cleaned := strings.ToUpper(strings.TrimSpace(code))
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	if cleaned == "" {
		return uuid.Nil, errs.New(errs.CodeInvalidInput, "Type the card number.")
	}

	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM gift_card
		WHERE company_id = $1
		  AND replace(replace(upper(code), ' ', ''), '-', '') = $2`,
		companyID, cleaned).Scan(&id)
	if err == pgx.ErrNoRows {
		return uuid.Nil, errs.New(errs.CodeNotFound,
			"No gift card with that number.")
	}
	return id, err
}

// Issue sells a gift card.
//
// The card and its opening entry are written together with the journal entry
// already made, so no row here is ever completed by a second statement — the
// freeze triggers on this schema refuse an UPDATE, and a record that needs two
// writes to become true is a record that is briefly false.
func (s *Service) Issue(
	ctx context.Context, scope Scope, in NewCard,
) (Card, error) {
	if !in.FaceValue.IsPositive() {
		return Card{}, errs.New(errs.CodeInvalidInput,
			"A gift card has to be worth something.")
	}

	paid := decimal.Zero
	for _, p := range in.Proceeds {
		if !p.Amount.IsPositive() {
			return Card{}, errs.New(errs.CodeInvalidInput,
				"Each payment for a gift card has to be a positive amount.")
		}
		paid = paid.Add(p.Amount)
	}
	if len(in.Proceeds) > 0 && !paid.Equal(in.FaceValue) {
		// Selling a 100 card for 80 is a promotion, not a gift card, and the
		// difference has to land somewhere a person chose.
		return Card{}, errs.Newf(errs.CodeInvalidInput,
			"That card is worth %s but the payments come to %s.",
			in.FaceValue.StringFixed(2), paid.StringFixed(2))
	}

	var out Card
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var currency, country string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency, country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency, &country); e != nil {
			return e
		}

		code := strings.ToUpper(strings.TrimSpace(in.Code))
		if code == "" {
			generated, e := generateCode(ctx, tx, scope.CompanyID)
			if e != nil {
				return e
			}
			code = generated
		}

		cardID := uuid.New()

		// Posted before the card is written, so the entry id is on the opening
		// row rather than attached to it afterwards.
		rule := "storecredit.issue"
		txn := accounting.Transaction{
			Amounts: accounting.Amounts{"amount": in.FaceValue},
		}
		if len(in.Proceeds) > 0 {
			rule = "giftcard.issue"
			group := make(accounting.Group, 0, len(in.Proceeds))
			for _, p := range in.Proceeds {
				group = append(group, accounting.GroupMember{
					Role: p.Role, Amount: p.Amount, Memo: p.Role,
				})
			}
			txn.Groups = map[string]accounting.Group{"proceeds": group}
		}

		posted, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date:       time.Now().UTC(),
			SourceType: "gift_card", SourceID: cardID,
			RuleKey: rule, PostedBy: &scope.UserID,
			Memo: "Gift card " + code,
		}, country, txn)
		if e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO gift_card
			  (id, tenant_id, company_id, code, face_value, currency,
			   expires_on, customer_id, note, issued_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			cardID, scope.TenantID, scope.CompanyID, code, in.FaceValue,
			currency, in.ExpiresOn, in.CustomerID, nullIfBlank(in.Note),
			scope.UserID); e != nil {
			return db.Translate(e, "That gift card could not be issued.")
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO store_credit_entry
			  (tenant_id, company_id, gift_card_id, amount, currency, reason,
			   journal_entry_id, expires_on, created_by)
			VALUES ($1,$2,$3,$4,$5,'issued',$6,$7,$8)`,
			scope.TenantID, scope.CompanyID, cardID, in.FaceValue, currency,
			posted.EntryID, in.ExpiresOn, scope.UserID); e != nil {
			return db.Translate(e, "That gift card could not be issued.")
		}

		if e := audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "gift_card_issued",
			EntityType: "gift_card", EntityID: &cardID,
			After: map[string]any{
				"code": code, "face_value": in.FaceValue.StringFixed(2),
				"paid_for": len(in.Proceeds) > 0,
			},
		}); e != nil {
			return e
		}

		read, e := s.card(ctx, tx, scope, cardID, false)
		out = read
		return e
	})
	return out, err
}

// generateCode invents a card number nobody has to read out twice.
//
// Sixteen digits in groups of four, which is the shape people already know from
// every other card in their wallet. Drawn from crypto/rand rather than a
// sequence: a card number that can be guessed from the one before it is a card
// number somebody spends.
func generateCode(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var sb strings.Builder
		for group := 0; group < 4; group++ {
			if group > 0 {
				sb.WriteByte('-')
			}
			n, err := rand.Int(rand.Reader, big.NewInt(10000))
			if err != nil {
				return "", err
			}
			sb.WriteString(pad4(n.Int64()))
		}
		code := sb.String()

		var taken bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM gift_card
			                WHERE company_id = $1 AND upper(code) = $2)`,
			companyID, code).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return code, nil
		}
	}
	return "", errs.New(errs.CodeInternal,
		"A card number could not be generated. Try again.")
}

func pad4(n int64) string {
	s := decimal.NewFromInt(n).String()
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

func nullIfBlank(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}
