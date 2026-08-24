//go:build integration

// QA gate M2, mechanised: "Hash chain unbroken across 10,000+ sequential test
// invoices. ICV never resets or gaps."
package zatca

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/config"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// testHasher stands in for the real canonicalising hasher while the byte-level
// XML and QR formats remain unverified.
//
// It hashes ICV, PIH and the document bytes together, which gives the chain the
// property under test: any change to a document changes its hash, and therefore
// breaks every link after it. What it deliberately does NOT do is claim to
// produce ZATCA-valid bytes — that is the seam the release gate protects.
type testHasher struct{ version string }

func (h testHasher) SchemaVersion() string { return h.version }

func (h testHasher) Hash(_ context.Context, d Document) (string, error) {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d|%s|%s", d.ICV, d.PIH, d.XML))
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

type fixture struct {
	pool      *db.Pool
	chain     *Chain
	tenantID  uuid.UUID
	companyID uuid.UUID
	unitID    uuid.UUID
}

func newFixture(t *testing.T) *fixture {
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

	f := &fixture{pool: pool, chain: NewChain(pool, testHasher{version: "1.2"})}

	err = pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO tenant (name) VALUES ('Chain Test') RETURNING id`).
			Scan(&f.tenantID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO company (tenant_id, legal_name, country, base_currency)
			VALUES ($1, 'Chain Test Co', 'sa', 'SAR') RETURNING id`,
			f.tenantID).Scan(&f.companyID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO egs_unit (tenant_id, company_id, label, architecture)
			VALUES ($1, $2, 'central', 'centralized_server') RETURNING id`,
			f.tenantID, f.companyID).Scan(&f.unitID)
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Cleanup(func() {
		_ = pool.TxAsPlatform(context.Background(), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(),
				`DELETE FROM tenant WHERE id = $1`, f.tenantID)
			return err
		})
	})
	return f
}

// issue creates an invoice and puts it on the chain, returning its link.
func (f *fixture) issue(t *testing.T, n int) Link {
	t.Helper()
	ctx := context.Background()
	var link Link

	err := f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		invoiceUUID := uuid.New()
		var invoiceID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO sales_invoice
			  (tenant_id, company_id, uuid, doc_type, issue_date, issued_at,
			   currency, total_inclusive, state)
			VALUES ($1,$2,$3,'simplified',current_date,now(),'SAR',115,'signed_pending_report')
			RETURNING id`, f.tenantID, f.companyID, invoiceUUID).Scan(&invoiceID); err != nil {
			return err
		}

		// Reserve, then hash a document carrying that position. The two are
		// separate because the document contains its own ICV and PIH.
		icv, pih, err := f.chain.Reserve(ctx, tx, f.unitID)
		if err != nil {
			return err
		}
		link, err = f.chain.LinkFor(ctx, f.unitID, icv, pih,
			fmt.Appendf(nil, "<Invoice seq=%q></Invoice>", fmt.Sprint(n)))
		if err != nil {
			return err
		}
		return f.chain.Record(ctx, tx, invoiceID, f.tenantID, link)
	})
	if err != nil {
		t.Fatalf("issue invoice %d: %v", n, err)
	}
	return link
}

// QA gate M2. Ten thousand sequential invoices, no reset, no gap, every link
// matching the hash of the one before it.
func TestChainHoldsAcrossTenThousandInvoices(t *testing.T) {
	if testing.Short() {
		t.Skip("long-running acceptance test")
	}
	f := newFixture(t)

	const count = 10_000
	prevHash := GenesisPIH

	for i := 1; i <= count; i++ {
		link := f.issue(t, i)

		if link.ICV != int64(i) {
			t.Fatalf("invoice %d received counter %d; the counter must be strictly "+
				"sequential from 1", i, link.ICV)
		}
		if link.PIH != prevHash {
			t.Fatalf("invoice %d carries the wrong previous hash: the chain is broken "+
				"at this point", i)
		}
		prevHash = link.InvoiceHash
	}

	breaks, err := f.chain.Verify(context.Background(), f.tenantID, f.unitID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(breaks) != 0 {
		t.Fatalf("after %d invoices: %s", count, Describe(breaks))
	}

	// And the counter never went backwards.
	var last int64
	if err := f.pool.TxAsTenant(context.Background(), f.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT last_icv FROM egs_unit WHERE id = $1`, f.unitID).Scan(&last)
	}); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if last != count {
		t.Fatalf("counter ended at %d after %d invoices", last, count)
	}
}

// The same ICV cannot appear twice on a unit's chain. Enforced by a unique
// index, so it holds against direct SQL rather than only against the service.
func TestDuplicateCounterIsImpossible(t *testing.T) {
	f := newFixture(t)
	first := f.issue(t, 1)
	ctx := context.Background()

	err := f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var invoiceID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO sales_invoice
			  (tenant_id, company_id, uuid, doc_type, issue_date, issued_at,
			   currency, total_inclusive, state)
			VALUES ($1,$2,$3,'simplified',current_date,now(),'SAR',115,'signed_pending_report')
			RETURNING id`, f.tenantID, f.companyID, uuid.New()).Scan(&invoiceID); err != nil {
			return err
		}
		return f.chain.Record(ctx, tx, invoiceID, f.tenantID, Link{
			EGSUnitID: f.unitID, ICV: first.ICV,
			PIH: first.PIH, InvoiceHash: "a-different-hash", SchemaVersion: "1.2",
		})
	})
	if err == nil {
		t.Fatal("two invoices were recorded at the same counter value")
	}
}

// A signed invoice's chain fields are frozen. Submission status may advance —
// that is what the retry queue does — but nothing that defines its position may
// move, even through the most privileged path available.
func TestChainFieldsAreImmutable(t *testing.T) {
	f := newFixture(t)
	f.issue(t, 1)
	ctx := context.Background()

	frozen := []struct {
		column string
		value  any
	}{
		{"icv", int64(999)},
		{"pih", "tampered"},
		{"invoice_hash", "tampered"},
		{"schema_version", "0.1"},
	}

	for _, tc := range frozen {
		err := f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx,
				fmt.Sprintf(`UPDATE zatca_invoice SET %s = $1 WHERE egs_unit_id = $2`, tc.column),
				tc.value, f.unitID)
			return e
		})
		if err == nil {
			t.Errorf("%s was modified on a signed invoice; the chain is not immutable",
				tc.column)
		}
	}

	// Submission status must still be updatable, or a rejected invoice could
	// never be retried.
	err := f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`UPDATE zatca_invoice SET retry_count = retry_count + 1, response_code = 400
			 WHERE egs_unit_id = $1`, f.unitID)
		return e
	})
	if err != nil {
		t.Fatalf("submission status should remain updatable: %v", err)
	}
}

// A signed invoice can never be deleted, whatever the reason. Deleting an
// issued e-invoice carries a fine starting at SAR 10,000.
func TestSignedInvoiceCannotBeDeleted(t *testing.T) {
	f := newFixture(t)
	f.issue(t, 1)
	ctx := context.Background()

	err := f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM zatca_invoice WHERE egs_unit_id = $1`, f.unitID)
		return e
	})
	if err == nil {
		t.Fatal("a chain record was deleted")
	}

	err = f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `DELETE FROM sales_invoice WHERE company_id = $1`, f.companyID)
		return e
	})
	if err == nil {
		t.Fatal("an issued invoice was deleted")
	}
}

// A terminal signs offline and reports its counter on sync. Replaying that
// batch must not renumber or duplicate anything.
func TestTerminalReplayIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	record := func(icv int64, pih, hash string) error {
		return f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			var invoiceID uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO sales_invoice
				  (tenant_id, company_id, uuid, doc_type, issue_date, issued_at,
				   currency, total_inclusive, state)
				VALUES ($1,$2,$3,'simplified',current_date,now(),'SAR',115,'signed_pending_report')
				RETURNING id`, f.tenantID, f.companyID, uuid.New()).Scan(&invoiceID); err != nil {
				return err
			}
			return f.chain.RecordTerminalSigned(ctx, tx, invoiceID, f.tenantID, Link{
				EGSUnitID: f.unitID, ICV: icv, PIH: pih,
				InvoiceHash: hash, SchemaVersion: "1.2",
			})
		})
	}

	if err := record(1, GenesisPIH, "hash-one"); err != nil {
		t.Fatalf("first terminal invoice: %v", err)
	}
	if err := record(2, "hash-one", "hash-two"); err != nil {
		t.Fatalf("second terminal invoice: %v", err)
	}

	// The same batch arriving twice.
	err := record(2, "hash-one", "hash-two")
	if err == nil {
		t.Fatal("a replayed terminal invoice was accepted")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "already been used") {
		t.Fatalf("the refusal does not explain the replay: %v", err)
	}
	if errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("code = %q, want %q", errs.CodeOf(err), errs.CodeConflict)
	}
}

// A gap means invoices are missing, which is the exact signal ZATCA's tamper
// detection looks for. It must be reported clearly, naming how many are absent.
func TestTerminalGapIsReportedWithTheCount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	err := f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var invoiceID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO sales_invoice
			  (tenant_id, company_id, uuid, doc_type, issue_date, issued_at,
			   currency, total_inclusive, state)
			VALUES ($1,$2,$3,'simplified',current_date,now(),'SAR',115,'signed_pending_report')
			RETURNING id`, f.tenantID, f.companyID, uuid.New()).Scan(&invoiceID); err != nil {
			return err
		}
		// Jumping straight to 5 leaves 1 through 4 missing.
		return f.chain.RecordTerminalSigned(ctx, tx, invoiceID, f.tenantID, Link{
			EGSUnitID: f.unitID, ICV: 5, PIH: GenesisPIH,
			InvoiceHash: "hash-five", SchemaVersion: "1.2",
		})
	})
	if err == nil {
		t.Fatal("an invoice leaving a four-invoice gap was accepted")
	}
	if !strings.Contains(err.Error(), "4 missing") {
		t.Fatalf("the refusal does not say how many invoices are missing: %v", err)
	}
}

// A terminal whose predecessor hash does not match must be refused: that is a
// forked chain, not a late arrival.
func TestTerminalBrokenLinkageIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	record := func(icv int64, pih, hash string) error {
		return f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
			var invoiceID uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO sales_invoice
				  (tenant_id, company_id, uuid, doc_type, issue_date, issued_at,
				   currency, total_inclusive, state)
				VALUES ($1,$2,$3,'simplified',current_date,now(),'SAR',115,'signed_pending_report')
				RETURNING id`, f.tenantID, f.companyID, uuid.New()).Scan(&invoiceID); err != nil {
				return err
			}
			return f.chain.RecordTerminalSigned(ctx, tx, invoiceID, f.tenantID, Link{
				EGSUnitID: f.unitID, ICV: icv, PIH: pih,
				InvoiceHash: hash, SchemaVersion: "1.2",
			})
		})
	}

	if err := record(1, GenesisPIH, "hash-one"); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := record(2, "not-the-previous-hash", "hash-two")
	if err == nil {
		t.Fatal("an invoice that does not follow its predecessor was accepted")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "does not follow") {
		t.Fatalf("unhelpful refusal: %v", err)
	}
}

// The chain verifier must actually detect a break, or the nightly job is
// theatre. A gap is forced in directly, bypassing the service.
func TestVerifierDetectsAnInjectedGap(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 3; i++ {
		f.issue(t, i)
	}
	ctx := context.Background()

	if breaks, err := f.chain.Verify(ctx, f.tenantID, f.unitID); err != nil {
		t.Fatalf("Verify: %v", err)
	} else if len(breaks) != 0 {
		t.Fatalf("a clean chain reported breaks: %s", Describe(breaks))
	}

	// Force a gap by pushing the counter forward, then recording past it.
	err := f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx,
			`UPDATE egs_unit SET last_icv = 9 WHERE id = $1`, f.unitID); e != nil {
			return e
		}
		var invoiceID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO sales_invoice
			  (tenant_id, company_id, uuid, doc_type, issue_date, issued_at,
			   currency, total_inclusive, state)
			VALUES ($1,$2,$3,'simplified',current_date,now(),'SAR',115,'signed_pending_report')
			RETURNING id`, f.tenantID, f.companyID, uuid.New()).Scan(&invoiceID); e != nil {
			return e
		}
		return f.chain.Record(ctx, tx, invoiceID, f.tenantID, Link{
			EGSUnitID: f.unitID, ICV: 10, PIH: "whatever",
			InvoiceHash: "hash-ten", SchemaVersion: "1.2",
		})
	})
	if err != nil {
		t.Fatalf("inject gap: %v", err)
	}

	breaks, err := f.chain.Verify(ctx, f.tenantID, f.unitID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(breaks) == 0 {
		t.Fatal("the verifier did not detect an injected gap; the nightly " +
			"integrity check would report a corrupted chain as healthy")
	}
	if !strings.Contains(Describe(breaks), "gap") {
		t.Fatalf("the break was found but not described as a gap: %s", Describe(breaks))
	}
}

// Two chains on different units are independent: they both start at 1 and
// neither is renumbered by the other. Blueprint E1.3 RULE 5.
func TestChainsAreIsolatedPerUnit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	var secondUnit uuid.UUID
	err := f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var storeID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO store (tenant_id, company_id, code, name)
			VALUES ($1,$2,'OLAYA','Olaya') RETURNING id`,
			f.tenantID, f.companyID).Scan(&storeID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			INSERT INTO egs_unit (tenant_id, company_id, store_id, label, architecture)
			VALUES ($1,$2,$3,'till-1','smart_pos') RETURNING id`,
			f.tenantID, f.companyID, storeID).Scan(&secondUnit)
	})
	if err != nil {
		t.Fatalf("seed second unit: %v", err)
	}

	f.issue(t, 1)
	f.issue(t, 2)

	// The new unit starts its own chain at 1, not at 3.
	var link Link
	err = f.pool.TxAsTenant(ctx, f.tenantID, func(tx pgx.Tx) error {
		var invoiceID uuid.UUID
		if e := tx.QueryRow(ctx, `
			INSERT INTO sales_invoice
			  (tenant_id, company_id, uuid, doc_type, issue_date, issued_at,
			   currency, total_inclusive, state)
			VALUES ($1,$2,$3,'simplified',current_date,now(),'SAR',115,'signed_pending_report')
			RETURNING id`, f.tenantID, f.companyID, uuid.New()).Scan(&invoiceID); e != nil {
			return e
		}
		icv, pih, e := f.chain.Reserve(ctx, tx, secondUnit)
		if e != nil {
			return e
		}
		link, e = f.chain.LinkFor(ctx, secondUnit, icv, pih,
			[]byte("<Invoice unit=\"second\"></Invoice>"))
		if e != nil {
			return e
		}
		return f.chain.Record(ctx, tx, invoiceID, f.tenantID, link)
	})
	if err != nil {
		t.Fatalf("issue on second unit: %v", err)
	}

	if link.ICV != 1 {
		t.Fatalf("a new unit started at counter %d; each chain starts at 1", link.ICV)
	}
	if link.PIH != GenesisPIH {
		t.Fatal("a new unit's first invoice did not carry the genesis hash")
	}
}

// The genesis hash must be a real seed, not an empty string: "first invoice"
// and "predecessor not recorded" have to be distinguishable.
func TestGenesisHashIsASeedNotEmpty(t *testing.T) {
	if GenesisPIH == "" {
		t.Fatal("the genesis previous-hash is empty, so a first invoice cannot be " +
			"told apart from one whose predecessor was never recorded")
	}
	want := sha256.Sum256([]byte("0"))
	if GenesisPIH != base64.StdEncoding.EncodeToString(want[:]) {
		t.Fatalf("genesis hash = %q, want the base64 SHA-256 of \"0\"", GenesisPIH)
	}
}
