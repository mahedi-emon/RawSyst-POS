// What a customer sees when they sign in (blueprint F2).
//
// Every query here is filtered on the CALLER's customer id, taken from the
// session and never from a parameter. There is no route in this file that
// accepts a customer id, so there is no way to ask for somebody else's.

package portal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Me is the customer's own summary, which is the portal's home screen.
type Me struct {
	Name  string `json:"name"`
	Phone string `json:"phone,omitempty"`
	Email string `json:"email,omitempty"`

	Currency string `json:"currency"`

	// What they owe the shop, and what the shop holds for them. Both matter on
	// the same screen: somebody with store credit and an unpaid invoice wants
	// to know they can settle one with the other.
	//
	// A gift card's balance is store-credit entries carrying its id, which is
	// how the wallet module holds it. Splitting them here rather than summing
	// one figure because a card can be given away and a credit cannot.
	Outstanding string `json:"outstanding"`
	StoreCredit string `json:"store_credit"`
	GiftCards   string `json:"gift_card_balance"`

	// Loyalty. Absent for a shop that runs no scheme, which is different from
	// a member with no points.
	LoyaltyEnrolled bool   `json:"loyalty_enrolled"`
	Points          int64  `json:"points"`
	Tier            string `json:"tier,omitempty"`
}

// Read builds the home screen.
func (s *Service) Me(ctx context.Context, c Caller) (Me, error) {
	if c.CustomerID == nil {
		return Me{}, errs.New(errs.CodeForbidden, "That is not your account.")
	}
	var out Me
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		var outstanding, credit, cards decimal.Decimal
		if e := tx.QueryRow(ctx, `
			SELECT c.name, coalesce(c.phone, ''), coalesce(c.email, ''),
			       co.base_currency,
			       coalesce((SELECT sum(i.total_inclusive - coalesce((
			           SELECT sum(a.amount)
			             FROM customer_receipt_allocation a
			            WHERE a.invoice_id = i.id), 0))
			         FROM sales_invoice i
			        WHERE i.company_id = c.company_id
			          AND i.customer_id = c.id
			          AND i.doc_type = 'standard'), 0),
			       coalesce((SELECT sum(e.amount) FROM store_credit_entry e
			                  WHERE e.customer_id = c.id
			                    AND e.gift_card_id IS NULL), 0),
			       coalesce((SELECT sum(e.amount) FROM store_credit_entry e
			                  JOIN gift_card g ON g.id = e.gift_card_id
			                  WHERE e.customer_id = c.id
			                    AND NOT g.is_void), 0)
			FROM customer c
			JOIN company co ON co.id = c.company_id
			WHERE c.id = $1 AND c.company_id = $2`,
			*c.CustomerID, c.CompanyID).Scan(
			&out.Name, &out.Phone, &out.Email, &out.Currency,
			&outstanding, &credit, &cards); e != nil {
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound, "That account was not found.")
			}
			return e
		}
		out.Outstanding = outstanding.StringFixed(2)
		out.StoreCredit = credit.StringFixed(2)
		out.GiftCards = cards.StringFixed(2)

		// The balance is the sum of the ledger, which is what a member wants
		// to know. The TIER is computed by the loyalty module from lifetime
		// spend against the programme's own tier list, and is deliberately not
		// re-derived here: two answers to "what tier am I" is one too many.
		if e := tx.QueryRow(ctx, `
			SELECT coalesce(sum(points), 0) FROM loyalty_entry
			 WHERE customer_id = $1`, *c.CustomerID).Scan(&out.Points); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM loyalty_program
			                WHERE company_id = $1 AND is_active)`,
			c.CompanyID).Scan(&out.LoyaltyEnrolled)
	})
	return out, db.Translate(err, "")
}

// PortalInvoice is one of the customer's own invoices.
type PortalInvoice struct {
	ID          uuid.UUID `json:"id"`
	Number      string    `json:"human_number"`
	IssuedAt    string    `json:"issued_at"`
	Total       string    `json:"total"`
	Paid        string    `json:"paid"`
	Currency    string    `json:"currency"`
	Outstanding string    `json:"outstanding"`
}

// Invoices lists what the customer has bought.
func (s *Service) Invoices(
	ctx context.Context, c Caller,
) ([]PortalInvoice, error) {
	if c.CustomerID == nil {
		return nil, errs.New(errs.CodeForbidden, "That is not your account.")
	}
	out := []PortalInvoice{}
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT i.id, i.human_number, i.issued_at, i.total_inclusive,
			       coalesce((SELECT sum(a.amount)
			                   FROM customer_receipt_allocation a
			                  WHERE a.invoice_id = i.id), 0),
			       i.currency
			FROM sales_invoice i
			WHERE i.company_id = $1 AND i.customer_id = $2
			ORDER BY i.issued_at DESC
			LIMIT 200`, c.CompanyID, *c.CustomerID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var inv PortalInvoice
			var total, paid decimal.Decimal
			var issued time.Time
			if e := rows.Scan(&inv.ID, &inv.Number, &issued, &total, &paid,
				&inv.Currency); e != nil {
				return e
			}
			inv.IssuedAt = issued.UTC().Format(time.RFC3339)
			inv.Total = total.StringFixed(2)
			inv.Paid = paid.StringFixed(2)
			inv.Outstanding = total.Sub(paid).StringFixed(2)
			out = append(out, inv)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// PortalOrder is an order and where it has got to.
type PortalOrder struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"order_no"`
	State    string    `json:"state"`
	PlacedAt string    `json:"placed_at"`
	Total    string    `json:"total"`
	Currency string    `json:"currency"`

	// The delivery, when there is one. F2 asks for order status AND delivery
	// tracking, and a customer does not distinguish them.
	DeliveryStatus string `json:"delivery_status,omitempty"`
	DriverName     string `json:"driver_name,omitempty"`
	DeliveredAt    string `json:"delivered_at,omitempty"`
}

// Orders lists what is on its way.
func (s *Service) Orders(
	ctx context.Context, c Caller,
) ([]PortalOrder, error) {
	if c.CustomerID == nil {
		return nil, errs.New(errs.CodeForbidden, "That is not your account.")
	}
	out := []PortalOrder{}
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT o.id, o.order_no, o.state, o.created_at,
			       coalesce((SELECT sum(l.qty * l.unit_price - l.discount)
			                   FROM sales_order_line l
			                  WHERE l.order_id = o.id), 0),
			       o.currency,
			       coalesce(d.status, ''), coalesce(u.full_name, ''),
			       d.delivered_at
			FROM sales_order o
			LEFT JOIN delivery d ON d.order_id = o.id
			LEFT JOIN app_user u ON u.id = d.driver_id
			WHERE o.company_id = $1 AND o.customer_id = $2
			ORDER BY o.created_at DESC
			LIMIT 200`, c.CompanyID, *c.CustomerID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var o PortalOrder
			var total decimal.Decimal
			var placed time.Time
			var delivered *time.Time
			if e := rows.Scan(&o.ID, &o.Number, &o.State, &placed, &total,
				&o.Currency, &o.DeliveryStatus, &o.DriverName,
				&delivered); e != nil {
				return e
			}
			o.PlacedAt = placed.UTC().Format(time.RFC3339)
			o.Total = total.StringFixed(2)
			if delivered != nil {
				o.DeliveredAt = delivered.UTC().Format(time.RFC3339)
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// Warranty is what a serial number is covered for.
type Warranty struct {
	SerialNo   string `json:"serial_no"`
	Product    string `json:"product,omitempty"`
	Status     string `json:"status"`
	SoldOn     string `json:"sold_on,omitempty"`
	ExpiresOn  string `json:"expires_on,omitempty"`
	InWarranty bool   `json:"in_warranty"`
}

// CheckWarranty answers "is this still covered".
//
// F2 lets a customer check by serial number, and deliberately only tells them
// about a serial the shop sold to THEM. Answering for any serial would let
// anybody enumerate what a shop has sold.
func (s *Service) CheckWarranty(
	ctx context.Context, c Caller, serialNo string,
) (Warranty, error) {
	if c.CustomerID == nil {
		return Warranty{}, errs.New(errs.CodeForbidden,
			"That is not your account.")
	}
	serialNo = strings.TrimSpace(serialNo)
	if serialNo == "" {
		return Warranty{}, errs.New(errs.CodeInvalidInput,
			"Enter the serial number printed on the item.")
	}

	var out Warranty
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		var sold, expires *time.Time
		e := tx.QueryRow(ctx, `
			SELECT s.serial_no, coalesce(p.name, ''), s.status,
			       s.sold_at, s.warranty_until
			FROM stock_serial s
			LEFT JOIN variant v ON v.id = s.variant_id
			LEFT JOIN product p ON p.id = v.product_id
			WHERE s.company_id = $1 AND s.serial_no = $2
			  AND s.customer_id = $3`,
			c.CompanyID, serialNo, *c.CustomerID).Scan(
			&out.SerialNo, &out.Product, &out.Status, &sold, &expires)
		if e == pgx.ErrNoRows {
			return errs.New(errs.CodeNotFound,
				"No item with that serial number was sold to you.")
		}
		if e != nil {
			return e
		}
		if sold != nil {
			out.SoldOn = sold.Format("2006-01-02")
		}
		if expires != nil {
			out.ExpiresOn = expires.Format("2006-01-02")
			out.InWarranty = expires.After(time.Now().UTC())
		}
		return nil
	})
	return out, db.Translate(err, "")
}

// Address is a saved delivery address.
type Address struct {
	ID        uuid.UUID `json:"id"`
	Label     string    `json:"label"`
	Line1     string    `json:"line1"`
	Line2     string    `json:"line2,omitempty"`
	City      string    `json:"city,omitempty"`
	District  string    `json:"district,omitempty"`
	Postcode  string    `json:"postcode,omitempty"`
	Country   string    `json:"country,omitempty"`
	Phone     string    `json:"phone,omitempty"`
	IsDefault bool      `json:"is_default"`
}

// Addresses lists them.
func (s *Service) Addresses(
	ctx context.Context, c Caller,
) ([]Address, error) {
	if c.CustomerID == nil {
		return nil, errs.New(errs.CodeForbidden, "That is not your account.")
	}
	out := []Address{}
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, label, line1, coalesce(line2, ''), coalesce(city, ''),
			       coalesce(district, ''), coalesce(postcode, ''),
			       coalesce(country, ''), coalesce(phone, ''), is_default
			FROM customer_address
			WHERE customer_id = $1 AND company_id = $2
			ORDER BY is_default DESC, label`, *c.CustomerID, c.CompanyID)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var a Address
			if e := rows.Scan(&a.ID, &a.Label, &a.Line1, &a.Line2, &a.City,
				&a.District, &a.Postcode, &a.Country, &a.Phone,
				&a.IsDefault); e != nil {
				return e
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// SaveAddress adds or replaces one.
func (s *Service) SaveAddress(
	ctx context.Context, c Caller, in Address,
) ([]Address, error) {
	if c.CustomerID == nil {
		return nil, errs.New(errs.CodeForbidden, "That is not your account.")
	}
	if strings.TrimSpace(in.Label) == "" || strings.TrimSpace(in.Line1) == "" {
		return nil, errs.New(errs.CodeInvalidInput,
			"Give the address a name and a first line.")
	}

	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		// At most one default, enforced by a partial unique index. Clearing
		// the old one first makes "make this my usual address" work rather
		// than conflict, which is what pressing it means.
		if in.IsDefault {
			if _, e := tx.Exec(ctx, `
				UPDATE customer_address SET is_default = false
				 WHERE customer_id = $1 AND is_default`, *c.CustomerID); e != nil {
				return e
			}
		}

		if in.ID == uuid.Nil {
			_, e := tx.Exec(ctx, `
				INSERT INTO customer_address (
				  tenant_id, company_id, customer_id, label, line1, line2,
				  city, district, postcode, country, phone, is_default)
				VALUES ($1,$2,$3,$4,$5,nullif($6,''),nullif($7,''),
				        nullif($8,''),nullif($9,''),nullif(lower($10),''),
				        nullif($11,''),$12)`,
				c.TenantID, c.CompanyID, *c.CustomerID, in.Label, in.Line1,
				in.Line2, in.City, in.District, in.Postcode, in.Country,
				in.Phone, in.IsDefault)
			return db.Translate(e, "That address could not be saved.")
		}

		tag, e := tx.Exec(ctx, `
			UPDATE customer_address
			   SET label = $3, line1 = $4, line2 = nullif($5,''),
			       city = nullif($6,''), district = nullif($7,''),
			       postcode = nullif($8,''), country = nullif(lower($9),''),
			       phone = nullif($10,''), is_default = $11
			 WHERE id = $1 AND customer_id = $2`,
			in.ID, *c.CustomerID, in.Label, in.Line1, in.Line2, in.City,
			in.District, in.Postcode, in.Country, in.Phone, in.IsDefault)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.CodeNotFound, "That address was not found.")
		}
		return nil
	})
	if err != nil {
		return nil, db.Translate(err, "")
	}
	return s.Addresses(ctx, c)
}

// RemoveAddress deletes one.
func (s *Service) RemoveAddress(
	ctx context.Context, c Caller, id uuid.UUID,
) error {
	if c.CustomerID == nil {
		return errs.New(errs.CodeForbidden, "That is not your account.")
	}
	return db.Translate(s.pool.TxAsTenant(ctx, c.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx,
				`DELETE FROM customer_address
				  WHERE id = $1 AND customer_id = $2`, id, *c.CustomerID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeNotFound,
					"That address was not found.")
			}
			return nil
		}), "")
}

// ReturnRequest is a customer asking to send something back.
type ReturnRequest struct {
	ID           uuid.UUID  `json:"id"`
	Number       string     `json:"request_no"`
	InvoiceID    *uuid.UUID `json:"invoice_id,omitempty"`
	InvoiceNo    string     `json:"invoice_no,omitempty"`
	Kind         string     `json:"kind"`
	Reason       string     `json:"reason"`
	Items        string     `json:"items"`
	Status       string     `json:"status"`
	DecisionNote string     `json:"decision_note,omitempty"`
	CreatedAt    string     `json:"created_at"`
	DecidedAt    string     `json:"decided_at,omitempty"`
	// The staff screen needs the customer's name; the portal ignores it.
	CustomerName string `json:"customer_name,omitempty"`
}

// AskToReturn records a request.
func (s *Service) AskToReturn(
	ctx context.Context, c Caller, invoiceID *uuid.UUID,
	kind, reason, items string,
) (ReturnRequest, error) {
	if c.CustomerID == nil {
		return ReturnRequest{}, errs.New(errs.CodeForbidden,
			"That is not your account.")
	}
	if kind == "" {
		kind = "return"
	}
	if strings.TrimSpace(reason) == "" || strings.TrimSpace(items) == "" {
		return ReturnRequest{}, errs.New(errs.CodeInvalidInput,
			"Say what you would like to send back and why.")
	}

	var id uuid.UUID
	err := s.pool.TxAsTenant(ctx, c.TenantID, func(tx pgx.Tx) error {
		// The invoice, if named, has to be one of theirs. Without the check a
		// customer could attach their request to somebody else's receipt and
		// the shop would see the wrong sale.
		if invoiceID != nil {
			var theirs bool
			e := tx.QueryRow(ctx, `
				SELECT true FROM sales_invoice i
				WHERE i.id = $1 AND i.company_id = $2
				  AND i.customer_id = $3`,
				*invoiceID, c.CompanyID, *c.CustomerID).Scan(&theirs)
			if e == pgx.ErrNoRows {
				return errs.New(errs.CodeNotFound,
					"That receipt is not one of yours.")
			}
			if e != nil {
				return e
			}
		}

		var n int64
		if e := tx.QueryRow(ctx,
			`SELECT nextval('return_request_seq')`).Scan(&n); e != nil {
			return e
		}

		return tx.QueryRow(ctx, `
			INSERT INTO return_request (
			  tenant_id, company_id, customer_id, request_no, invoice_id,
			  kind, reason, items)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			c.TenantID, c.CompanyID, *c.CustomerID,
			fmt.Sprintf("RET-%06d", n), invoiceID, kind,
			strings.TrimSpace(reason), strings.TrimSpace(items)).Scan(&id)
	})
	if err != nil {
		return ReturnRequest{}, db.Translate(err,
			"That request could not be recorded.")
	}

	list, err := s.MyReturnRequests(ctx, c)
	if err != nil {
		return ReturnRequest{}, err
	}
	for _, r := range list {
		if r.ID == id {
			return r, nil
		}
	}
	return ReturnRequest{}, errs.New(errs.CodeNotFound,
		"That request was not found.")
}

// MyReturnRequests lists the caller's own.
func (s *Service) MyReturnRequests(
	ctx context.Context, c Caller,
) ([]ReturnRequest, error) {
	if c.CustomerID == nil {
		return nil, errs.New(errs.CodeForbidden, "That is not your account.")
	}
	return s.returnRequests(ctx, c.TenantID, c.CompanyID, c.CustomerID, false)
}

// PendingReturnRequests is the STAFF read: every open request in the shop.
func (s *Service) PendingReturnRequests(
	ctx context.Context, scope Scope, openOnly bool,
) ([]ReturnRequest, error) {
	return s.returnRequests(ctx, scope.TenantID, scope.CompanyID, nil, openOnly)
}

func (s *Service) returnRequests(
	ctx context.Context, tenantID, companyID uuid.UUID,
	customerID *uuid.UUID, openOnly bool,
) ([]ReturnRequest, error) {
	out := []ReturnRequest{}
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT r.id, r.request_no, r.invoice_id,
			       coalesce(i.human_number, ''), r.kind, r.reason, r.items,
			       r.status, coalesce(r.decision_note, ''), r.created_at,
			       r.decided_at, cu.name
			FROM return_request r
			JOIN customer cu ON cu.id = r.customer_id
			LEFT JOIN sales_invoice i ON i.id = r.invoice_id
			WHERE r.company_id = $1
			  AND ($2::uuid IS NULL OR r.customer_id = $2)
			  AND (NOT $3::boolean OR r.status = 'requested')
			ORDER BY r.created_at DESC
			LIMIT 200`, companyID, customerID, openOnly)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r ReturnRequest
			var created time.Time
			var decided *time.Time
			if e := rows.Scan(&r.ID, &r.Number, &r.InvoiceID, &r.InvoiceNo,
				&r.Kind, &r.Reason, &r.Items, &r.Status, &r.DecisionNote,
				&created, &decided, &r.CustomerName); e != nil {
				return e
			}
			r.CreatedAt = created.UTC().Format(time.RFC3339)
			if decided != nil {
				r.DecidedAt = decided.UTC().Format(time.RFC3339)
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// DecideReturnRequest is the shop's answer.
//
// It does NOT post a return. The return itself goes through the existing
// returns path, where the accounting and the stock movement belong; this
// records that the shop agreed to receive the goods.
func (s *Service) DecideReturnRequest(
	ctx context.Context, scope Scope, actorID, id uuid.UUID,
	accept bool, note string,
) error {
	status := "accepted"
	if !accept {
		status = "refused"
		if strings.TrimSpace(note) == "" {
			return errs.New(errs.CodeInvalidInput,
				"Give the customer a reason.")
		}
	}

	return db.Translate(s.pool.TxAsTenant(ctx, scope.TenantID,
		func(tx pgx.Tx) error {
			tag, e := tx.Exec(ctx, `
				UPDATE return_request
				   SET status = $3, decision_note = nullif($4,''),
				       decided_at = now(), decided_by = $5
				 WHERE id = $1 AND company_id = $2 AND status = 'requested'`,
				id, scope.CompanyID, status, strings.TrimSpace(note), actorID)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.CodeConflict,
					"That request was not found, or it is already answered.")
			}
			return nil
		}), "")
}
