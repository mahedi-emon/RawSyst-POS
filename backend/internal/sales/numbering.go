package sales

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
)

// claimHumanNumber allocates the friendly invoice number for a store.
//
// Separate from the ICV by design. Blueprint I3 warns that letting a custom
// invoice number drive the tamper-evident counter is the mistake to avoid, so
// this series resets each January — which most shops expect, and which is
// exactly what makes it useless as a tamper signal and the ICV necessary.
func claimHumanNumber(
	ctx context.Context, tx pgx.Tx, storeID uuid.UUID, issuedAt time.Time, docType string,
) (string, error) {
	series := "INV"
	switch docType {
	case "credit_note":
		series = "CRN"
	case "debit_note":
		series = "DBN"
	}

	// The tenant comes from the store row rather than being passed in. Row-level
	// security has already scoped this query to the caller's tenant, so reading
	// it here cannot widen access — and taking it as an argument would create a
	// way for the two to disagree.
	var number string
	err := tx.QueryRow(ctx, `
		SELECT format_invoice_number(
		         s.code, $2::text, $3::integer,
		         claim_invoice_number(s.tenant_id, s.id, $2::text, $3::integer))
		FROM store s WHERE s.id = $1`,
		storeID, series, issuedAt.Year()).Scan(&number)
	if err != nil {
		return "", db.Translate(err, "An invoice number could not be issued.")
	}
	return number, nil
}
