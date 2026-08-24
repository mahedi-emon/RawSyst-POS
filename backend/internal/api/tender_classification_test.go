//go:build integration

package api

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Every tender method has to be classified, or the Z report quietly guesses.
//
// "Non-cash takings" used to mean "every tender that is not cash", which
// counted three things that are not money arriving: a sale on account, where
// the customer paid nothing; store credit, which the shop already held; and
// loyalty points, the same against another liability. An Owner reconciling
// card settlements against that figure could never make it tie, and the
// natural conclusion is that the acquirer has underpaid.
//
// Fixing the query fixes today. This fixes tomorrow: the CHECK constraint on
// sales_tender.method is a closed set, and the moment somebody adds to it this
// test fails until the new method has been put in one of the two lists below.
// Adding a payment method should be a decision about what it MEANS, not a
// default somebody inherits.

// Money that genuinely arrives by a route other than notes. These belong in
// non-cash takings, and an Owner expects the acquirer or the bank to deposit
// them.
var arrivesAsMoney = map[string]string{
	"mada":          "Saudi national debit",
	"visa":          "card",
	"mastercard":    "card",
	"amex":          "card",
	"apple_pay":     "wallet",
	"stc_pay":       "wallet",
	"samsung_pay":   "wallet",
	"sadad":         "national bill payment",
	"bank_transfer": "settles into the bank",
	"cheque":        "settles into the bank",
	"tabby":         "buy now pay later; the provider settles",
	"tamara":        "buy now pay later; the provider settles",
	"bkash":         "Bangladesh mobile money",
	"nagad":         "Bangladesh mobile money",
}

// Not money arriving, for four different reasons.
var notTakings = map[string]string{
	"cash":              "counted separately, as physical notes",
	"customer_due":      "the customer paid nothing; it sits in receivables",
	"store_credit":      "the shop already held this; redeeming settles a liability",
	"loyalty_points":    "the same, against a different liability",
	"exchange_clearing": "a bookkeeping device, not a payment",
}

// methodsIn reads the permitted values straight out of the CHECK constraint,
// so the test cannot drift from the schema.
func methodsIn(t *testing.T, h *harness, constraint string) []string {
	t.Helper()
	ctx := context.Background()

	var definition string
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT pg_get_constraintdef(oid)
			  FROM pg_constraint WHERE conname = $1`, constraint).Scan(&definition)
	}); err != nil {
		t.Fatalf("reading %s: %v", constraint, err)
	}

	// pg_get_constraintdef renders the list as ARRAY['cash'::text, ...].
	var out []string
	for _, part := range strings.Split(definition, "'") {
		part = strings.TrimSpace(part)
		if part == "" || strings.ContainsAny(part, "(),:[]= ") {
			continue
		}
		out = append(out, part)
	}
	sort.Strings(out)
	return out
}

func TestEveryTenderMethodIsClassifiedForTheZReport(t *testing.T) {
	h := newHarness(t)

	methods := methodsIn(t, h, "sales_tender_method_valid")
	if len(methods) == 0 {
		t.Fatal("no tender methods were read from the constraint; the parse is " +
			"wrong and this test would pass whatever the schema said")
	}

	var unclassified []string
	for _, m := range methods {
		_, isMoney := arrivesAsMoney[m]
		_, isNot := notTakings[m]

		if isMoney && isNot {
			t.Errorf("%q is in both lists; it cannot be both takings and not", m)
		}
		if !isMoney && !isNot {
			unclassified = append(unclassified, m)
		}
	}

	if len(unclassified) > 0 {
		t.Errorf("these tender methods are not classified: %v\n\n"+
			"Decide what each one MEANS before it reaches a Z report. If money "+
			"actually arrives by it -- a card, a wallet, a bank transfer -- add "+
			"it to arrivesAsMoney and it will be counted in non-cash takings. If "+
			"it settles a liability the shop already held, or puts the amount on "+
			"a customer's account, add it to notTakings.\n\n"+
			"Guessing is how a sale on account came to be reported as money "+
			"taken, which an Owner then spent an afternoon looking for.",
			unclassified)
	}

	// And the lists must not have grown things the schema does not permit,
	// which would mean a method was removed and the reasoning left behind.
	permitted := map[string]bool{}
	for _, m := range methods {
		permitted[m] = true
	}
	for m := range arrivesAsMoney {
		if !permitted[m] {
			t.Errorf("arrivesAsMoney lists %q, which the schema no longer permits", m)
		}
	}
	for m := range notTakings {
		if !permitted[m] {
			t.Errorf("notTakings lists %q, which the schema no longer permits", m)
		}
	}
}

// The report must agree with the classification above, not merely with itself.
//
// Reads the function's own SQL and checks that every method classified as not
// being takings is actually excluded from the non-cash figure.
func TestTheZReportExcludesEveryMethodThatIsNotTakings(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	var body string
	if err := h.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT prosrc FROM pg_proc WHERE proname = 'cash_session_report'`).
			Scan(&body)
	}); err != nil {
		t.Fatalf("reading the report function: %v", err)
	}

	// The tender half of the non-cash figure.
	if !strings.Contains(body, "non_cash") && !strings.Contains(body, "NOT IN") {
		t.Fatal("the report function does not exclude anything from non-cash " +
			"takings, so every method is being counted as money taken")
	}

	for method, why := range notTakings {
		if method == "cash" {
			continue // excluded by being counted separately
		}
		if !strings.Contains(body, "'"+method+"'") {
			t.Errorf("%q (%s) is not excluded from the Z report's non-cash "+
				"takings, so it is being reported as money the shop took",
				method, why)
		}
	}
}
