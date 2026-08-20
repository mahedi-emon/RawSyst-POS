package accounting

import (
	"testing"

	"github.com/shopspring/decimal"
)

// A reversing entry is the same rule with the sides swapped. Amounts stay
// positive because a negative debit is not a credit — the journal refuses it.
func TestFlipSidesSwapsDebitsAndCreditsWithoutTouchingAmounts(t *testing.T) {
	ten := decimal.RequireFromString("10.00")
	five := decimal.RequireFromString("5.00")

	got := FlipSides([]Line{
		{Role: "cash", Side: Debit, Amount: ten, Memo: "cash"},
		{Role: "accounts_receivable", Side: Credit, Amount: ten},
		{Role: "bank", Side: Debit, Amount: five},
	})

	if len(got) != 3 {
		t.Fatalf("flipped %d lines, want 3", len(got))
	}
	if got[0].Side != Credit || got[1].Side != Debit || got[2].Side != Credit {
		t.Errorf("sides = %s/%s/%s, want credit/debit/credit",
			got[0].Side, got[1].Side, got[2].Side)
	}
	if !got[0].Amount.Equal(ten) || got[0].Role != "cash" || got[0].Memo != "cash" {
		t.Errorf("the cash line was rewritten rather than flipped: %+v", got[0])
	}
}

func TestFlipSidesTwiceIsTheOriginal(t *testing.T) {
	in := []Line{
		{Role: "cash", Side: Debit, Amount: decimal.RequireFromString("40")},
		{Role: "accounts_receivable", Side: Credit, Amount: decimal.RequireFromString("40")},
	}
	got := FlipSides(FlipSides(in))
	if got[0].Side != Debit || got[1].Side != Credit {
		t.Errorf("flipping twice produced %s/%s, want the original debit/credit",
			got[0].Side, got[1].Side)
	}
}

func TestFlipSidesOfNothingIsNothing(t *testing.T) {
	if got := FlipSides(nil); got == nil || len(got) != 0 {
		t.Errorf("FlipSides(nil) = %#v, want an empty slice", got)
	}
}
