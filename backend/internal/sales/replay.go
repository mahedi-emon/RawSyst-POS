package sales

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/sync"
)

// Replaying an offline sale.
//
// # It goes through the same door as an online one
//
// This file decodes a payload, checks who sent it, and calls FinalizeInTx.
// It contains no invoice numbering, no chain allocation, no stock movement and
// no journal posting, and it must never grow any: the offline path handles the
// sales nobody watched happen, so it is the LAST place a second implementation
// should live. Every guarantee proved for an online sale holds here because it
// is the same code.
//
// # Signing stays on the terminal
//
// Nothing here signs anything. A replayed sale takes its position on the chain
// through the same allocator an online sale uses, and the cryptographic stamp
// was produced on the terminal before the sale ever reached a network. The
// server never holds the key and never will (E1.3, H1).
//
// # What a replay is allowed to trust
//
// Almost nothing. A payload arriving hours late from a device that may have
// been offline, tampered with, or simply running an old build is the least
// trustworthy input the system takes. So the tenant comes from the session, the
// company, store, EGS unit and warehouse come from the device, the tax rate and
// currency come from the registry and the company, and the cashier is checked
// against this tenant. What the payload actually supplies is what only it can
// know: which items, at what prices, paid how, and when.

// SaleApplier replays offline sales through the authoritative sale path.
type SaleApplier struct {
	svc *Service
}

// NewSaleApplier wires the sale service into the sync engine.
func NewSaleApplier(svc *Service) *SaleApplier { return &SaleApplier{svc: svc} }

// Ordered reports that sales must be applied in sequence.
//
// True, and not negotiable. Each terminal keeps its own ICV and hash chain, and
// a chain is only meaningful in order: applying sale 7 while 6 is still stuck
// leaves a gap, and a gap is precisely the signal ZATCA tamper detection looks
// for. The engine stalls the rest of a terminal's sales the moment one fails
// rather than racing ahead — per terminal, because chains are per terminal and
// one till's problem must not stop another's takings from landing.
func (a *SaleApplier) Ordered() bool { return true }

// offlineSale is the payload a terminal queues.
//
// Money and quantities are strings for the same reason they are across the HTTP
// boundary: a device that parsed a rate into a float64 and multiplied would
// drift from the server's numeric, and an offline queue can hold a drifting
// figure for days before anyone sees it.
type offlineSale struct {
	InvoiceUUID string `json:"invoice_uuid"`
	DocType     string `json:"doc_type"`
	IssuedAt    string `json:"issued_at"`
	WarehouseID string `json:"warehouse_id"`
	CashierID   string `json:"cashier_id"`

	// CustomerID is who owes it, when any part of the sale went on account.
	// Carried through the queue because the credit-limit check and the
	// receivable both need it, and a sale that arrived without it would post to
	// the control account with nobody's balance behind it.
	CustomerID string `json:"customer_id"`

	PricesIncludeTax *bool  `json:"prices_include_tax"`
	InvoiceDiscount  string `json:"invoice_discount"`

	Lines   []offlineLine   `json:"lines"`
	Tenders []offlineTender `json:"tenders"`
}

type offlineLine struct {
	VariantID     string `json:"variant_id"`
	Description   string `json:"description"`
	DescriptionAr string `json:"description_ar"`
	Qty           string `json:"qty"`
	UnitPrice     string `json:"unit_price"`
	LineDiscount  string `json:"line_discount"`
	// The campaign the discount came from, carried through the queue for the
	// same reason CustomerID is: the redemption is recorded when the sale
	// finalises, and an offline sale that arrived without it would spend a
	// campaign's budget without counting against its limit.
	PromotionID  string `json:"promotion_id"`
	TaxTreatment string `json:"tax_treatment"`
}

type offlineTender struct {
	Method    string `json:"method"`
	Amount    string `json:"amount"`
	Reference string `json:"reference"`
}

// Apply replays one offline sale.
//
// Returns sync.ErrAlreadyApplied when this sale has already been rung up, which
// the engine records as a duplicate rather than a failure. That is the expected
// outcome of a healthy retry: sync delivers at least once by design, so the
// same sale arriving twice is normal traffic, not an incident.
func (a *SaleApplier) Apply(
	ctx context.Context, tx pgx.Tx, tenantID, deviceID uuid.UUID, item sync.Item,
) error {
	var payload offlineSale
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return errs.New(errs.CodeInvalidInput,
			"This sale could not be read. The terminal may be running a newer "+
				"app version than the server.")
	}

	sale, warehouseID, err := a.build(payload, item)
	if err != nil {
		return err
	}

	// The cashier is checked against THIS tenant and must still be allowed to
	// sell. A device offline for a week can arrive carrying sales attributed to
	// someone who has since left, and an invoice naming a user from another
	// tenant would be a cross-tenant write dressed up as a synced sale.
	if err := checkCashier(ctx, tx, tenantID, sale.CashierID); err != nil {
		return err
	}

	out, err := a.svc.FinalizeInTx(ctx, tx, tenantID, deviceID, sale, warehouseID)
	if err != nil {
		return err
	}
	if out.AlreadyRung {
		return sync.ErrAlreadyApplied
	}
	return nil
}

// build turns a payload into a Sale, refusing anything malformed before it can
// reach the finalizer.
func (a *SaleApplier) build(p offlineSale, item sync.Item) (Sale, uuid.UUID, error) {
	invoiceUUID, err := uuid.Parse(p.InvoiceUUID)
	if err != nil {
		return Sale{}, uuid.Nil, errs.New(errs.CodeInvalidInput,
			"This sale carries no usable invoice identifier.")
	}

	// The queue's entity id and the payload's must agree. If they can differ,
	// the engine deduplicates on one and the finalizer on the other — and a
	// sale could be applied twice while every layer believed it had checked.
	if invoiceUUID != item.EntityUUID {
		return Sale{}, uuid.Nil, errs.New(errs.CodeInvalidInput,
			"This sale's identifier does not match the one it was queued under.")
	}

	if len(p.Lines) == 0 {
		return Sale{}, uuid.Nil, errs.New(errs.CodeInvalidInput,
			"This sale has no items on it.")
	}

	issuedAt, err := time.Parse(time.RFC3339, p.IssuedAt)
	if err != nil {
		// The DEVICE's time, not the server's. An offline sale belongs to the
		// day it was rung up, and dating it on arrival would put a week of
		// trading into the wrong month and the wrong VAT return.
		return Sale{}, uuid.Nil, errs.New(errs.CodeInvalidInput,
			"This sale does not say when it was rung up.")
	}

	warehouseID, err := optionalUUID(p.WarehouseID, "warehouse")
	if err != nil {
		return Sale{}, uuid.Nil, err
	}

	customerID, err := optionalUUID(p.CustomerID, "customer")
	if err != nil {
		return Sale{}, uuid.Nil, err
	}
	var customer *uuid.UUID
	if customerID != uuid.Nil {
		customer = &customerID
	}

	docType := p.DocType
	if docType == "" {
		docType = "simplified"
	}

	sale := Sale{
		InvoiceUUID: invoiceUUID,
		DocType:     docType,
		IssuedAt:    issuedAt,
		CustomerID:  customer,
		Input: SaleInput{
			PricesIncludeTax: p.PricesIncludeTax == nil || *p.PricesIncludeTax,
		},
		// Currency, TaxRate, Rules and StockPolicy are all left unset on
		// purpose. FinalizeInTx fills them from the company and the registry —
		// a terminal running last year's build must not replay a week of sales
		// at last year's VAT rate.
	}

	if sale.Input.InvoiceDiscount, err = optionalAmount(
		p.InvoiceDiscount, "invoice discount"); err != nil {
		return Sale{}, uuid.Nil, err
	}

	for i, l := range p.Lines {
		variantID, err := uuid.Parse(l.VariantID)
		if err != nil {
			return Sale{}, uuid.Nil, errs.Newf(errs.CodeInvalidInput,
				"Line %d names no recognisable item.", i+1)
		}
		qty, err := requiredAmount(l.Qty, "quantity", i)
		if err != nil {
			return Sale{}, uuid.Nil, err
		}
		unitPrice, err := requiredAmount(l.UnitPrice, "price", i)
		if err != nil {
			return Sale{}, uuid.Nil, err
		}
		var promotionID *uuid.UUID
		if raw := strings.TrimSpace(l.PromotionID); raw != "" {
			id, e := uuid.Parse(raw)
			if e != nil {
				return Sale{}, uuid.Nil, errs.New(errs.CodeInvalidInput,
					"A queued sale names a promotion that is not a valid id.")
			}
			promotionID = &id
		}

		lineDiscount, err := optionalAmount(l.LineDiscount, "line discount")
		if err != nil {
			return Sale{}, uuid.Nil, err
		}

		sale.Input.Lines = append(sale.Input.Lines, LineInput{
			VariantID: l.VariantID, Description: l.Description,
			DescriptionAr: l.DescriptionAr,
			Qty:           qty, UnitPrice: unitPrice, LineDiscount: lineDiscount,
			PromotionID:  promotionID,
			TaxTreatment: l.TaxTreatment,
		})
		sale.Lines = append(sale.Lines, SaleLineRef{VariantID: variantID})
	}

	for i, t := range p.Tenders {
		amount, err := requiredAmount(t.Amount, "payment", i)
		if err != nil {
			return Sale{}, uuid.Nil, err
		}
		sale.Tenders = append(sale.Tenders, Tender{
			Method: t.Method, Amount: amount, Reference: t.Reference,
		})
	}

	if p.CashierID != "" {
		cashierID, err := uuid.Parse(p.CashierID)
		if err != nil {
			return Sale{}, uuid.Nil, errs.New(errs.CodeInvalidInput,
				"This sale names no recognisable cashier.")
		}
		sale.CashierID = &cashierID
	}

	return sale, warehouseID, nil
}

// checkCashier refuses a sale attributed to someone who cannot sell.
//
// Read inside the tenant transaction, so row-level security answers the
// cross-tenant question for us: another tenant's user simply is not there.
func checkCashier(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, cashierID *uuid.UUID) error {
	if cashierID == nil {
		return nil
	}

	// Status is deliberately NOT checked. A cashier suspended since the sale
	// was rung up still made it, and refusing their queued sales would lose
	// real takings and leave a gap in the terminal's chain — worse than
	// recording a sale by someone since suspended, which the audit trail
	// surfaces anyway. What must still hold is that they were ever allowed to
	// sell at all, and that they belong to this tenant.
	var permitted bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		         SELECT 1
		         FROM user_role_assignment a
		         JOIN role_permission rp ON rp.role_id = a.role_id
		         WHERE a.user_id = u.id AND rp.permission = 'sales.create'
		       )
		FROM app_user u
		WHERE u.id = $1 AND u.tenant_id = $2`,
		*cashierID, tenantID).Scan(&permitted)

	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.CodeForbidden,
			"This sale is attributed to someone who does not work here.")
	}
	if err != nil {
		return err
	}
	if !permitted {
		return errs.New(errs.CodeForbidden,
			"This sale is attributed to someone who is not permitted to sell.")
	}
	return nil
}

func optionalUUID(raw, what string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errs.Newf(errs.CodeInvalidInput,
			"This sale names no recognisable %s.", what)
	}
	return id, nil
}

func requiredAmount(raw, what string, index int) (decimal.Decimal, error) {
	if raw == "" {
		return decimal.Zero, errs.Newf(errs.CodeInvalidInput,
			"Line %d is missing its %s.", index+1, what)
	}
	d, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, errs.Newf(errs.CodeInvalidInput,
			"Line %d has a %s that is not a number.", index+1, what)
	}
	return d, nil
}

func optionalAmount(raw, what string) (decimal.Decimal, error) {
	if raw == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, errs.Newf(errs.CodeInvalidInput,
			"The %s is not a number.", what)
	}
	return d, nil
}
