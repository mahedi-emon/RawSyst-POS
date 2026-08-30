package sales

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// An exchange is a return and a sale, and it is exactly that — not a third
// kind of document.
//
// Design 11 §7 draws it as one screen: scan the invoice, pick the lines coming
// back, pick the replacement, settle the difference. That is the cashier's
// view. Underneath, ZATCA sees what it must see: a credit note against the
// original invoice, and a new invoice for the goods leaving the shop. There is
// no "exchange document" because inventing one would mean inventing its
// treatment, and E1 does not have one to invent.
//
// # Why both, and why in one transaction
//
// The alternative — edit the original invoice to swap the line — is the thing
// the architecture exists to prevent. A finalized tax invoice is immutable;
// design 11 §7 spells out the refusal the UI must give and the legal path it
// must offer instead. So the goods coming back are credited and the goods going
// out are sold, and because either one alone leaves the shop's books wrong,
// they go together or not at all.
//
// # How the two settle against each other
//
// A customer swapping a 100 item for a 150 one hands over 50. They do not hand
// over 150 and receive 100 back, and the books must not say they did: the
// drawer would then be expected to hold cash that never moved through it, and
// the blind Z-count at close would show a variance with no cause.
//
// So the offsetting portion — min(credit, replacement) — is settled on both
// documents through `exchange_clearing`, and only the genuine difference moves
// real money. The clearing account rises on the credit note and falls on the
// invoice within the same transaction, netting to exactly zero. A non-zero
// balance on it is therefore a bug with a name, findable by looking at one
// account rather than by reconciling a day of takings.
//
// It is deliberately NOT store_credit. Store credit is a real balance a real
// customer can come back and spend; using it as an internal mechanism would
// inflate every report of credit issued and outstanding, and an owner would be
// told their liability had grown when nothing of the sort had happened.
const ExchangeClearing = "exchange_clearing"

// Exchange is a swap awaiting processing.
type Exchange struct {
	// Return names the original invoice and the lines coming back. Its Refunds
	// are IGNORED and replaced: how an exchange settles is arithmetic the
	// server does, not a figure the till is allowed to assert.
	Return Return

	// Replacement is the sale of the goods going out. Its Tenders are likewise
	// replaced.
	Replacement Sale

	// Settlement is how the DIFFERENCE moves, in whichever direction it goes:
	// what the customer pays when the replacement costs more, or what the shop
	// hands back when it costs less. Empty for an even swap.
	Settlement []Tender
}

// Exchanged is what the till gets back: both documents, and the difference.
type Exchanged struct {
	Refunded  Refunded
	Finalized Finalized

	// CreditApplied is the offsetting portion, settled through the clearing
	// account and moving no money.
	CreditApplied decimal.Decimal

	// Difference is positive when the customer owed more, negative when the
	// shop owed them, zero on an even swap.
	Difference decimal.Decimal

	// AlreadyExchanged is set when this exact exchange has been processed
	// before and the stored outcome is being returned instead of a second one.
	AlreadyExchanged bool
}

// ProcessExchange returns goods and sells their replacement, atomically.
//
// The credit note is written first and takes the earlier position on the EGS
// unit's chain. That ordering is fixed rather than incidental: both documents
// sit on the same terminal's ICV sequence, and a sequence that varied with the
// order the code happened to run in would be reproducible only by accident.
// Goods come back, then goods go out — the same order they cross the counter.
func (s *Service) ProcessExchange(
	ctx context.Context, tx pgx.Tx, term Terminal, ex Exchange,
) (Exchanged, error) {
	// Idempotency covers the WHOLE operation, not each half.
	//
	// A retry that found the credit note already written and went on to sell
	// the replacement a second time would give the customer two jackets for
	// one. Both halves carry device-assigned UUIDs, and both must already be
	// present for this to be a recognised retry; a transaction that got
	// half-way cannot exist, because it would have rolled back.
	if done, found, err := s.alreadyExchanged(ctx, tx, term, ex); err != nil {
		return Exchanged{}, err
	} else if found {
		return done, nil
	}

	credit, err := s.valueComingBack(ctx, tx, ex.Return)
	if err != nil {
		return Exchanged{}, err
	}

	// Priced before either document is written, because the tenders on both
	// depend on the difference between them. Compute is pure and deterministic,
	// so pricing here and again inside Finalize cannot disagree.
	replacement, err := Compute(ex.Replacement.Input)
	if err != nil {
		return Exchanged{}, err
	}

	plan, err := planSettlement(credit, replacement.TotalInclusive, ex.Settlement)
	if err != nil {
		return Exchanged{}, err
	}

	ret := ex.Return
	ret.Refunds = plan.refunds
	refunded, err := s.ProcessReturn(ctx, tx, term, ret)
	if err != nil {
		return Exchanged{}, err
	}

	// The credit note is the authority on what the goods were worth, not the
	// figure computed a moment ago from the same rows. If they disagree the
	// exchange is refused rather than settled against a number that is no
	// longer true — a partial return of a discounted line is exactly where
	// that could happen.
	if !refunded.Computed.TotalInclusive.Equal(credit) {
		return Exchanged{}, errs.Newf(errs.CodeInternal,
			"The credit note came to %s against the %s this exchange was settled on.",
			refunded.Computed.TotalInclusive, credit)
	}

	sale := ex.Replacement
	sale.Tenders = plan.tenders
	finalized, err := s.Finalize(ctx, tx, term, sale)
	if err != nil {
		return Exchanged{}, err
	}

	// The clearing account must net to zero across the pair. It is checked here
	// rather than trusted, because the whole reason for routing an exchange
	// through a named account is to make a mistake in it visible.
	if err := plan.check(); err != nil {
		return Exchanged{}, err
	}

	// And net to zero in the company's own currency, which is where the ledger
	// asserts it rather than where this code arranged it.
	if err := s.sameRate(
		ctx, tx, refunded.CreditNoteID, finalized.InvoiceID); err != nil {
		return Exchanged{}, err
	}

	return Exchanged{
		Refunded: refunded, Finalized: finalized,
		CreditApplied: plan.credit, Difference: plan.difference,
	}, nil
}

// valueComingBack prices the return without writing anything.
func (s *Service) valueComingBack(
	ctx context.Context, tx pgx.Tx, ret Return,
) (decimal.Decimal, error) {
	originals, err := s.returnableLines(ctx, tx, ret.OriginalInvoiceID)
	if err != nil {
		return decimal.Zero, err
	}
	computed, err := ComputeReturn(originals, ret.Requests)
	if err != nil {
		return decimal.Zero, err
	}
	return computed.TotalInclusive, nil
}

// settlement is how the money is arranged across the two documents.
type settlement struct {
	credit     decimal.Decimal
	difference decimal.Decimal

	refunds []Refund
	tenders []Tender
}

// checkMethodsAreOfferable refuses a settlement method the server reserves for
// itself.
//
// ExchangeClearing is not a way of paying. It is the bookkeeping device that
// carries the offsetting half of an exchange from the credit note across to the
// replacement invoice, and planSettlement is the only thing entitled to produce
// it: the two legs are created together, in one transaction, and check() proves
// they cancel.
//
// It has to be refused explicitly, because nothing else refuses it. Migration
// 0030 added 'exchange_clearing' to the method CHECK on both sales_tender and
// sales_refund — it had to, or the exchange's own legs could not be written —
// and that CHECK cannot tell the two documents of an exchange from any other
// sale. tenderRole maps the method to the clearing account for whoever asks. So
// a till that rang up an ordinary SAR 500 sale and tendered it
// 'exchange_clearing' was accepted: revenue recognised, the clearing account
// credited 500.00, and nothing ever debiting it back.
//
// That is the worst shape a defect can take here. No money is involved, so the
// drawer reconciles to the hallala and the shift closes clean; the sale reads as
// ordinary in every report; and the only trace is a permanent balance in the one
// account whose entire purpose is to be empty. exchange_clearing_balance exists
// to report exactly that balance, and it would report it months later with
// nothing left to tie it back to.
//
// It is checked on the way in rather than on the way out because the alternative
// already existed and was not enough: check() does catch a smuggled clearing leg
// on the exchange path, in all three settlement directions, but only after both
// documents have been written, and it reports "this exchange leaves 500.00 in the
// clearing account" — an internal error naming a symptom, when the cause is a
// request that named a method it may not name.
func checkMethodsAreOfferable(methods []string) error {
	for _, m := range methods {
		if m == ExchangeClearing {
			return errs.Newf(errs.CodeInvalidInput,
				"%q is not a way of paying. It is how the two halves of an "+
					"exchange offset each other, and only an exchange can use it.",
				m)
		}
	}
	return nil
}

// tenderMethods and refundMethods name what was offered, for
// checkMethodsAreOfferable.
func tenderMethods(tenders []Tender) []string {
	out := make([]string, len(tenders))
	for i, t := range tenders {
		out[i] = t.Method
	}
	return out
}

func refundMethods(refunds []Refund) []string {
	out := make([]string, len(refunds))
	for i, r := range refunds {
		out[i] = r.Method
	}
	return out
}

// planSettlement works out what settles what.
//
// Three cases, and the arithmetic is the same in all of them: the smaller of
// the two totals offsets through the clearing account, and whatever is left
// over moves as real money in whichever direction it is owed.
func planSettlement(
	credit, replacement decimal.Decimal, offered []Tender,
) (settlement, error) {
	// What the customer hands over for the difference is real money. The
	// clearing legs below are this function's own, and a request that offered
	// one would be asking for the offset to be counted twice.
	if err := checkMethodsAreOfferable(tenderMethods(offered)); err != nil {
		return settlement{}, err
	}
	if credit.IsNegative() || replacement.IsNegative() {
		return settlement{}, errs.New(errs.CodeInvalidInput,
			"An exchange cannot be made against a negative amount.")
	}
	if !credit.IsPositive() {
		return settlement{}, errs.New(errs.CodeInvalidInput,
			"There is nothing coming back, so this is a sale rather than an exchange.")
	}
	if !replacement.IsPositive() {
		return settlement{}, errs.New(errs.CodeInvalidInput,
			"There is nothing going out, so this is a return rather than an exchange.")
	}

	offsetting := decimal.Min(credit, replacement)
	difference := replacement.Sub(credit)

	offeredTotal := decimal.Zero
	for _, t := range offered {
		if !t.Amount.IsPositive() {
			return settlement{}, errs.Newf(errs.CodeInvalidInput,
				"A settlement of %s is not a settlement.", t.Amount)
		}
		offeredTotal = offeredTotal.Add(t.Amount)
	}

	// The difference is stated by the server and must be met exactly. Not "at
	// least": an overpayment is change owed, and treating it as part of the
	// sale overstates takings and the VAT on them.
	if !offeredTotal.Equal(difference.Abs()) {
		return settlement{}, errs.Newf(errs.CodeInvalidInput,
			"This exchange settles at %s, but %s was offered.",
			difference.Abs(), offeredTotal)
	}

	plan := settlement{credit: offsetting, difference: difference}

	// Both documents carry the offsetting portion through the clearing account.
	plan.refunds = []Refund{{Method: ExchangeClearing, Amount: offsetting}}
	plan.tenders = []Tender{{Method: ExchangeClearing, Amount: offsetting}}

	switch {
	case difference.IsPositive():
		// The replacement costs more. The customer pays the difference, and it
		// is a tender on the new sale like any other payment.
		for _, t := range offered {
			plan.tenders = append(plan.tenders, t)
		}
	case difference.IsNegative():
		// The replacement costs less. The shop hands the difference back, and
		// it is a refund on the credit note — which is what puts it through the
		// drawer, and through the day's cash-out, correctly.
		for _, t := range offered {
			plan.refunds = append(plan.refunds, Refund{
				Method: t.Method, Amount: t.Amount, Reference: t.Reference,
			})
		}
	}

	return plan, nil
}

// check proves the clearing account nets to zero.
//
// The invariant the whole mechanism rests on, asserted rather than assumed. If
// this ever fails, an exchange has moved money into an account nobody owns and
// the transaction must not commit.
func (p settlement) check() error {
	in, out := decimal.Zero, decimal.Zero
	for _, r := range p.refunds {
		if r.Method == ExchangeClearing {
			in = in.Add(r.Amount)
		}
	}
	for _, t := range p.tenders {
		if t.Method == ExchangeClearing {
			out = out.Add(t.Amount)
		}
	}
	if !in.Equal(out) {
		return errs.Newf(errs.CodeInternal,
			"This exchange leaves %s in the clearing account.", in.Sub(out))
	}
	return nil
}

// sameRate refuses an exchange whose two halves were valued at different rates.
//
// Both halves are individually right, which is why this refuses rather than
// corrects. The credit note reverses at the rate the original sale was charged
// at — originalInvoice reads currency and fx_rate off the invoice being returned
// against, and TestACreditUsesTheStoredRateNotTodays pins it — because a credit
// has to undo what was actually taken. The replacement is a new sale, so
// applyTaxProfile gives it the rate in force today. The pair can therefore
// differ, and the clearing mechanism has no way to express it.
//
// exchange_clearing_balance (migration 0030) nets the clearing account in BASE
// currency, deliberately: "an exchange settled across two currencies would
// otherwise appear unbalanced when it was not". So the same transaction-currency
// amount posted on both halves at two different rates leaves the difference in
// that account for good. A USD 100 jacket swapped for a USD 100 jacket, sold at
// 3.75 and exchanged at 3.80, strands SAR 5.00 there — and the migration's own
// comment says a row in that account "is a real finding, not noise to be
// filtered".
//
// What is stranded is an exchange gain or loss between the two dates. The
// product has no account, no role and no rule for one, and deciding where it
// belongs is an accounting judgement rather than a coding one, so this refuses
// the exchange with both valuations in the message instead of posting the
// difference somewhere plausible. The shop can still do the same trade as a
// return followed by a separate sale, where each document stands on its own rate
// and nothing has to offset.
//
// Nothing reaches this today: applyTaxProfile sets every sale to the company's
// base currency and no caller sets a rate, so both halves are base at 1. It is
// here because the day a foreign-currency sale becomes possible this stops being
// unreachable, and the failure it prevents is the silent kind — a clearing
// account that drifts by a little at a time and is looked at once it is already
// wrong.
//
// The two documents are read BACK rather than compared from what was computed a
// moment ago, for the reason ProcessExchange gives about the credit note total:
// the rows are what the ledger was built from, and a check against anything else
// is a check on this function's own arithmetic. Both were written in this
// transaction, so both are visible, and refusing here rolls the pair back
// together.
func (s *Service) sameRate(
	ctx context.Context, tx pgx.Tx, creditNoteID, invoiceID uuid.UUID,
) error {
	type valuation struct {
		currency string
		rate     decimal.Decimal
	}
	found := make(map[uuid.UUID]valuation, 2)

	rows, err := tx.Query(ctx, `
		SELECT id, currency, fx_rate FROM sales_invoice WHERE id = ANY($1)`,
		[]uuid.UUID{creditNoteID, invoiceID})
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var v valuation
		if err := rows.Scan(&id, &v.currency, &v.rate); err != nil {
			return err
		}
		found[id] = v
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Two distinct rows, or there is nothing to compare. Missing rows would both
	// read as the empty valuation and compare equal, which would turn this check
	// into a way of passing.
	if len(found) != 2 {
		return errs.New(errs.CodeInternal,
			"An exchange's credit note and replacement invoice could not both be "+
				"read back to compare what they were valued at.")
	}

	credit, replacement := found[creditNoteID], found[invoiceID]
	if credit.currency == replacement.currency &&
		credit.rate.Equal(replacement.rate) {
		return nil
	}
	return errs.Newf(errs.CodeConflict,
		"The goods coming back were sold in %s at a rate of %s and the "+
			"replacement is priced in %s at %s, so the two halves of this "+
			"exchange cannot offset each other exactly. Take the goods back as a "+
			"return and ring the replacement up as a separate sale.",
		credit.currency, credit.rate, replacement.currency, replacement.rate)
}

// alreadyExchanged recognises an exchange the device has sent before.
//
// Both halves must be present. One without the other cannot happen — the
// transaction is atomic — so finding exactly one means something is wrong
// enough to refuse rather than to paper over.
func (s *Service) alreadyExchanged(
	ctx context.Context, tx pgx.Tx, term Terminal, ex Exchange,
) (Exchanged, bool, error) {
	refunded, hasCredit, err := s.alreadyRefunded(ctx, tx, term, ex.Return.CreditNoteUUID)
	if err != nil {
		return Exchanged{}, false, err
	}
	finalized, hasSale, err := s.alreadyRung(ctx, tx, term, ex.Replacement.InvoiceUUID)
	if err != nil {
		return Exchanged{}, false, err
	}

	switch {
	case !hasCredit && !hasSale:
		return Exchanged{}, false, nil
	case hasCredit && hasSale:
		return Exchanged{
			Refunded: refunded, Finalized: finalized,
			CreditApplied: decimal.Min(
				refunded.Computed.TotalInclusive,
				finalized.Computed.TotalInclusive),
			Difference: finalized.Computed.TotalInclusive.
				Sub(refunded.Computed.TotalInclusive),
			AlreadyExchanged: true,
		}, true, nil
	default:
		// Half an exchange on record. Not something a rollback can produce, so
		// it means these two UUIDs have been used for different work — most
		// likely a till reusing one of them.
		return Exchanged{}, false, errs.New(errs.CodeConflict,
			"Part of this exchange has already been recorded against different "+
				"work. It cannot be completed as it stands.")
	}
}

// ExchangeGoods processes an exchange in its own transaction.
func (s *Service) ExchangeGoods(
	ctx context.Context, tenantID, deviceID uuid.UUID,
	ex Exchange, warehouseID uuid.UUID,
) (Exchanged, error) {
	if s.pool == nil {
		return Exchanged{}, errs.New(errs.CodeInternal,
			"The sales service was built without a database connection.")
	}

	var out Exchanged
	err := s.pool.TxAsTenant(ctx, tenantID, func(tx pgx.Tx) error {
		term, profile, e := resolveTerminal(ctx, tx, tenantID, deviceID, warehouseID)
		if e != nil {
			return e
		}
		// The replacement is a sale and gets a sale's treatment: rate and
		// currency from the registry at the transaction date, stock policy from
		// the company. A till does not get to be the authority on any of them
		// merely because the sale arrived inside an exchange.
		if e := s.applyTaxProfile(ctx, tx, &ex.Replacement, profile, tenantID); e != nil {
			return e
		}
		out, e = s.ProcessExchange(ctx, tx, term, ex)
		return e
	})
	return out, err
}
