package wallet

// Reading balances, giving credit, and taking it back when it expires.

import (
	"context"
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

// Wallet reads a customer's store credit and how it got there.
func (s *Service) Wallet(
	ctx context.Context, scope Scope, customerID uuid.UUID,
) (Wallet, error) {
	var out Wallet
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var currency string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency); e != nil {
			return e
		}

		var name string
		if e := tx.QueryRow(ctx,
			`SELECT name FROM customer WHERE id = $1`, customerID).
			Scan(&name); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That customer is not on this company's books.")
			}
			return e
		}

		balance, e := CustomerBalance(ctx, tx, customerID)
		if e != nil {
			return e
		}

		entries, e := s.entries(ctx, tx, `customer_id = $1`, customerID)
		if e != nil {
			return e
		}

		out = Wallet{
			CustomerID: customerID, Customer: name,
			Balance: balance.StringFixed(2), Currency: currency,
			Entries: entries,
		}
		return nil
	})
	return out, err
}

// Give puts credit on a customer's wallet without money changing hands.
//
// A goodwill gesture, or a refund the customer chose to take as credit. It
// costs the shop, so it posts to Sales Discounts — which is also where it comes
// back from if it expires unspent, so a credit nobody ever used nets to nothing
// over its life.
func (s *Service) Give(
	ctx context.Context, scope Scope, customerID uuid.UUID,
	amount decimal.Decimal, expiresOn *time.Time, note string,
) (Wallet, error) {
	if !amount.IsPositive() {
		return Wallet{}, errs.New(errs.CodeInvalidInput,
			"Store credit has to be given in a positive amount.")
	}
	if strings.TrimSpace(note) == "" {
		// Not bureaucracy. Credit appearing on an account with no reason is the
		// shape of both an honest mistake and a dishonest one, and the customer
		// asking "where did this come from" deserves an answer.
		return Wallet{}, errs.New(errs.CodeInvalidInput,
			"Say why this credit is being given.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var currency, country string
		if e := tx.QueryRow(ctx,
			`SELECT base_currency, country FROM company WHERE id = $1`,
			scope.CompanyID).Scan(&currency, &country); e != nil {
			return e
		}

		entryID := uuid.New()
		posted, e := accounting.PostByRule(ctx, tx, accounting.Entry{
			TenantID: scope.TenantID, CompanyID: scope.CompanyID,
			Date:       time.Now().UTC(),
			SourceType: "store_credit", SourceID: entryID,
			RuleKey: "storecredit.issue", PostedBy: &scope.UserID,
			Memo: "Store credit: " + strings.TrimSpace(note),
		}, country, accounting.Transaction{
			Amounts: accounting.Amounts{"amount": amount},
		})
		if e != nil {
			return e
		}

		if _, e := tx.Exec(ctx, `
			INSERT INTO store_credit_entry
			  (id, tenant_id, company_id, customer_id, amount, currency,
			   reason, journal_entry_id, expires_on, note, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,'issued',$7,$8,$9,$10)`,
			entryID, scope.TenantID, scope.CompanyID, customerID, amount,
			currency, posted.EntryID, expiresOn, strings.TrimSpace(note),
			scope.UserID); e != nil {
			return db.Translate(e, "That credit could not be given.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "store_credit_given",
			EntityType: "customer", EntityID: &customerID,
			After: map[string]any{
				"amount": amount.StringFixed(2), "note": strings.TrimSpace(note),
			},
		})
	})
	if err != nil {
		return Wallet{}, err
	}
	return s.Wallet(ctx, scope, customerID)
}

// Cards lists the gift cards, newest first.
func (s *Service) Cards(
	ctx context.Context, scope Scope, includeSpent bool,
) ([]Card, error) {
	out := []Card{}
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT g.id, g.code, g.face_value,
			       coalesce((SELECT sum(amount) FROM store_credit_entry e
			                 WHERE e.gift_card_id = g.id), 0),
			       g.currency,
			       coalesce(to_char(g.expires_on, 'YYYY-MM-DD'), ''),
			       g.expires_on IS NOT NULL AND g.expires_on < current_date,
			       g.is_void, coalesce(g.void_reason, ''),
			       g.customer_id, coalesce(c.name, ''),
			       coalesce(g.note, ''), coalesce(u.full_name, ''),
			       to_char(g.issued_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
			FROM gift_card g
			LEFT JOIN customer c ON c.id = g.customer_id
			LEFT JOIN app_user u ON u.id = g.issued_by
			WHERE g.company_id = $1
			  AND ($2 OR NOT g.is_void)
			ORDER BY g.issued_at DESC
			LIMIT 500`, scope.CompanyID, includeSpent)
		if e != nil {
			return e
		}
		defer rows.Close()

		for rows.Next() {
			var c Card
			var face, balance decimal.Decimal
			if e := rows.Scan(&c.ID, &c.Code, &face, &balance, &c.Currency,
				&c.ExpiresOn, &c.Expired, &c.Void, &c.VoidWhy,
				&c.CustomerID, &c.Customer, &c.Note, &c.IssuedBy,
				&c.IssuedAt); e != nil {
				return e
			}
			c.FaceValue = face.StringFixed(2)
			c.Balance = balance.StringFixed(2)
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// Card reads one gift card and everything that has happened to it.
func (s *Service) Card(
	ctx context.Context, scope Scope, id uuid.UUID,
) (Card, error) {
	var out Card
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		read, e := s.card(ctx, tx, scope, id, true)
		out = read
		return e
	})
	return out, err
}

// Lookup is what a till calls when a cashier types a card number.
//
// Behind `wallet.view`, which a cashier holds: they need to be able to say what
// is on a card before they take it in payment.
func (s *Service) Lookup(
	ctx context.Context, scope Scope, code string,
) (Card, error) {
	var out Card
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		id, e := FindCard(ctx, tx, scope.CompanyID, code)
		if e != nil {
			return e
		}
		read, e := s.card(ctx, tx, scope, id, false)
		out = read
		return e
	})
	return out, err
}

// Void cancels a gift card and writes its remaining balance back.
//
// The balance comes off with an entry rather than by hiding the card: the shop
// no longer owes that money and the general ledger has to hear about it on the
// day it stopped owing it, not never.
func (s *Service) Void(
	ctx context.Context, scope Scope, id uuid.UUID, reason string,
) (Card, error) {
	if strings.TrimSpace(reason) == "" {
		return Card{}, errs.New(errs.CodeInvalidInput,
			"Say why the card is being cancelled.")
	}

	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var code, currency, country string
		var void bool
		if e := tx.QueryRow(ctx, `
			SELECT g.code, g.currency, c.country, g.is_void
			FROM gift_card g
			JOIN company c ON c.id = g.company_id
			WHERE g.id = $1 FOR UPDATE OF g`, id).
			Scan(&code, &currency, &country, &void); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound, "That gift card does not exist.")
			}
			return e
		}
		if void {
			return errs.Newf(errs.CodeConflict,
				"Card %s has already been cancelled.", code)
		}

		balance, e := CardBalance(ctx, tx, id)
		if e != nil {
			return e
		}

		if balance.IsPositive() {
			writeBackID := uuid.New()
			posted, e := accounting.PostByRule(ctx, tx, accounting.Entry{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				Date:       time.Now().UTC(),
				SourceType: "store_credit", SourceID: writeBackID,
				RuleKey: "storecredit.writeback", PostedBy: &scope.UserID,
				Memo: "Gift card " + code + " cancelled",
			}, country, accounting.Transaction{
				Amounts: accounting.Amounts{"amount": balance},
			})
			if e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO store_credit_entry
				  (id, tenant_id, company_id, gift_card_id, amount, currency,
				   reason, journal_entry_id, note, created_by)
				VALUES ($1,$2,$3,$4,$5,$6,'voided',$7,$8,$9)`,
				writeBackID, scope.TenantID, scope.CompanyID, id, balance.Neg(),
				currency, posted.EntryID, strings.TrimSpace(reason),
				scope.UserID); e != nil {
				return db.Translate(e, "That card could not be cancelled.")
			}
		}

		if _, e := tx.Exec(ctx, `
			UPDATE gift_card SET is_void = true, void_reason = $2
			WHERE id = $1`, id, strings.TrimSpace(reason)); e != nil {
			return db.Translate(e, "That card could not be cancelled.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "gift_card_voided",
			EntityType: "gift_card", EntityID: &id,
			After: map[string]any{
				"code": code, "reason": strings.TrimSpace(reason),
				"written_back": balance.StringFixed(2),
			},
		})
	})
	if err != nil {
		return Card{}, err
	}
	return s.Card(ctx, scope, id)
}

// Expired is the count and value of credit that has run out and not yet been
// written back, and the sweep that writes it back.
type Expired struct {
	Cards   int    `json:"cards"`
	Wallets int    `json:"wallets"`
	Total   string `json:"total"`
}

// ExpireCredit writes back every gift card and wallet credit whose date has
// passed.
//
// Run on demand rather than by a timer: a shop's accountant decides when the
// books move, and a background job that quietly changed a liability on a
// Saturday would be a change nobody could attribute.
func (s *Service) ExpireCredit(ctx context.Context, scope Scope) (Expired, error) {
	var out Expired
	err := s.pool.TxAsTenant(ctx, scope.TenantID, func(tx pgx.Tx) error {
		var country string
		if e := tx.QueryRow(ctx,
			`SELECT country FROM company WHERE id = $1`, scope.CompanyID).
			Scan(&country); e != nil {
			return e
		}

		// Every source of expired credit, wallet and card alike, with what is
		// left on it. One query rather than two, because the write-back is the
		// same act either way and the entry it posts is the same rule.
		rows, e := tx.Query(ctx, `
			WITH balances AS (
			  SELECT e.customer_id, e.gift_card_id, e.currency,
			         min(e.expires_on) AS expires_on,
			         sum(e.amount) AS balance
			  FROM store_credit_entry e
			  WHERE e.company_id = $1
			  GROUP BY e.customer_id, e.gift_card_id, e.currency
			)
			SELECT customer_id, gift_card_id, currency, balance
			FROM balances
			WHERE balance > 0
			  AND expires_on IS NOT NULL
			  AND expires_on < current_date`, scope.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()

		type due struct {
			customerID *uuid.UUID
			cardID     *uuid.UUID
			currency   string
			balance    decimal.Decimal
		}
		var owed []due
		for rows.Next() {
			var d due
			if e := rows.Scan(&d.customerID, &d.cardID, &d.currency,
				&d.balance); e != nil {
				return e
			}
			owed = append(owed, d)
		}
		if e := rows.Err(); e != nil {
			return e
		}

		total := decimal.Zero
		for _, d := range owed {
			writeBackID := uuid.New()
			posted, e := accounting.PostByRule(ctx, tx, accounting.Entry{
				TenantID: scope.TenantID, CompanyID: scope.CompanyID,
				Date:       time.Now().UTC(),
				SourceType: "store_credit", SourceID: writeBackID,
				RuleKey: "storecredit.writeback", PostedBy: &scope.UserID,
				Memo: "Store credit expired",
			}, country, accounting.Transaction{
				Amounts: accounting.Amounts{"amount": d.balance},
			})
			if e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO store_credit_entry
				  (id, tenant_id, company_id, customer_id, gift_card_id,
				   amount, currency, reason, journal_entry_id, created_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7,'expired',$8,$9)`,
				writeBackID, scope.TenantID, scope.CompanyID, d.customerID,
				d.cardID, d.balance.Neg(), d.currency, posted.EntryID,
				scope.UserID); e != nil {
				return db.Translate(e, "That credit could not be written back.")
			}

			total = total.Add(d.balance)
			if d.cardID != nil {
				out.Cards++
			} else {
				out.Wallets++
			}
		}
		out.Total = total.StringFixed(2)

		if len(owed) == 0 {
			return nil
		}
		return audit.Write(ctx, tx, audit.Entry{
			TenantID: &scope.TenantID, ActorID: &scope.UserID,
			ActorLabel: audit.LabelFor(ctx, tx, scope.UserID),
			Action:     "store_credit_expired",
			EntityType: "company", EntityID: &scope.CompanyID,
			After: map[string]any{
				"cards": out.Cards, "wallets": out.Wallets, "total": out.Total,
			},
		})
	})
	return out, err
}

func (s *Service) card(
	ctx context.Context, tx pgx.Tx, scope Scope, id uuid.UUID, withHistory bool,
) (Card, error) {
	var c Card
	var face, balance decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT g.id, g.code, g.face_value,
		       coalesce((SELECT sum(amount) FROM store_credit_entry e
		                 WHERE e.gift_card_id = g.id), 0),
		       g.currency,
		       coalesce(to_char(g.expires_on, 'YYYY-MM-DD'), ''),
		       g.expires_on IS NOT NULL AND g.expires_on < current_date,
		       g.is_void, coalesce(g.void_reason, ''),
		       g.customer_id, coalesce(c.name, ''),
		       coalesce(g.note, ''), coalesce(u.full_name, ''),
		       to_char(g.issued_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM gift_card g
		LEFT JOIN customer c ON c.id = g.customer_id
		LEFT JOIN app_user u ON u.id = g.issued_by
		WHERE g.id = $1 AND g.company_id = $2`, id, scope.CompanyID).
		Scan(&c.ID, &c.Code, &face, &balance, &c.Currency, &c.ExpiresOn,
			&c.Expired, &c.Void, &c.VoidWhy, &c.CustomerID, &c.Customer,
			&c.Note, &c.IssuedBy, &c.IssuedAt)
	if err == pgx.ErrNoRows {
		return Card{}, errs.New(errs.CodeNotFound, "That gift card does not exist.")
	}
	if err != nil {
		return Card{}, err
	}
	c.FaceValue = face.StringFixed(2)
	c.Balance = balance.StringFixed(2)

	if withHistory {
		entries, e := s.entries(ctx, tx, `gift_card_id = $1`, id)
		if e != nil {
			return Card{}, e
		}
		c.Entries = entries
	}
	return c, nil
}

// entries reads a ledger, newest first. The predicate is a constant in this
// package, never anything a request supplied.
func (s *Service) entries(
	ctx context.Context, tx pgx.Tx, where string, arg uuid.UUID,
) ([]Entry, error) {
	rows, err := tx.Query(ctx, `
		SELECT e.id, e.amount, e.currency, e.reason,
		       e.customer_id, coalesce(c.name, ''),
		       e.gift_card_id, coalesce(g.code, ''),
		       e.invoice_id, coalesce(i.human_number, ''),
		       coalesce(to_char(e.expires_on, 'YYYY-MM-DD'), ''),
		       coalesce(e.note, ''), coalesce(u.full_name, ''),
		       to_char(e.created_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		FROM store_credit_entry e
		LEFT JOIN customer c ON c.id = e.customer_id
		LEFT JOIN gift_card g ON g.id = e.gift_card_id
		LEFT JOIN sales_invoice i ON i.id = e.invoice_id
		LEFT JOIN app_user u ON u.id = e.created_by
		WHERE e.`+where+`
		ORDER BY e.created_at DESC
		LIMIT 200`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Entry{}
	for rows.Next() {
		var e Entry
		var amount decimal.Decimal
		if err := rows.Scan(&e.ID, &amount, &e.Currency, &e.Reason,
			&e.CustomerID, &e.Customer, &e.CardID, &e.CardCode,
			&e.InvoiceID, &e.InvoiceNo, &e.ExpiresOn, &e.Note,
			&e.CreatedBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Amount = amount.StringFixed(2)
		out = append(out, e)
	}
	return out, rows.Err()
}
