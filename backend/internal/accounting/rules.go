package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Posting rules live in the database, not in this file.
//
// C9.2 requires each transaction type to have its "own defined, CONFIGURABLE
// posting rule". A rule written in Go means every new transaction type is a
// code release and no tenant can vary its chart-of-accounts mapping — so the
// rule describes the SHAPE of an entry and the transaction supplies the
// numbers, and neither knows the other's business.
//
// The division of labour, which is the whole design:
//
//   - posting_rule says which roles, which side, and which named amount.
//   - account_role_map says which account a role means for THIS company.
//   - The transaction supplies the amounts by name.
//   - The regulatory registry supplies any legal value involved.
//
// No one of those four can be changed by editing another, which is what stops a
// chart of accounts change from becoming a code change.

// Amounts are the named figures a transaction offers a rule.
type Amounts map[string]decimal.Decimal

// Group is a repeating set of lines — the tenders on a sale, the refunds on a
// return — where the count is not known until the transaction happens.
//
// Each member carries its own role because that is the point: a sale paid part
// in cash and part by Mada must debit two different accounts, and E3.1 requires
// Mada never to be merged into a generic "card".
type Group []GroupMember

// GroupMember is one line of a repeating group.
type GroupMember struct {
	Role   string
	Amount decimal.Decimal
	Memo   string
}

// Transaction is everything a rule may draw on.
type Transaction struct {
	Amounts Amounts
	Groups  map[string]Group
}

// ruleLine is one template line as stored in posting_rule.lines.
type ruleLine struct {
	// Role names an account role directly. Empty when ForEach is set.
	Role string `json:"role"`

	// ForEach expands this template into one line per member of a named group.
	ForEach string `json:"for_each"`

	Side   string `json:"side"`
	Amount string `json:"amount"`
}

// Rule is a resolved posting rule.
type Rule struct {
	Key     string
	Version int
	Lines   []ruleLine
}

// ResolveRule finds the rule in force for a key on a date.
//
// Effective-dated at the TRANSACTION date, never at "now". An offline sale that
// syncs a week after a rule changed must post the way it would have posted when
// it was rung up, or two identical sales minutes apart end up in different
// accounts because of when the network came back.
func ResolveRule(
	ctx context.Context, tx pgx.Tx, key, country string, on time.Time,
) (Rule, error) {
	var r Rule
	var raw []byte

	// A country-specific rule wins over the universal one; among equals the
	// highest version wins. Ordering rather than filtering, so a market that
	// needs its own shape can be added without touching the general rule.
	err := tx.QueryRow(ctx, `
		SELECT rule_key, version, lines
		FROM posting_rule
		WHERE rule_key = $1
		  AND (country = $2 OR country IS NULL)
		  AND effective_from <= $3::date
		  AND (effective_to IS NULL OR effective_to > $3::date)
		ORDER BY (country IS NOT NULL) DESC, version DESC
		LIMIT 1`, key, country, on).Scan(&r.Key, &r.Version, &raw)

	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, errs.Newf(errs.CodeConflict,
			"No posting rule named %q was in force on %s, so this transaction "+
				"cannot be recorded in the books.",
			key, on.Format("2 January 2006"))
	}
	if err != nil {
		return Rule{}, err
	}

	if err := json.Unmarshal(raw, &r.Lines); err != nil {
		return Rule{}, errs.Newf(errs.CodeInternal,
			"Posting rule %q version %d is not readable: %v", key, r.Version, err)
	}
	return r, nil
}

// Build turns a rule and a transaction into entry lines.
//
// A named amount the transaction does not supply is an ERROR, never zero. That
// distinction matters more than it looks: a typo in a rule, or a caller that
// forgot a figure, would otherwise post an entry that is silently wrong rather
// than one that fails. The imbalance would usually be caught downstream, but
// "debits do not equal credits" is a far worse message than "this rule wants
// tax_total and nothing supplied it".
func (r Rule) Build(txn Transaction) ([]Line, error) {
	out := make([]Line, 0, len(r.Lines))

	for i, tmpl := range r.Lines {
		side, err := parseSide(tmpl.Side, r.Key, i)
		if err != nil {
			return nil, err
		}

		if tmpl.ForEach != "" {
			members, ok := txn.Groups[tmpl.ForEach]
			if !ok {
				return nil, errs.Newf(errs.CodeInternal,
					"Posting rule %q expects a group called %q and this "+
						"transaction supplied none.", r.Key, tmpl.ForEach)
			}
			for _, m := range members {
				if m.Role == "" {
					return nil, errs.Newf(errs.CodeInternal,
						"A %q line in rule %q has no account role.",
						tmpl.ForEach, r.Key)
				}
				out = append(out, Line{
					Role: m.Role, Side: side, Amount: m.Amount, Memo: m.Memo,
				})
			}
			continue
		}

		if tmpl.Role == "" {
			return nil, errs.Newf(errs.CodeInternal,
				"Line %d of posting rule %q names no account role.", i+1, r.Key)
		}
		amount, ok := txn.Amounts[tmpl.Amount]
		if !ok {
			return nil, errs.Newf(errs.CodeInternal,
				"Posting rule %q wants an amount called %q and this transaction "+
					"supplied none.", r.Key, tmpl.Amount)
		}
		out = append(out, Line{Role: tmpl.Role, Side: side, Amount: amount})
	}

	return out, nil
}

func parseSide(s, key string, index int) (Side, error) {
	switch Side(s) {
	case Debit:
		return Debit, nil
	case Credit:
		return Credit, nil
	default:
		return "", errs.Newf(errs.CodeInternal,
			"Line %d of posting rule %q says side %q, which is neither debit "+
				"nor credit.", index+1, key, s)
	}
}

// PostByRule resolves a rule, builds its lines and posts the entry.
//
// This is the one route a module should use. Constructing Lines by hand still
// works — the sync worker replaying a historical entry needs it — but a module
// that hand-writes its posting shape has put the rule back into code, which is
// the thing C9.2 forbids.
func PostByRule(
	ctx context.Context, tx pgx.Tx, e Entry, country string, txn Transaction,
) (Result, error) {
	if e.RuleKey == "" {
		return Result{}, errs.New(errs.CodeInternal,
			"An entry posted by rule must name the rule.")
	}

	rule, err := ResolveRule(ctx, tx, e.RuleKey, country, e.Date)
	if err != nil {
		return Result{}, err
	}

	lines, err := rule.Build(txn)
	if err != nil {
		return Result{}, err
	}

	e.Lines = lines
	e.RuleVersion = rule.Version
	return Post(ctx, tx, e)
}
