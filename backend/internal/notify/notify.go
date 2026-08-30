// Package notify is the notification centre (blueprint D3).
//
// # A notification is a fact, not a message
//
// D3 lists fourteen triggers — low stock, an online order, a payment due, a
// failed backup — and each of them is something that BECAME TRUE. The row here
// records that fact once; how it reached somebody (in-app, email, SMS, push) is
// a delivery attempt against it. Writing one row per channel instead would mean
// the same low-stock warning appearing three times in a list, and a person
// marking one of them read while the other two stayed bold.
//
// # The same fact is not raised twice
//
// Stock going below its reorder point is true continuously, not once. Something
// has to decide when to say so again, and the answer here is a quiet window per
// (kind, subject): the same fact about the same thing is not re-raised inside
// it. Without that, every screen that reads a stock level would post a
// notification, and the centre would fill with one warning repeated four
// hundred times — which is indistinguishable from being broken.
//
// # Nobody reads somebody else's
//
// There is no permission for reading another person's notifications, and no
// route that could. A notification with a user_id belongs to that person; one
// without belongs to whoever in the company may see that kind, which is how a
// low-stock warning reaches whoever is looking rather than nobody in
// particular.
package notify

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// The severities, in the order a person cares about them.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// D3's trigger list. Named constants rather than free text at the call sites,
// so a typo becomes a compile error instead of a notification nobody's
// preferences match.
const (
	KindLowStock           = "low_stock"
	KindOnlineOrder        = "online_order"
	KindPurchaseRequest    = "purchase_request"
	KindPaymentDue         = "payment_due"
	KindSupplierPaymentDue = "supplier_payment_due"
	KindCreditLimit        = "credit_limit"
	KindExpenseApproval    = "expense_approval"
	KindStockTransfer      = "stock_transfer"
	KindSubmissionFailed   = "submission_failed"
	KindBackupFailed       = "backup_failed"
	KindSuspiciousLogin    = "suspicious_login"
	KindWarrantyExpiring   = "warranty_expiring"
	KindBatchExpiring      = "batch_expiring"
	KindIDExpiring         = "id_expiring"
	KindApprovalRequired   = "approval_required"
	KindAnnouncement       = "announcement"
)

// Kinds is every trigger a person can hold a preference about.
//
// Exported because the preferences screen has to offer all of them: a list
// built from what has already happened would hide the trigger somebody most
// wants to be told about, which is the one that has not fired yet.
var Kinds = []string{
	KindLowStock, KindOnlineOrder, KindPurchaseRequest, KindPaymentDue,
	KindSupplierPaymentDue, KindCreditLimit, KindExpenseApproval,
	KindStockTransfer, KindSubmissionFailed, KindBackupFailed,
	KindSuspiciousLogin, KindWarrantyExpiring, KindBatchExpiring,
	KindIDExpiring, KindApprovalRequired, KindAnnouncement,
}

// quietWindow is how long the same fact about the same thing stays quiet.
//
// Six hours: long enough that a stock level read all morning raises one
// warning, short enough that a shop opening the next day is told again.
const quietWindow = "6 hours"

// Service reads and raises notifications.
type Service struct {
	pool *db.Pool
}

// NewService builds the service.
func NewService(pool *db.Pool) *Service { return &Service{pool: pool} }

// Scope is who is asking and on whose books.
type Scope struct {
	TenantID  uuid.UUID
	CompanyID uuid.UUID
	UserID    uuid.UUID
}

// Fact is something that became true and somebody should be told about.
type Fact struct {
	Kind     string
	Severity string
	Title    string
	Body     string

	// Subject and SubjectID say what it is about, so tapping the notification
	// goes somewhere useful and so the de-duplicator can tell two facts apart.
	Subject   string
	SubjectID *uuid.UUID

	// UserID addresses it to one person. Nil means everybody in the company
	// who may see this kind.
	UserID *uuid.UUID
}

// Notification is one fact as a screen reads it.
type Notification struct {
	ID        uuid.UUID  `json:"id"`
	Kind      string     `json:"kind"`
	Severity  string     `json:"severity"`
	Title     string     `json:"title"`
	Body      string     `json:"body,omitempty"`
	Subject   string     `json:"subject,omitempty"`
	SubjectID *uuid.UUID `json:"subject_id,omitempty"`
	Read      bool       `json:"is_read"`
	ReadAt    string     `json:"read_at,omitempty"`
	CreatedAt string     `json:"created_at"`
}

// Preference is what one person wants to hear about, and how.
type Preference struct {
	Kind  string `json:"kind"`
	InApp bool   `json:"in_app"`
	Email bool   `json:"email"`
	SMS   bool   `json:"sms"`
	Push  bool   `json:"push"`
}

// Raise records a fact, and queues the deliveries the recipients want.
//
// Takes a tx because it is called from inside the transaction of whatever
// became true: a stock movement that dropped below the reorder point and a
// warning about it must appear together, or a shop is told about a shortage
// that a rolled-back transaction never caused.
//
// Returns the id of the notification written, or uuid.Nil when the same fact
// was raised recently enough to stay quiet. Callers do not have to care —
// nothing here fails a caller's transaction for want of a notification.
func Raise(
	ctx context.Context, tx pgx.Tx,
	tenantID, companyID uuid.UUID, f Fact,
) (uuid.UUID, error) {
	if strings.TrimSpace(f.Kind) == "" || strings.TrimSpace(f.Title) == "" {
		return uuid.Nil, errs.New(errs.CodeInternal,
			"A notification needs a kind and a title.")
	}
	severity := f.Severity
	if severity == "" {
		severity = SeverityInfo
	}

	// The quiet window, and only for a fact ABOUT SOMETHING.
	//
	// It exists for conditions that are true continuously and get re-observed:
	// a product below its reorder point is still below it every time a screen
	// reads the stock level. Those all name a subject, and the window is scoped
	// to (kind, subject) rather than to the kind alone — two different products
	// running low are two facts, and silencing the second because the first was
	// recent would hide a real shortage.
	//
	// A fact with no subject is an EVENT: a backup failed at three in the
	// morning, a manager posted a notice. Events are not re-observed, each one
	// is new, and de-duplicating them would swallow the second failure of the
	// night and the afternoon's second notice.
	if f.SubjectID != nil {
		var recent bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM notification
			  WHERE company_id = $1 AND kind = $2
			    AND subject_id = $3
			    AND user_id IS NOT DISTINCT FROM $4
			    AND created_at > now() - $5::interval)`,
			companyID, f.Kind, *f.SubjectID, f.UserID,
			quietWindow).Scan(&recent); err != nil {
			return uuid.Nil, err
		}
		if recent {
			return uuid.Nil, nil
		}
	}

	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO notification
		  (tenant_id, company_id, kind, severity, title, body, subject,
		   subject_id, user_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id`,
		tenantID, companyID, f.Kind, severity, strings.TrimSpace(f.Title),
		nullText(f.Body), nullText(f.Subject), f.SubjectID,
		f.UserID).Scan(&id); err != nil {
		return uuid.Nil, db.Translate(err, "That notification could not be raised.")
	}

	if err := queueDeliveries(ctx, tx, tenantID, id, f); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// queueDeliveries writes one row per channel each recipient wants.
//
// In-app is always written and never asks: the centre is where a person goes
// to find out what happened, and letting somebody switch off the record of an
// event would mean a shop with no way to discover why a submission failed. The
// other three channels are opt-in, which is what D3's consent gate requires —
// an SMS costs money and arrives on somebody's own phone.
func queueDeliveries(
	ctx context.Context, tx pgx.Tx, tenantID, notificationID uuid.UUID, f Fact,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO notification_delivery
		  (tenant_id, notification_id, channel, status, sent_at)
		VALUES ($1,$2,'in_app','sent',now())`,
		tenantID, notificationID); err != nil {
		return err
	}

	// Only a notification addressed to one person can reach that person's own
	// phone. A company-wide fact has no single address to send to, and guessing
	// one — "everybody with this permission" — would text a shop's whole staff
	// list every time stock ran low.
	if f.UserID == nil {
		return nil
	}

	rows, err := tx.Query(ctx, `
		SELECT p.email, p.sms, p.push,
		       coalesce(u.email, ''), coalesce(u.phone, '')
		FROM notification_preference p
		JOIN app_user u ON u.id = p.user_id
		WHERE p.user_id = $1 AND p.kind = $2`, *f.UserID, f.Kind)
	if err != nil {
		return err
	}
	defer rows.Close()

	type want struct {
		channel     string
		destination string
	}
	var wanted []want
	for rows.Next() {
		var email, sms, push bool
		var address, phone string
		if e := rows.Scan(&email, &sms, &push, &address, &phone); e != nil {
			return e
		}
		if email && address != "" {
			wanted = append(wanted, want{"email", address})
		}
		if sms && phone != "" {
			wanted = append(wanted, want{"sms", phone})
		}
		if push {
			wanted = append(wanted, want{"push", ""})
		}
	}
	if e := rows.Err(); e != nil {
		return e
	}

	for _, w := range wanted {
		if _, err := tx.Exec(ctx, `
			INSERT INTO notification_delivery
			  (tenant_id, notification_id, channel, destination)
			VALUES ($1,$2,$3,$4)`,
			tenantID, notificationID, w.channel, nullText(w.destination)); err != nil {
			return err
		}
	}
	return nil
}

func nullText(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}
