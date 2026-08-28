package sales

// Finding the sale a customer is holding a receipt for.
//
// # The gap this closes
//
// Design 11 §7 opens with "the original invoice is always scanned or linked,
// never re-typed (B10)". The till obeys the second half and had nothing to
// obey the first half with.
//
// A sale rung up at a terminal is queued locally and pushed to
// POST /api/v1/sync/push. The terminal generates the document UUID, and that is
// the only identifier it ever knows: the push response reports applied or
// failed against that UUID and says nothing else. sales_invoice.id, meanwhile,
// is a fresh uuid.New() minted inside Finalize (finalize.go), and it is
// sales_invoice.id — not the UUID the till holds — that
// GET /api/v1/pos/sales/{invoiceID}/returnable looks up.
//
// So the returns screen, which asks the cashier to "scan the receipt or type
// the sale reference" and sends whatever it gets straight to that route, could
// not find a single sale the till had made. Typing the reference printed on the
// receipt (the first eight characters of the document UUID) failed as a
// malformed UUID; typing the whole document UUID came back "That invoice was
// not found."; and the invoice id that would have worked appears on no receipt,
// no screen and no response the till receives. Every return at every terminal.
//
// Found by driving the packaged application — see e2e/tauri.mjs.
//
// # What this resolves
//
// Everything a receipt can actually carry, because a cashier holding one has no
// way to know which kind of identifier they are looking at:
//
//   - the document UUID the till generated and prints;
//   - a prefix of it, which is what the receipt prints in full — eight
//     characters, and refused when it is not unique rather than guessed at;
//   - the human number, which is what the invoice is called once the server has
//     numbered it and what a customer reads out over the telephone;
//   - the invoice id, so callers that already hold one keep working.
//
// # Scope
//
// A device's lookup is confined to the device's own company. Row-level security
// stops at the tenant, and a tenant with two companies would otherwise let one
// shop's till pull up the other shop's invoice and refund against it. For a
// person the tenant is the boundary, which is what every other read here does.

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// InvoiceMatch is enough to confirm the right sale and then go and get it.
//
// Deliberately not the whole invoice: this answers "which sale is this?", and
// the caller that wants the lines asks for the lines. Money is a string at the
// boundary, as everywhere else.
type InvoiceMatch struct {
	ID          uuid.UUID `json:"id"`
	UUID        uuid.UUID `json:"uuid"`
	DocType     string    `json:"doc_type"`
	HumanNumber *string   `json:"human_number"`
	State       string    `json:"state"`

	IssueDate      string `json:"issue_date"`
	Currency       string `json:"currency"`
	TotalInclusive string `json:"total_inclusive"`
}

// The shortest prefix that may be matched.
//
// The receipt prints eight characters, so eight has to work. Shorter than that
// is not a reference somebody read off a receipt; it is a fragment, and
// matching one would eventually pull up an invoice nobody asked for.
const minReferencePrefix = 8

// Lookup finds the invoice a reference names.
//
// companyID confines the search; pass uuid.Nil to search the whole tenant.
func (s *Service) Lookup(
	ctx context.Context, tenantID, companyID uuid.UUID, reference string,
) (InvoiceMatch, error) {
	if s.pool == nil {
		return InvoiceMatch{}, errs.New(errs.CodeInternal,
			"The sales service was built without a database connection.")
	}

	ref := strings.TrimSpace(reference)
	if ref == "" {
		return InvoiceMatch{}, errs.New(errs.CodeInvalidInput,
			"Scan the receipt or type the sale reference from it.")
	}

	var out InvoiceMatch
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// An exact identifier first, and only then the prefix. A reference that
		// is a whole UUID must never be answered by a prefix search: the prefix
		// branch can find two invoices and refuse, and refusing an exact match
		// because something else shares its first eight characters would be
		// absurd.
		if id, e := uuid.Parse(ref); e == nil {
			m, found, e2 := s.matchOne(ctx, tx, companyID,
				`si.id = $1 OR si.uuid = $1`, id)
			if e2 != nil {
				return e2
			}
			if found {
				out = m
				return nil
			}
			return notFound()
		}

		// A human number, which is what the server calls the invoice and what a
		// customer reads out. Case-folded because nobody types the case of a
		// series prefix reliably.
		m, found, e := s.matchOne(ctx, tx, companyID,
			`upper(si.human_number) = upper($1)`, ref)
		if e != nil {
			return e
		}
		if found {
			out = m
			return nil
		}

		// Last, the prefix the receipt prints.
		if len(ref) < minReferencePrefix {
			return errs.Newf(errs.CodeInvalidInput,
				"A sale reference is at least %d characters. Scan the receipt, "+
					"or type the reference on it in full.", minReferencePrefix)
		}
		if !hexish(ref) {
			return notFound()
		}

		matches, e := s.matchPrefix(ctx, tx, companyID, strings.ToLower(ref))
		if e != nil {
			return e
		}
		switch len(matches) {
		case 0:
			return notFound()
		case 1:
			out = matches[0]
			return nil
		default:
			// Two sales sharing a prefix is rare and picking one is never
			// right: the wrong one is a refund against a stranger's invoice.
			return errs.New(errs.CodeInvalidInput,
				"More than one sale starts with that reference. Type more of "+
					"it, or use the invoice number.")
		}
	})
	if err != nil {
		return InvoiceMatch{}, err
	}
	return out, nil
}

func notFound() error {
	// Another tenant's invoice reads as absent under row-level security, and
	// another company's is excluded by the predicate. Both are the same answer
	// on purpose: whether a record exists elsewhere is not this caller's
	// business (`07-api-conventions.md` §3).
	return errs.New(errs.CodeNotFound,
		"No sale was found for that reference. Check the receipt.")
}

const matchSelect = `
	SELECT si.id, si.uuid, si.doc_type, si.human_number, si.state,
	       to_char(si.issue_date, 'YYYY-MM-DD'), si.currency,
	       si.total_inclusive::text
	FROM sales_invoice si`

func (s *Service) matchOne(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	where string, arg any,
) (InvoiceMatch, bool, error) {
	var m InvoiceMatch
	err := tx.QueryRow(ctx,
		matchSelect+` WHERE (`+where+`) AND ($2::uuid IS NULL OR si.company_id = $2)`,
		arg, nullUUID(companyID)).
		Scan(&m.ID, &m.UUID, &m.DocType, &m.HumanNumber, &m.State,
			&m.IssueDate, &m.Currency, &m.TotalInclusive)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvoiceMatch{}, false, nil
	}
	if err != nil {
		return InvoiceMatch{}, false, err
	}
	return m, true, nil
}

func (s *Service) matchPrefix(
	ctx context.Context, tx pgx.Tx, companyID uuid.UUID, prefix string,
) ([]InvoiceMatch, error) {
	// Two is enough to answer the only question asked of this: whether the
	// prefix is unique.
	rows, err := tx.Query(ctx,
		matchSelect+`
		WHERE si.uuid::text LIKE $1 || '%'
		  AND ($2::uuid IS NULL OR si.company_id = $2)
		LIMIT 2`, prefix, nullUUID(companyID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []InvoiceMatch
	for rows.Next() {
		var m InvoiceMatch
		if e := rows.Scan(&m.ID, &m.UUID, &m.DocType, &m.HumanNumber, &m.State,
			&m.IssueDate, &m.Currency, &m.TotalInclusive); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// hexish reports whether a reference could be the start of a UUID.
//
// Checked before it reaches a LIKE, so a cashier's typo goes to "no sale was
// found" rather than to a table scan for a pattern that cannot match.
func hexish(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9',
			r >= 'a' && r <= 'f',
			r >= 'A' && r <= 'F',
			r == '-':
		default:
			return false
		}
	}
	return true
}

func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
