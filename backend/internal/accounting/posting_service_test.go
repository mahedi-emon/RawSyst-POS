//go:build integration

// The posting service, as opposed to the database constraints tested in
// posting_test.go. Those prove nothing can write an unbalanced entry by any
// route; these prove the one route every module actually uses does the right
// thing — allocates a gapless number, refuses a closed period with a sentence a
// person can act on, and posts a replayed sale exactly once.
package accounting

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func aug(day int) time.Time {
	return time.Date(2026, 8, day, 12, 0, 0, 0, time.UTC)
}

// mapRoles gives the company a chart of accounts a posting rule can name.
func (b *books) mapRoles(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	err := b.pool.TxAsTenant(ctx, b.tenantID, func(tx pgx.Tx) error {
		for role, id := range map[string]uuid.UUID{
			"cash": b.cash, "sales_revenue": b.revenue, "output_vat": b.outputVAT,
		} {
			if _, e := tx.Exec(ctx, `
				INSERT INTO account_role_map (tenant_id, company_id, role, account_id)
				VALUES ($1,$2,$3,$4)`, b.tenantID, b.companyID, role, id); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("map account roles: %v", err)
	}
}

func (b *books) postEntry(t *testing.T, e Entry) (Result, error) {
	t.Helper()
	var res Result
	err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		var e2 error
		res, e2 = Post(context.Background(), tx, e)
		return e2
	})
	return res, err
}

// A cash sale: the shape every till produces.
func (b *books) cashSale(id uuid.UUID, total, net, vat string) Entry {
	return Entry{
		TenantID: b.tenantID, CompanyID: b.companyID, Date: aug(15),
		SourceType: "sale", SourceID: id, RuleKey: "sale.cash",
		Currency: "SAR", BaseCurrency: "SAR",
		Lines: []Line{
			{Role: "cash", Side: Debit, Amount: decimal.RequireFromString(total)},
			{Role: "sales_revenue", Side: Credit, Amount: decimal.RequireFromString(net)},
			{Role: "output_vat", Side: Credit, Amount: decimal.RequireFromString(vat)},
		},
	}
}

func TestPostWritesABalancedEntry(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	got, err := b.postEntry(t, b.cashSale(uuid.New(), "115.00", "100.00", "15.00"))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got.AlreadyPosted {
		t.Error("a first posting reported itself as a replay")
	}
	if got.EntryNo != 1 {
		t.Errorf("entry number = %d, want 1", got.EntryNo)
	}

	var debits, credits decimal.Decimal
	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT coalesce(sum(base_debit),0), coalesce(sum(base_credit),0)
			FROM journal_line WHERE entry_id = $1`, got.EntryID).Scan(&debits, &credits)
	}); err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if !debits.Equal(decimal.RequireFromString("115")) {
		t.Errorf("debits = %s, want 115", debits)
	}
	if !debits.Equal(credits) {
		t.Errorf("the posted entry does not balance: %s against %s", debits, credits)
	}
}

// Pillar 3 depends on this. Sync delivers at least once, so the same sale
// arrives more than once, and the books must say the same thing either way.
func TestPostingTheSameSaleTwiceChangesNothing(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)
	saleID := uuid.New()

	first, err := b.postEntry(t, b.cashSale(saleID, "115.00", "100.00", "15.00"))
	if err != nil {
		t.Fatalf("first post: %v", err)
	}
	second, err := b.postEntry(t, b.cashSale(saleID, "115.00", "100.00", "15.00"))
	if err != nil {
		t.Fatalf("replay was rejected instead of recognised: %v", err)
	}

	if !second.AlreadyPosted {
		t.Error("a replayed sale was not recognised as already posted")
	}
	if second.EntryID != first.EntryID {
		t.Errorf("the replay produced a different entry: %s then %s",
			first.EntryID, second.EntryID)
	}

	var entries, lines int
	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(context.Background(),
			`SELECT count(*) FROM journal_entry WHERE source_id = $1`, saleID).
			Scan(&entries); e != nil {
			return e
		}
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM journal_line WHERE entry_id = $1`, first.EntryID).
			Scan(&lines)
	}); err != nil {
		t.Fatalf("count: %v", err)
	}
	if entries != 1 {
		t.Errorf("%d entries for one sale; the revenue is double counted", entries)
	}
	if lines != 3 {
		t.Errorf("%d lines on the entry, want 3", lines)
	}
}

// A replay must not burn a number either. Claiming one and then discovering the
// conflict would leave a permanent gap in the journal, and a gap in numbered
// accounting records is what an auditor asks about.
func TestAReplayDoesNotBurnAnEntryNumber(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)
	saleID := uuid.New()

	if _, err := b.postEntry(t, b.cashSale(saleID, "115.00", "100.00", "15.00")); err != nil {
		t.Fatalf("first post: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := b.postEntry(t, b.cashSale(saleID, "115.00", "100.00", "15.00")); err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}

	next, err := b.postEntry(t, b.cashSale(uuid.New(), "230.00", "200.00", "30.00"))
	if err != nil {
		t.Fatalf("post after replays: %v", err)
	}
	if next.EntryNo != 2 {
		t.Errorf("the next entry is number %d, want 2; three replays burned %d "+
			"numbers and left a gap in the journal", next.EntryNo, next.EntryNo-2)
	}
}

// Two tills committing at the same instant must not claim the same number.
func TestConcurrentPostsGetDistinctGaplessNumbers(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	const n = 12
	var wg sync.WaitGroup
	numbers := make([]int64, n)
	errsSeen := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := b.postEntry(t, b.cashSale(uuid.New(), "115.00", "100.00", "15.00"))
			numbers[i], errsSeen[i] = res.EntryNo, err
		}(i)
	}
	wg.Wait()

	seen := map[int64]bool{}
	for i, err := range errsSeen {
		if err != nil {
			t.Fatalf("concurrent post %d failed: %v", i, err)
		}
		if seen[numbers[i]] {
			t.Fatalf("entry number %d was issued twice", numbers[i])
		}
		seen[numbers[i]] = true
	}
	for want := int64(1); want <= n; want++ {
		if !seen[want] {
			t.Errorf("entry number %d was never issued; the journal has a gap", want)
		}
	}
}

// Refused before the database sees it, with the difference stated. The trigger
// is the guarantee; this is the message a developer needs at three in the
// morning.
func TestUnbalancedEntryIsRefusedWithTheDifference(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	e := b.cashSale(uuid.New(), "115.00", "100.00", "10.00") // 5 short
	_, err := b.postEntry(t, e)
	if err == nil {
		t.Fatal("an unbalanced entry was posted")
	}
	if !strings.Contains(err.Error(), "5") {
		t.Errorf("the refusal does not say by how much: %v", err)
	}
}

// A period that is closed refuses the entry in words a person can act on.
func TestPostingIntoAClosedPeriodSaysWhatToDo(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		_, e := tx.Exec(context.Background(),
			`UPDATE fiscal_period SET state = 'closed' WHERE id = $1`, b.periodID)
		return e
	}); err != nil {
		t.Fatalf("close the period: %v", err)
	}

	_, err := b.postEntry(t, b.cashSale(uuid.New(), "115.00", "100.00", "15.00"))
	if err == nil {
		t.Fatal("an entry was posted into a closed period")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("the refusal does not say the period is closed: %v", err)
	}
}

// A date no period covers is a setup problem, and saying so beats a foreign key
// error naming a column.
func TestPostingOutsideAnyPeriodIsExplained(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	e := b.cashSale(uuid.New(), "115.00", "100.00", "15.00")
	e.Date = time.Date(2027, 3, 4, 12, 0, 0, 0, time.UTC)

	_, err := b.postEntry(t, e)
	if err == nil {
		t.Fatal("an entry was posted into no period at all")
	}
	if !strings.Contains(err.Error(), "period") {
		t.Errorf("the refusal does not mention the period: %v", err)
	}
}

// An unmapped role names itself, because whoever configures the chart of
// accounts needs to know which one is missing.
func TestAnUnmappedAccountRoleNamesItself(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	e := b.cashSale(uuid.New(), "115.00", "100.00", "15.00")
	e.Lines[0].Role = "petty_cash"

	_, err := b.postEntry(t, e)
	if err == nil {
		t.Fatal("an entry posted to a role this company has never mapped")
	}
	if !strings.Contains(err.Error(), "petty_cash") {
		t.Errorf("the refusal does not name the missing role: %v", err)
	}
}

// Zero lines are dropped, not refused. A rule with a discount leg produces one
// on every sale, discounted or not, and making each caller filter them puts the
// same filtering in every module.
func TestZeroAmountLinesAreDropped(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	e := b.cashSale(uuid.New(), "115.00", "100.00", "15.00")
	e.Lines = append(e.Lines, Line{
		Role: "sales_revenue", Side: Credit, Amount: decimal.Zero,
	})

	got, err := b.postEntry(t, e)
	if err != nil {
		t.Fatalf("a zero line broke the posting: %v", err)
	}

	var lines int
	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM journal_line WHERE entry_id = $1`, got.EntryID).
			Scan(&lines)
	}); err != nil {
		t.Fatalf("count lines: %v", err)
	}
	if lines != 3 {
		t.Errorf("%d lines written, want 3; the zero line was not dropped", lines)
	}
}

// A negative amount is refused rather than silently flipped. Writing a credit
// as a negative debit makes every report that sums a column wrong, and it would
// pass the balance check.
func TestNegativeAmountsAreRefused(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	e := b.cashSale(uuid.New(), "115.00", "100.00", "15.00")
	e.Lines[1].Amount = decimal.RequireFromString("-100.00")

	if _, err := b.postEntry(t, e); err == nil {
		t.Fatal("a negative line amount was accepted")
	}
}

// A foreign-currency entry must balance in the BASE currency too. Converting
// each line independently rounds many times and the two sides need not agree —
// an entry perfectly balanced in dollars would fail to balance in riyals for no
// reason anyone could explain from the invoice.
func TestForeignCurrencyBalancesInBaseCurrencyDespiteRounding(t *testing.T) {
	b := newBooks(t)
	b.mapRoles(t)

	// A rate chosen so each line converts to a recurring amount.
	e := Entry{
		TenantID: b.tenantID, CompanyID: b.companyID, Date: aug(15),
		SourceType: "sale", SourceID: uuid.New(), RuleKey: "sale.cash",
		Currency: "USD", BaseCurrency: "SAR",
		FXRate: decimal.RequireFromString("3.7512345"),
		Lines: []Line{
			{Role: "cash", Side: Debit, Amount: decimal.RequireFromString("100.03")},
			{Role: "sales_revenue", Side: Credit, Amount: decimal.RequireFromString("87.01")},
			{Role: "output_vat", Side: Credit, Amount: decimal.RequireFromString("13.02")},
		},
	}

	got, err := b.postEntry(t, e)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}

	var baseDebits, baseCredits, debits, credits decimal.Decimal
	if err := b.pool.TxAsTenant(context.Background(), b.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `
			SELECT coalesce(sum(base_debit),0), coalesce(sum(base_credit),0),
			       coalesce(sum(debit),0),      coalesce(sum(credit),0)
			FROM journal_line WHERE entry_id = $1`, got.EntryID).
			Scan(&baseDebits, &baseCredits, &debits, &credits)
	}); err != nil {
		t.Fatalf("read entry: %v", err)
	}

	if !debits.Equal(credits) {
		t.Errorf("transaction currency is out: %s against %s", debits, credits)
	}
	if !baseDebits.Equal(baseCredits) {
		t.Errorf("base currency is out by %s; the trial balance would not balance",
			baseDebits.Sub(baseCredits))
	}
}

// QA gate M8 at the posting layer: one tenant cannot post into another's books.
func TestPostingIntoAnotherTenantsCompanyIsRefused(t *testing.T) {
	mine := newBooks(t)
	theirs := newBooks(t)
	mine.mapRoles(t)
	theirs.mapRoles(t)

	e := mine.cashSale(uuid.New(), "115.00", "100.00", "15.00")
	e.CompanyID = theirs.companyID

	if _, err := mine.postEntry(t, e); err == nil {
		t.Fatal("one tenant posted an entry into another tenant's books")
	}

	var count int
	if err := theirs.pool.TxAsTenant(context.Background(), theirs.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM journal_entry WHERE company_id = $1`,
			theirs.companyID).Scan(&count)
	}); err != nil {
		t.Fatalf("count their entries: %v", err)
	}
	if count != 0 {
		t.Fatalf("their books now hold %d entries they did not post", count)
	}
}
