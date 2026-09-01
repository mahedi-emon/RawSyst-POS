package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/identity"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/portal"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/reports"
)

// The two notification kinds that are not a password reset: a customer's portal
// sign-in code, and a scheduled report.
//
// # A code goes to a phone, and a phone is not a mailbox
//
// `Texter` exists for the same reason `Mailer` does, and its refusing default
// behaves the same way: a deployment with no SMS provider FAILS the job rather
// than discarding it, so an operator finds out that portal sign-in is broken
// before a customer does.
//
// # A scheduled report is computed when it is sent, not when it was scheduled
//
// The handler runs the report at delivery time. That is what keeps the schedule
// from being a snapshot — and it is why the handler holds the reports service
// rather than the sweep pre-rendering figures into the payload.

// Texter delivers one short message to a phone.
//
// The same contract as Mailer: return an error for anything transient so the
// job goes back on the queue, and succeed only when the message has genuinely
// been handed over.
type Texter interface {
	Send(ctx context.Context, to, body string) error
}

// LogTexter writes the message to the log instead of sending it.
//
// For development, at WARN for the reason LogMailer gives: this is not a normal
// state and should be visible in a log filtered to things needing attention.
type LogTexter struct{ Log *slog.Logger }

func (t LogTexter) Send(ctx context.Context, to, body string) error {
	t.Log.Warn("no SMS provider configured; message logged instead of sent",
		slog.String("to", to), slog.String("body", body))
	return nil
}

// RefusingTexter fails every send.
type RefusingTexter struct{}

func (RefusingTexter) Send(context.Context, string, string) error {
	return errs.New(errs.CodeUnavailable,
		"No SMS provider is configured, so this code cannot be sent. Set one "+
			"up, or customers cannot sign in to the portal.")
}

// portalCodeMessage is what a customer reads on their phone.
//
// Short, because it is read on a lock screen, and it says what to do if they
// did not ask — a sign-in code arriving unbidden is how somebody learns their
// number is being tried, and a message that does not say so wastes the warning.
func portalCodeMessage(p identity.NotifyPayload) string {
	return fmt.Sprintf(
		"%s is your code. It expires in %d minutes. If you did not ask for "+
			"it, ignore this message.",
		p.Code, p.ExpiresInMinutes)
}

// scheduledReport is the payload the sweep queues.
type scheduledReport struct {
	Kind   string `json:"kind"`
	Email  string `json:"email"`
	Report struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"report"`
	// CompanyID and TenantID come off the job rather than the payload, so a
	// payload cannot name a company the job was not queued for.
	CompanyID string `json:"company_id"`
}

// renderReport computes the report and returns a plain-text summary.
//
// Plain text rather than a rendered document, and stated rather than glossed:
// the product has no PDF surface, and inventing one inside a mail template
// would be a second document renderer that nobody could keep in step with the
// screens. The figures are the point — an accountant reading "September:
// revenue 412,300, gross profit 128,900" has what they asked for.
func renderReport(
	ctx context.Context, svc *reports.Service, scope reports.Scope,
	kind string, from, to time.Time,
) (string, error) {
	switch kind {
	case "profit_and_loss":
		pl, err := svc.ProfitAndLossFor(ctx, scope, from, to)
		if err != nil {
			return "", err
		}
		return join(
			line("Sales", pl.RevenueTotal, pl.BaseCurrency),
			line("Cost of sales", pl.CostOfSalesTotal, pl.BaseCurrency),
			line("Gross profit", pl.GrossProfit, pl.BaseCurrency),
			line("Running costs", pl.ExpensesTotal, pl.BaseCurrency),
			line("Profit", pl.NetProfit, pl.BaseCurrency),
		), nil

	case "balance_sheet":
		bs, err := svc.BalanceSheetAt(ctx, scope, to)
		if err != nil {
			return "", err
		}
		out := join(
			line("What is owned", bs.AssetsTotal, bs.BaseCurrency),
			line("What is owed", bs.LiabilitiesTotal, bs.BaseCurrency),
			line("What is left", bs.EquityTotal, bs.BaseCurrency),
		)
		// Reported rather than hidden, the way the screen reports it: a
		// balance sheet whose whole purpose is to reveal an imbalance should
		// say so in the email too.
		if !bs.Balanced {
			out += imbalance("balance sheet", bs.Difference, bs.BaseCurrency)
		}
		return out, nil

	case "trial_balance":
		tb, err := svc.TrialBalanceAt(ctx, scope, to)
		if err != nil {
			return "", err
		}
		out := join(
			line("Total debits", tb.TotalDebit, tb.BaseCurrency),
			line("Total credits", tb.TotalCredit, tb.BaseCurrency),
		)
		if !tb.Balanced {
			out += imbalance("trial balance", tb.Difference, tb.BaseCurrency)
		}
		return out, nil

	case "cash_flow":
		cf, err := svc.CashFlowFor(ctx, scope, from, to)
		if err != nil {
			return "", err
		}
		return join(
			line("Opening", cf.Opening, cf.BaseCurrency),
			line("Net movement", cf.NetTotal, cf.BaseCurrency),
			line("Closing", cf.Closing, cf.BaseCurrency),
		), nil
	}

	// A kind this handler has not learned to render. Permanent rather than
	// transient: retrying will not teach it, and the schedule's own row records
	// the reason where the owner will see it.
	return "", Permanent{errs.Newf(errs.CodeInvalidInput,
		"There is no summary for a %s report yet.", kind)}
}

// imbalance is what a statement says when it does not balance.
//
// Reported rather than hidden, the way the screens report it: a statement whose
// whole purpose is to reveal an imbalance should reveal it in the email too,
// and an accountant reading a tidy set of figures that quietly does not add up
// is worse served than one who is told.
func imbalance(what, difference, currency string) string {
	return fmt.Sprintf(
		"\n\nThe %s does NOT balance. Difference: %s %s",
		what, difference, currency)
}

func line(label, amount, currency string) string {
	return fmt.Sprintf("%-16s %14s %s", label, amount, currency)
}

func join(lines ...string) string {
	return strings.Join(lines, "\n")
}

// portalCodeKind is the payload kind the portal queues.
//
// Named here rather than imported so `jobs` does not have to hold the portal
// package for one constant — though it does hold it for the type below, which
// is why they agree by test rather than by hope.
var _ = portal.NotifyKindPortalCode

func reportScopeFor(tenantID, companyID uuid.UUID) reports.Scope {
	return reports.Scope{TenantID: tenantID, CompanyID: companyID}
}
