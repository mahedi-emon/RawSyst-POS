// Tax that is not set nationally.
//
// `regulatory_rule` answers "what is the rule for this country on this date",
// which is the right shape for a VAT and the wrong shape for a sales tax. In
// the United States a state, a county, a city and sometimes a special district
// each levy their own share of the same sale, each changes on its own schedule,
// and there is no national rate to fall back on.
//
// So this resolves a rate by walking a jurisdiction to its root and summing the
// shares in force on the day. A Saudi or Bangladeshi sale never comes here.
package registry

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

// JurisdictionShare is one authority's portion of a combined rate.
//
// The combined rate is returned WITH its parts rather than as a single number,
// because remittance is per authority: a shop files with the state and the city
// separately, and a total that cannot be broken down cannot be filed.
type JurisdictionShare struct {
	JurisdictionID uuid.UUID       `json:"jurisdiction_id"`
	Level          string          `json:"level"`
	Code           string          `json:"code"`
	Name           string          `json:"name"`
	Rate           decimal.Decimal `json:"rate"`
}

// CombinedRate is what a sale in one jurisdiction is taxed at, and by whom.
type CombinedRate struct {
	Total  decimal.Decimal     `json:"total"`
	Shares []JurisdictionShare `json:"shares"`
}

// JurisdictionRate resolves the combined rate for a treatment in a jurisdiction
// on a date.
//
// # Every ancestor counts, and every ancestor must have answered
//
// A sale in a city is taxed by the city, its county and its state. The walk
// starts at the named jurisdiction and climbs to the country root, taking each
// authority's rate for the treatment that is in force on the day.
//
// An authority with NO rate row on that date refuses the whole calculation. It
// is tempting to let it contribute nothing — the sum still comes out, the sale
// still rings up — and that is exactly the failure to avoid: a city rate
// recorded and a state rate not yet loaded would charge the customer the city's
// 2% alone, print it on a receipt as the tax due, and post it to the tax
// account. Nobody would see an error, and the shop would be under-remitting to
// the state until an audit found it.
//
// So absence and zero are different facts here, and only one of them is a
// statement. An authority that genuinely levies nothing on a treatment gets an
// explicit 0.000000 row with its source, which somebody has to look up and
// write down; an authority nobody has loaded yet gets a refusal that names it.
//
// # An unverified share refuses the whole rate
//
// Not "skipped", not "treated as zero". A combined rate assembled from three
// verified shares and one unverified one is wrong by exactly the unverified
// share, and would be charged to a customer and remitted as if it were right.
// Where the deployment requires verification, one unverified row refuses the
// calculation and names the authority — the same judgement gate() makes about a
// registry rule.
//
// # A jurisdiction with no rates at all is refused, loudly
//
// This is the state the product is in for every US jurisdiction today: the
// tables exist, and not one rate has been recorded because not one has been
// verified against a state or city authority. The refusal says so rather than
// returning a zero rate, because a zero rate is a legal claim — that this sale
// is not taxed — and it is one nobody has made.
func (s *Service) JurisdictionRate(
	ctx context.Context, tx pgx.Tx, jurisdictionID uuid.UUID, treatment string,
	asOf time.Time,
) (CombinedRate, error) {
	if jurisdictionID == uuid.Nil {
		return CombinedRate{}, errs.New(errs.CodeInvalidInput,
			"A sale taxed by jurisdiction has to say which jurisdiction it is in.")
	}

	run := func(q pgx.Tx) (CombinedRate, error) {
		var out CombinedRate

		// The chain from the named jurisdiction up to its country root, with
		// each authority's rate for this treatment on this date. LEFT JOIN so a
		// level with no rate still appears -- the difference between "this
		// authority levies nothing" and "this authority is missing from the
		// tree" matters when somebody is checking why a total looks low.
		rows, err := q.Query(ctx, `
			WITH RECURSIVE chain AS (
			  SELECT id, parent_id, level, code, name
			  FROM   tax_jurisdiction WHERE id = $1
			  UNION ALL
			  SELECT j.id, j.parent_id, j.level, j.code, j.name
			  FROM   tax_jurisdiction j
			  JOIN   chain c ON c.parent_id = j.id
			)
			SELECT c.id, c.level, c.code, c.name, r.rate, r.verified_on IS NOT NULL
			FROM   chain c
			LEFT JOIN tax_jurisdiction_rate r
			       ON r.jurisdiction_id = c.id
			      AND r.treatment = $2
			      AND r.effective_from <= $3::date
			      AND (r.effective_to IS NULL OR r.effective_to > $3::date)`,
			jurisdictionID, treatment, asOf)
		if err != nil {
			return CombinedRate{}, db.Translate(err, "")
		}
		defer rows.Close()

		total := decimal.Zero
		found := false
		// Authorities in the chain with no rate row for this treatment today.
		var silent []string
		for rows.Next() {
			var sh JurisdictionShare
			var rate *decimal.Decimal
			var verified *bool
			if err := rows.Scan(&sh.JurisdictionID, &sh.Level, &sh.Code, &sh.Name,
				&rate, &verified); err != nil {
				return CombinedRate{}, db.Translate(err, "")
			}
			found = true
			if rate == nil {
				// Named, not counted. The refusal below has to say WHICH
				// authority is unanswered, because "the total looks low" is
				// not something anyone can act on.
				silent = append(silent, fmt.Sprintf("%s (%s)", sh.Name, sh.Level))
				continue
			}
			if s.requireVerified && (verified == nil || !*verified) {
				return CombinedRate{}, errs.Newf(errs.CodeUnverifiedRule,
					"The %s tax rate for %s (%s) has not been verified against "+
						"its authority, so this sale cannot be priced. A "+
						"combined rate is only as sound as its least checked "+
						"part.", treatment, sh.Name, sh.Code)
			}
			sh.Rate = *rate
			total = total.Add(sh.Rate)
			out.Shares = append(out.Shares, sh)
		}
		if err := rows.Err(); err != nil {
			return CombinedRate{}, db.Translate(err, "")
		}

		if !found {
			return CombinedRate{}, errs.New(errs.CodeNotFound,
				"That tax jurisdiction is not on file.")
		}
		if len(out.Shares) == 0 {
			return CombinedRate{}, errs.Newf(errs.CodeUnverifiedRule,
				"No %s tax rate has been recorded for this jurisdiction or any "+
					"authority above it, so a sale here cannot be priced. The "+
					"rates have to be entered with their source before this "+
					"market can trade.", treatment)
		}
		if len(silent) > 0 {
			// The partial case, and the dangerous one: some authorities
			// answered and some did not, so a total exists and is too low.
			return CombinedRate{}, errs.Newf(errs.CodeUnverifiedRule,
				"No %s tax rate is on file for %s on %s, and a sale here is "+
					"taxed by that authority as well as the ones that are on "+
					"file. Pricing it on the rates present would undercharge "+
					"the customer and under-remit. Record the missing rate "+
					"with its source — or record it as 0 if that authority "+
					"levies nothing, which is a statement somebody has to "+
					"make rather than one this can assume.",
				treatment, strings.Join(silent, ", "), asOf.Format("2006-01-02"))
		}

		out.Total = total
		return out, nil
	}

	if tx != nil {
		return run(tx)
	}

	var out CombinedRate
	err := s.pool.TxAsPlatform(ctx, func(t pgx.Tx) error {
		var e error
		out, e = run(t)
		return e
	})
	return out, err
}
