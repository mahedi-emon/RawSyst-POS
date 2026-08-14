// Package zatca implements the invoice chain: the counter, the hash linkage
// and the state machine around them.
//
// # What is verified and what is not
//
// The chain's STRUCTURE is confirmed against ZATCA primary sources: SHA-256
// hashing, a strictly sequential non-resetting counter per EGS unit, each
// invoice carrying the previous one's hash, one sequence per unit, and a
// rejected invoice keeping its position rather than freeing its counter.
//
// The BYTES that get hashed are not. Exact XML canonicalisation and the TLV
// layout of the QR payload come from the XML Implementation Standard and the
// Security Features Standard, and SA.ZATCA.QR_TLV_FIELDS is still an unverified
// release blocker. So this package takes a DocumentHasher rather than building
// the bytes itself: everything confirmed is implemented and tested, and the one
// unconfirmed piece is a named seam that cannot be filled in by guesswork.
package zatca

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// GenesisPIH is the previous-invoice-hash carried by the first invoice on a
// chain: the base64 SHA-256 of the single character "0".
//
// A seed rather than an empty value, so "this is the first invoice" and "the
// predecessor was not recorded" are different states. An empty PIH could mean
// either, and the difference is exactly what a tamper check must be able to
// tell apart.
var GenesisPIH = func() string {
	sum := sha256.Sum256([]byte("0"))
	return base64.StdEncoding.EncodeToString(sum[:])
}()

// DocumentHasher produces the hash that becomes an invoice's identity on the
// chain.
//
// The implementation must canonicalise the invoice XML exactly as ZATCA's XML
// Implementation Standard specifies before hashing. Getting those bytes wrong
// produces a chain that is internally consistent and rejected at scale, which
// is the worst failure mode available here: everything looks correct until
// thousands of invoices are refused.
type DocumentHasher interface {
	// Hash returns the base64 SHA-256 of the canonicalised document.
	Hash(ctx context.Context, doc Document) (string, error)

	// SchemaVersion identifies the standard this hasher implements, recorded on
	// every invoice so an archived document stays verifiable against the rules
	// that produced it — up to fifteen years later.
	SchemaVersion() string
}

// Document is the invoice content presented for hashing.
type Document struct {
	InvoiceUUID uuid.UUID
	ICV         int64
	PIH         string
	XML         []byte
}

// Link is one invoice's position on a chain.
type Link struct {
	EGSUnitID     uuid.UUID
	ICV           int64
	PIH           string
	InvoiceHash   string
	SchemaVersion string
}

// Chain allocates positions on an EGS unit's invoice chain.
type Chain struct {
	pool   *db.Pool
	hasher DocumentHasher
}

func NewChain(pool *db.Pool, hasher DocumentHasher) *Chain {
	return &Chain{pool: pool, hasher: hasher}
}

// Allocate reserves the next position on a unit's chain and returns the link.
//
// The read and the increment happen in one statement, so two concurrent sales
// on the same unit cannot receive the same ICV. The row lock serialises them;
// the loser waits rather than duplicating, which is the correct trade when the
// alternative is a broken chain.
//
// This is the server-side allocator, used where the EGS unit is a centralized
// or branch server. A smart POS owns its counter locally and reports what it
// used — see Record.
func (c *Chain) Allocate(
	ctx context.Context, tx pgx.Tx, egsUnitID uuid.UUID, doc Document,
) (Link, error) {
	var icv int64
	var prevHash *string

	err := tx.QueryRow(ctx, `
		UPDATE egs_unit
		SET last_icv = last_icv + 1
		WHERE id = $1
		RETURNING last_icv, last_invoice_hash`, egsUnitID).Scan(&icv, &prevHash)
	if err != nil {
		return Link{}, db.Translate(err,
			"That e-invoicing unit does not exist, so no invoice number could be issued.")
	}

	pih := GenesisPIH
	if prevHash != nil && *prevHash != "" {
		pih = *prevHash
	}

	doc.ICV = icv
	doc.PIH = pih

	hash, err := c.hasher.Hash(ctx, doc)
	if err != nil {
		return Link{}, err
	}

	return Link{
		EGSUnitID:     egsUnitID,
		ICV:           icv,
		PIH:           pih,
		InvoiceHash:   hash,
		SchemaVersion: c.hasher.SchemaVersion(),
	}, nil
}

// Record writes a link and advances the unit's high-water mark.
//
// Both the server-allocated and the terminal-allocated paths end here. A
// terminal signs offline and reports its ICV on sync; the checks below are what
// make accepting that safe.
func (c *Chain) Record(
	ctx context.Context, tx pgx.Tx, invoiceID, tenantID uuid.UUID, link Link,
) error {
	if err := validateLink(link); err != nil {
		return err
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO zatca_invoice
		  (invoice_id, tenant_id, egs_unit_id, icv, pih, invoice_hash, schema_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		invoiceID, tenantID, link.EGSUnitID, link.ICV,
		link.PIH, link.InvoiceHash, link.SchemaVersion)
	if err != nil {
		return db.Translate(err,
			"That invoice could not be recorded on the e-invoicing chain.")
	}

	// The high-water mark only ever moves forward. A terminal reporting an
	// older ICV — a replayed sync batch, or a device restored from a backup —
	// must not drag the counter back, because the next allocation would then
	// reuse a number and produce a duplicate ZATCA rejects.
	_, err = tx.Exec(ctx, `
		UPDATE egs_unit
		SET last_icv = GREATEST(last_icv, $2),
		    last_invoice_hash = CASE WHEN $2 >= last_icv THEN $3 ELSE last_invoice_hash END
		WHERE id = $1`, link.EGSUnitID, link.ICV, link.InvoiceHash)
	if err != nil {
		return db.Translate(err, "")
	}
	return nil
}

// RecordTerminalSigned accepts a link a POS terminal produced offline.
//
// The terminal is authoritative for its own chain — it signed the invoice and
// gave the customer a legally deliverable receipt, so the server cannot
// renumber it. What the server can do is refuse a link that would corrupt the
// chain, and say precisely which invariant it breaks.
func (c *Chain) RecordTerminalSigned(
	ctx context.Context, tx pgx.Tx, invoiceID, tenantID uuid.UUID, link Link,
) error {
	if err := validateLink(link); err != nil {
		return err
	}

	var expectedPIH *string
	var lastICV int64
	err := tx.QueryRow(ctx, `
		SELECT last_icv, last_invoice_hash FROM egs_unit WHERE id = $1 FOR UPDATE`,
		link.EGSUnitID).Scan(&lastICV, &expectedPIH)
	if err != nil {
		return db.Translate(err, "That e-invoicing unit does not exist.")
	}

	// An ICV at or below the high-water mark is a replay. The unique index
	// would catch an exact duplicate, but this catches the case earlier and
	// with a message that says what happened.
	if link.ICV <= lastICV {
		return errs.Newf(errs.CodeConflict,
			"Invoice counter %d has already been used on this terminal (the last "+
				"recorded was %d). This usually means a sync batch was sent twice.",
			link.ICV, lastICV)
	}

	// A gap is the signal ZATCA's tamper detection looks for, so it is surfaced
	// rather than silently accepted. It is not rejected outright: the invoices
	// in between may simply still be queued on the device, and refusing this
	// one would block the whole batch. The exception is raised for a human.
	if link.ICV != lastICV+1 {
		return errs.Newf(errs.CodeConflict,
			"Invoice counter jumps from %d to %d, leaving %d missing. Those "+
				"invoices must be synced before this one, or the chain will show a gap.",
			lastICV, link.ICV, link.ICV-lastICV-1)
	}

	want := GenesisPIH
	if expectedPIH != nil && *expectedPIH != "" {
		want = *expectedPIH
	}
	if link.PIH != want {
		return errs.New(errs.CodeConflict,
			"This invoice does not follow the previous one on the chain. Its "+
				"recorded previous-invoice hash does not match the last invoice "+
				"this terminal signed.")
	}

	return c.Record(ctx, tx, invoiceID, tenantID, link)
}

func validateLink(l Link) error {
	switch {
	case l.EGSUnitID == uuid.Nil:
		return errs.New(errs.CodeInternal, "An invoice must belong to an e-invoicing unit.")
	case l.ICV <= 0:
		// ICV starts at 1. Zero would mean the counter was reset, which the
		// guideline forbids outright.
		return errs.New(errs.CodeInternal, "The invoice counter must start at 1.")
	case l.PIH == "":
		return errs.New(errs.CodeInternal,
			"An invoice must record the previous invoice's hash, or the chain is broken.")
	case l.InvoiceHash == "":
		return errs.New(errs.CodeInternal, "An invoice must record its own hash.")
	case l.SchemaVersion == "":
		return errs.New(errs.CodeInternal,
			"An invoice must record which schema version signed it, or it cannot be "+
				"verified once the standard changes.")
	}
	return nil
}

// Break is one defect found while walking a chain.
type Break struct {
	ICV     int64  `json:"icv"`
	Problem string `json:"problem"`
}

// Verify walks a unit's chain and reports every break.
//
// Run nightly and by the acceptance test. QA gate M2 requires an unbroken chain
// across 10,000+ sequential invoices with the counter never resetting and never
// gapping, and this is the check that answers it.
func (c *Chain) Verify(ctx context.Context, tenantID, egsUnitID uuid.UUID) ([]Break, error) {
	var breaks []Break

	// Tenant context, not the raw pool. zatca_invoice is tenant-scoped with no
	// platform predicate, so an unscoped connection sees no rows at all — and a
	// verifier that can see nothing reports every chain as intact. That failure
	// is worse than having no check, because it is silent and reassuring.
	err := c.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT icv, problem FROM zatca_chain_breaks($1)`, egsUnitID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var b Break
			if err := rows.Scan(&b.ICV, &b.Problem); err != nil {
				return err
			}
			breaks = append(breaks, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return breaks, nil
}

// InvoiceCount reports how many invoices sit on a unit's chain.
//
// An empty chain and an unreadable one both produce zero breaks, and only one
// of those is good news. The nightly job pairs Verify with this so it can tell
// "nothing wrong" apart from "nothing seen".
func (c *Chain) InvoiceCount(
	ctx context.Context, tenantID, egsUnitID uuid.UUID,
) (int, error) {
	var n int
	err := c.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM zatca_invoice WHERE egs_unit_id = $1`,
			egsUnitID).Scan(&n)
	})
	if err != nil {
		return 0, db.Translate(err, "")
	}
	return n, nil
}

// Describe renders breaks for an alert or a support screen.
func Describe(breaks []Break) string {
	if len(breaks) == 0 {
		return "chain intact"
	}
	first := breaks[0]
	if len(breaks) == 1 {
		return fmt.Sprintf("chain break at invoice counter %d: %s", first.ICV, first.Problem)
	}
	return fmt.Sprintf("%d chain breaks, first at invoice counter %d: %s",
		len(breaks), first.ICV, first.Problem)
}
