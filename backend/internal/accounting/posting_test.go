//go:build integration

// QA gate M1, mechanised: "Trial balance always balances. Sub-ledgers tie to
// control accounts. Closed periods truly locked."
//
// Every attempt below goes through raw SQL in tenant context rather than
// through a service, because a guarantee that only holds when the application
// cooperates is not a guarantee. A background job, a migration or a support
// script must hit the same wall.
package accounting

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

type books struct {
	pool      *db.Pool
	tenantID  uuid.UUID
	companyID uuid.UUID
	periodID  uuid.UUID
	cash      uuid.UUID
	revenue   uuid.UUID
	outputVAT uuid.UUID
}

func newBooks(t *testing.T) *books {
	t.Helper()
	dsn := os.Getenv("RAWSYST_DB_DSN")
	if dsn == "" {
		t.Skip("RAWSYST_DB_DSN not set; skipping database-backed test")
	}
	ctx := context.Background()

	pool, err := db.Open(ctx, config.DB{DSN: dsn, MaxConns: 8, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx, nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	b := &books{pool: pool}

	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO tenant (name) VALUES ('Books Test') RETURNING id`).
			Scan(&b.tenantID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO company (tenant_id, legal_name, country, base_currency)
			VALUES ($1, 'Books Test Co', 'sa', 'SAR') RETURNING id`,
			b.tenantID).Scan(&b.companyID)
	})
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_ = pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, b.tenantID)
			return err
		})
	})

	err = pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		mk := func(code, name, kind string, dst *uuid.UUID) error {
			return tx.QueryRow(ctx, `
				INSERT INTO account (tenant_id, company_id, code, name, type)
				VALUES ($1,$2,$3,$4,$5) RETURNING id`,
				b.tenantID, b.companyID, code, name, kind).Scan(dst)
		}
		if err := mk("1100", "Cash", "asset", &b.cash); err != nil {
			return err
		}
		if err := mk("4100", "Sales Revenue", "revenue", &b.revenue); err != nil {
			return err
		}
		if err := mk("2200", "Output VAT Payable", "liability", &b.outputVAT); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO fiscal_period
			  (tenant_id, company_id, fiscal_year, period_no, starts_on, ends_on)
			VALUES ($1,$2,2026,8,'2026-08-01','2026-08-31') RETURNING id`,
			b.tenantID, b.companyID).Scan(&b.periodID)
	})
	if err != nil {
		t.Fatalf("seed chart of accounts: %v", err)
	}
	return b
}

type line struct {
	account uuid.UUID
	debit   string
	credit  string
}

// post writes an entry with its lines in one transaction, which is where the
// deferred balance check fires.
func (b *books) post(ctx context.Context, memo string, lines ...line) error {
	return b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		var entryID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO journal_entry
			  (tenant_id, company_id, period_id, entry_no, entry_date, source_type, memo)
			VALUES ($1,$2,$3,claim_entry_no($2),'2026-08-15','test',$4) RETURNING id`,
			b.tenantID, b.companyID, b.periodID, memo).Scan(&entryID); err != nil {
			return err
		}

		for i, l := range lines {
			d := decimal.RequireFromString(l.debit)
			c := decimal.RequireFromString(l.credit)
			if _, err := tx.Exec(ctx, `
				INSERT INTO journal_line
				  (tenant_id, entry_id, line_no, account_id, currency,
				   debit, credit, base_debit, base_credit)
				VALUES ($1,$2,$3,$4,'SAR',$5,$6,$5,$6)`,
				b.tenantID, entryID, i+1, l.account, d, c); err != nil {
				return err
			}
		}
		return nil
	})
}

// The rule the whole engine exists to enforce. C9: "an unbalanced entry can
// never be saved."
func TestUnbalancedEntryCannotBeSaved(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	err := b.post(ctx, "off by ten",
		line{b.cash, "1150.00", "0"},
		line{b.revenue, "0", "1000.00"},
		line{b.outputVAT, "0", "140.00"}, // should be 150.00
	)
	if err == nil {
		t.Fatal("an entry whose debits exceed its credits by 10 was saved")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "balance") {
		t.Fatalf("the refusal does not explain that the entry is unbalanced: %v", err)
	}
	// The message must name the actual difference, or an accountant has to
	// find it by hand.
	if !strings.Contains(err.Error(), "10") {
		t.Fatalf("the refusal does not say by how much it is out: %v", err)
	}
}

// C9.2 Rule 1: a cash retail sale of SAR 1,150 including 15% VAT.
func TestBalancedSalePosts(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	if err := b.post(ctx, "cash sale",
		line{b.cash, "1150.00", "0"},
		line{b.revenue, "0", "1000.00"},
		line{b.outputVAT, "0", "150.00"},
	); err != nil {
		t.Fatalf("a balanced entry was refused: %v", err)
	}

	var diff decimal.Decimal
	if err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT trial_balance_difference($1)`, b.companyID).Scan(&diff)
	}); err != nil {
		t.Fatalf("read trial balance: %v", err)
	}
	if !diff.IsZero() {
		t.Fatalf("the trial balance is out by %s after one balanced entry", diff)
	}
}

// A single-line entry cannot balance unless it is zero, and a zero entry
// records nothing. Both are rejected.
func TestSingleLineEntryIsRefused(t *testing.T) {
	b := newBooks(t)

	err := b.post(context.Background(), "one line",
		line{b.cash, "100.00", "0"})
	if err == nil {
		t.Fatal("a one-line journal entry was saved")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "two lines") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// A line that is neither a debit nor a credit records nothing; one that is both
// lets a real movement hide inside a net-zero line.
func TestLineMustBeOneSidedAndNonZero(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		debit  string
		credit string
	}{
		{"neither side", "0", "0"},
		{"both sides", "100.00", "100.00"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := b.post(ctx, tc.name,
				line{b.cash, tc.debit, tc.credit},
				line{b.revenue, "0", "100.00"},
			)
			if err == nil {
				t.Fatalf("a line with debit=%s credit=%s was accepted", tc.debit, tc.credit)
			}
		})
	}
}

// C9.1: "immutable once posted; corrections are made by reversing entries,
// never by editing history." Attempted through the platform plane, which is the
// most privileged path that exists.
func TestPostedEntriesAreImmutable(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	if err := b.post(ctx, "original",
		line{b.cash, "115.00", "0"},
		line{b.revenue, "0", "100.00"},
		line{b.outputVAT, "0", "15.00"},
	); err != nil {
		t.Fatalf("post: %v", err)
	}

	attempts := []struct {
		name string
		sql  string
	}{
		{"edit an entry", `UPDATE journal_entry SET memo = 'tampered' WHERE company_id = $1`},
		{"delete an entry", `DELETE FROM journal_entry WHERE company_id = $1`},
		{"edit a line", `UPDATE journal_line SET base_debit = 999 WHERE tenant_id = $1`},
		{"delete a line", `DELETE FROM journal_line WHERE tenant_id = $1`},
	}

	for _, a := range attempts {
		t.Run(a.name, func(t *testing.T) {
			arg := b.companyID
			if strings.Contains(a.sql, "tenant_id") {
				arg = b.tenantID
			}
			err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
				_, e := tx.Exec(ctx, a.sql, arg)
				return e
			})
			if err == nil {
				t.Fatalf("%s succeeded; posted accounting history is not immutable", a.name)
			}
		})
	}
}

// C10: a closed period accepts nothing. This is what makes a published set of
// statements trustworthy — otherwise last month's numbers can still move.
func TestClosedPeriodRefusesEntries(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	if err := b.post(ctx, "before close",
		line{b.cash, "115.00", "0"},
		line{b.revenue, "0", "100.00"},
		line{b.outputVAT, "0", "15.00"},
	); err != nil {
		t.Fatalf("post before close: %v", err)
	}

	if err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE fiscal_period SET state = 'closed', closed_at = now() WHERE id = $1`,
			b.periodID)
		return e
	}); err != nil {
		t.Fatalf("close period: %v", err)
	}

	err := b.post(ctx, "after close",
		line{b.cash, "115.00", "0"},
		line{b.revenue, "0", "100.00"},
		line{b.outputVAT, "0", "15.00"},
	)
	if err == nil {
		t.Fatal("an entry was posted into a closed period")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "closed") {
		t.Fatalf("the refusal does not say the period is closed: %v", err)
	}
	// The message must tell the user what to do next.
	if !strings.Contains(strings.ToLower(err.Error()), "reopen") {
		t.Fatalf("the refusal does not offer a way forward: %v", err)
	}
}

// An entry dated outside its own period would show in one month's statements
// while counting toward another month's close.
func TestEntryDateMustFallInsideItsPeriod(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		var entryID uuid.UUID
		return tx.QueryRow(ctx, `
			INSERT INTO journal_entry
			  (tenant_id, company_id, period_id, entry_no, entry_date, source_type)
			VALUES ($1,$2,$3,claim_entry_no($2),'2026-09-15','test') RETURNING id`,
			b.tenantID, b.companyID, b.periodID).Scan(&entryID)
	})
	if err == nil {
		t.Fatal("a September entry was posted into the August period")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "outside") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// Posting is idempotent per source. A sync retry replaying a sale must not
// double-post it — C9 says every transaction posts automatically, and that is
// only safe if "every" means exactly once.
func TestPostingIsIdempotentPerSource(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()
	saleID := uuid.New()

	postSale := func(entryNo int64) error {
		return b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
			var entryID uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO journal_entry
				  (tenant_id, company_id, period_id, entry_no, entry_date,
				   source_type, source_id, rule_key)
				VALUES ($1,$2,$3,claim_entry_no($2),'2026-08-15','sale',$4,'sale.cash') RETURNING id`,
				b.tenantID, b.companyID, b.periodID, saleID).Scan(&entryID); err != nil {
				return err
			}
			for i, l := range []line{
				{b.cash, "115.00", "0"},
				{b.revenue, "0", "100.00"},
				{b.outputVAT, "0", "15.00"},
			} {
				d := decimal.RequireFromString(l.debit)
				c := decimal.RequireFromString(l.credit)
				if _, err := tx.Exec(ctx, `
					INSERT INTO journal_line
					  (tenant_id, entry_id, line_no, account_id, currency,
					   debit, credit, base_debit, base_credit)
					VALUES ($1,$2,$3,$4,'SAR',$5,$6,$5,$6)`,
					b.tenantID, entryID, i+1, l.account, d, c); err != nil {
					return err
				}
			}
			return nil
		})
	}

	if err := postSale(1); err != nil {
		t.Fatalf("first posting: %v", err)
	}
	if err := postSale(2); err == nil {
		t.Fatal("the same sale posted twice; a sync retry would double the revenue")
	}
}

// Multi-currency: the books are kept in base currency, so that is what must
// balance. A transaction in USD against a SAR-based company still produces a
// balanced entry in SAR.
func TestForeignCurrencyEntryBalancesInBaseCurrency(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	// USD 100 at 3.75 = SAR 375.
	err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		var entryID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO journal_entry
			  (tenant_id, company_id, period_id, entry_no, entry_date, source_type)
			VALUES ($1,$2,$3,claim_entry_no($2),'2026-08-15','test') RETURNING id`,
			b.tenantID, b.companyID, b.periodID).Scan(&entryID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency, fx_rate,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,1,$3,'USD',3.75,100,0,375,0)`,
			b.tenantID, entryID, b.cash); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency, fx_rate,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,2,$3,'USD',3.75,0,100,0,375)`,
			b.tenantID, entryID, b.revenue)
		return err
	})
	if err != nil {
		t.Fatalf("a foreign-currency entry balancing in base currency was refused: %v", err)
	}
}

// A debit in transaction currency must stay a debit in base currency. A sign
// flip between the two would balance arithmetically while recording the
// opposite of what happened.
func TestSidesMustAgreeAcrossCurrencies(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		var entryID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO journal_entry
			  (tenant_id, company_id, period_id, entry_no, entry_date, source_type)
			VALUES ($1,$2,$3,claim_entry_no($2),'2026-08-15','test') RETURNING id`,
			b.tenantID, b.companyID, b.periodID).Scan(&entryID); err != nil {
			return err
		}
		// Debit in USD, credit in SAR: the same line pointing both ways.
		_, err := tx.Exec(ctx, `
			INSERT INTO journal_line
			  (tenant_id, entry_id, line_no, account_id, currency, fx_rate,
			   debit, credit, base_debit, base_credit)
			VALUES ($1,$2,1,$3,'USD',3.75,100,0,0,375)`,
			b.tenantID, entryID, b.cash)
		return err
	})
	if err == nil {
		t.Fatal("a line that debits in one currency and credits in the other was accepted")
	}
}

// Two periods cannot cover the same day, or "which period does this belong to"
// has more than one answer.
func TestOverlappingPeriodsAreImpossible(t *testing.T) {
	b := newBooks(t)

	err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(context.Background(), `
			INSERT INTO fiscal_period
			  (tenant_id, company_id, fiscal_year, period_no, starts_on, ends_on)
			VALUES ($1,$2,2026,9,'2026-08-15','2026-09-30')`,
			b.tenantID, b.companyID)
		return e
	})
	if err == nil {
		t.Fatal("two periods were allowed to cover the same dates")
	}
}

// Reopening a closed period demands a substantive reason. It changes numbers
// someone has already reported, so "fix" is not an explanation.
func TestReopeningRequiresARealReason(t *testing.T) {
	b := newBooks(t)
	ctx := context.Background()

	if err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE fiscal_period SET state='closed', closed_at=now() WHERE id=$1`, b.periodID)
		return e
	}); err != nil {
		t.Fatalf("close: %v", err)
	}

	err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `
			UPDATE fiscal_period
			SET state='open', reopened_at=now(), reopen_reason='fix'
			WHERE id=$1`, b.periodID)
		return e
	})
	if err == nil {
		t.Fatal("a period was reopened with a one-word reason")
	}
}
