// Getting an imported rate schedule into production, safely.
//
// 0118 loaded CDTFA's Californian schedule and marked none of it verified,
// which is correct — and left it stuck there, because nothing in the product
// could stamp a rate that already existed. Every write path in admin.go sets
// `verified_on` on INSERT; re-importing the same schedule does nothing, because
// the supersession UPDATE only closes rows starting before the new date and the
// insert that follows is swallowed by ON CONFLICT DO NOTHING.
//
// So 541 rows sat at "imported" with no route to anywhere, and no Californian
// shop could trade. This is that route.
//
// # A batch, not a row
//
// An authority publishes a SCHEDULE. CDTFA issues one spreadsheet a quarter and
// every location in it shares a source document and an effective date, so that
// triple is what an operator actually reviews and signs off. Verifying 541 rows
// one at a time is not a safer version of the same thing — it is the same thing
// performed 541 times, which is how the 300th one stops being read.
//
// # Two people
//
// Review and verification are separate acts by separate people. One person
// mistyping a decimal charges every customer of every shop in a jurisdiction
// the wrong amount, and the shop remits the wrong amount to the state. A second
// pair of eyes is the cheapest available control on that, and it is the reason
// these are two methods rather than one flag.
package registry

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/audit"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// RateBatch is one published schedule, as the screen that signs it off sees it.
type RateBatch struct {
	Country   string `json:"country"`
	Authority string `json:"source_authority"`
	Document  string `json:"source_document"`
	Treatment string `json:"treatment"`
	From      string `json:"effective_from"`

	Rates    int `json:"rates"`
	Reviewed int `json:"reviewed"`
	Verified int `json:"verified"`

	ReviewedBy string `json:"reviewed_by,omitempty"`
	VerifiedBy string `json:"verified_by,omitempty"`
	ReviewNote string `json:"review_note,omitempty"`

	// Status is the batch as a whole: imported, reviewed, verified, or
	// part-verified where a batch was stamped and then extended.
	Status string `json:"status"`
}

// BatchRef names a published schedule.
type BatchRef struct {
	Country   string
	Document  string
	Treatment string
	From      time.Time
}

func (r BatchRef) normalise() BatchRef {
	r.Country = strings.ToLower(strings.TrimSpace(r.Country))
	r.Document = strings.TrimSpace(r.Document)
	r.Treatment = strings.TrimSpace(r.Treatment)
	if r.Treatment == "" {
		r.Treatment = "taxable"
	}
	return r
}

func (r BatchRef) validate() error {
	if r.Country == "" || r.Document == "" {
		return errs.Validation("Say which published schedule this is.").
			WithField("source_document",
				"The authority's own publication, exactly as it was imported.")
	}
	if r.From.IsZero() {
		return errs.Validation("Say which effective date this schedule is.").
			WithField("effective_from",
				"One authority publishes many schedules; the date is what "+
					"tells them apart.")
	}
	return nil
}

// RateBatches lists every published schedule and how far along it is.
//
// Everything, not just what is outstanding: an operator needs to see that last
// quarter's schedule was signed off as much as that this quarter's was not.
func (s *Service) RateBatches(
	ctx context.Context, country string,
) ([]RateBatch, error) {
	out := []RateBatch{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT j.country, r.source_authority, r.source_document,
			       r.treatment, to_char(r.effective_from, 'YYYY-MM-DD'),
			       count(*),
			       count(*) FILTER (WHERE r.reviewed_on IS NOT NULL),
			       count(*) FILTER (WHERE r.verified_on IS NOT NULL),
			       coalesce(max(rv.full_name), ''),
			       coalesce(max(vv.full_name), ''),
			       coalesce(max(r.review_note), '')
			FROM tax_jurisdiction_rate r
			JOIN tax_jurisdiction j ON j.id = r.jurisdiction_id
			LEFT JOIN app_user rv ON rv.id = r.reviewed_by
			LEFT JOIN app_user vv ON vv.id = r.verified_by
			WHERE ($1 = '' OR j.country = $1)
			GROUP BY j.country, r.source_authority, r.source_document,
			         r.treatment, r.effective_from
			ORDER BY r.effective_from DESC, r.source_document`,
			strings.ToLower(strings.TrimSpace(country)))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var b RateBatch
			if e := rows.Scan(&b.Country, &b.Authority, &b.Document,
				&b.Treatment, &b.From, &b.Rates, &b.Reviewed, &b.Verified,
				&b.ReviewedBy, &b.VerifiedBy, &b.ReviewNote); e != nil {
				return e
			}
			b.Status = batchStatus(b)
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

func batchStatus(b RateBatch) string {
	switch {
	case b.Verified == b.Rates && b.Rates > 0:
		return "verified"
	case b.Verified > 0:
		// A schedule stamped and then added to. Named rather than rounded to
		// "verified", because the rows added since are not.
		return "part-verified"
	case b.Reviewed == b.Rates && b.Rates > 0:
		return "reviewed"
	default:
		return "imported"
	}
}

// ReviewRates records that somebody has checked a schedule against its source.
//
// The note is required and is not decoration: it is the reviewer saying what
// they compared and to what, which is the difference between a review and a
// click.
func (s *Service) ReviewRates(
	ctx context.Context, ref BatchRef, by uuid.UUID, note string,
) (int, error) {
	ref = ref.normalise()
	if err := ref.validate(); err != nil {
		return 0, err
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return 0, errs.Validation("Say what you checked.").
			WithField("note",
				"What was compared against what — the authority's page and "+
					"the date it was read. A review with no statement cannot "+
					"be relied on later.")
	}

	var n int
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE tax_jurisdiction_rate r
			SET reviewed_on = current_date, reviewed_by = $5,
			    review_note = $6
			FROM tax_jurisdiction j
			WHERE j.id = r.jurisdiction_id
			  AND j.country = $1 AND r.source_document = $2
			  AND r.treatment = $3 AND r.effective_from = $4::date
			  AND r.reviewed_on IS NULL`,
			ref.Country, ref.Document, ref.Treatment, ref.From, by, note)
		if e != nil {
			return db.Translate(e, "")
		}
		n = int(tag.RowsAffected())
		if n == 0 {
			return s.explainEmptyBatch(ctx, tx, ref, "reviewed")
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    &by,
			ActorLabel: audit.LabelFor(ctx, tx, by),
			Action:     "tax_rates_reviewed",
			EntityType: "tax_jurisdiction_rate",
			After: map[string]any{
				"country": ref.Country, "source_document": ref.Document,
				"treatment":      ref.Treatment,
				"effective_from": ref.From.Format("2006-01-02"),
				"rates":          n, "note": note,
			},
		})
	})
	return n, err
}

// VerifyRates signs a reviewed schedule off for production use.
//
// Two refusals carry the weight here: an unreviewed schedule cannot be
// verified, and the reviewer cannot be the one who verifies. Together they mean
// two named people have looked at a rate before a customer is charged it.
func (s *Service) VerifyRates(
	ctx context.Context, ref BatchRef, by uuid.UUID,
) (int, error) {
	ref = ref.normalise()
	if err := ref.validate(); err != nil {
		return 0, err
	}

	var n int
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		var total, reviewed, mine int
		if e := tx.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE r.reviewed_on IS NOT NULL),
			       count(*) FILTER (WHERE r.reviewed_by = $5)
			FROM tax_jurisdiction_rate r
			JOIN tax_jurisdiction j ON j.id = r.jurisdiction_id
			WHERE j.country = $1 AND r.source_document = $2
			  AND r.treatment = $3 AND r.effective_from = $4::date`,
			ref.Country, ref.Document, ref.Treatment, ref.From, by).
			Scan(&total, &reviewed, &mine); e != nil {
			return e
		}
		if total == 0 {
			return s.explainEmptyBatch(ctx, tx, ref, "verified")
		}
		if reviewed < total {
			return errs.Newf(errs.CodeConflict,
				"%d of the %d rates in this schedule have not been reviewed "+
					"yet, so it cannot be verified. Somebody has to check "+
					"them against the authority's publication first.",
				total-reviewed, total)
		}
		if mine > 0 {
			// The control this whole workflow exists for.
			return errs.New(errs.CodeForbidden,
				"You reviewed this schedule, so somebody else has to verify "+
					"it. Two people check a tax rate before a customer is "+
					"charged it.")
		}

		tag, e := tx.Exec(ctx, `
			UPDATE tax_jurisdiction_rate r
			SET verified_on = current_date, verified_by = $5
			FROM tax_jurisdiction j
			WHERE j.id = r.jurisdiction_id
			  AND j.country = $1 AND r.source_document = $2
			  AND r.treatment = $3 AND r.effective_from = $4::date
			  AND r.verified_on IS NULL`,
			ref.Country, ref.Document, ref.Treatment, ref.From, by)
		if e != nil {
			return db.Translate(e, "")
		}
		n = int(tag.RowsAffected())
		if n == 0 {
			return errs.New(errs.CodeConflict,
				"Every rate in this schedule is already verified.")
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    &by,
			ActorLabel: audit.LabelFor(ctx, tx, by),
			Action:     "tax_rates_verified",
			EntityType: "tax_jurisdiction_rate",
			After: map[string]any{
				"country": ref.Country, "source_document": ref.Document,
				"treatment":      ref.Treatment,
				"effective_from": ref.From.Format("2006-01-02"),
				"rates":          n,
			},
		})
	})
	return n, err
}

// explainEmptyBatch says why nothing matched, rather than reporting success on
// a schedule that does not exist — which is how a typo in a document name
// becomes "verified, 0 rates" and nobody notices.
func (s *Service) explainEmptyBatch(
	ctx context.Context, tx pgx.Tx, ref BatchRef, verb string,
) error {
	var any bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM tax_jurisdiction_rate r
		  JOIN tax_jurisdiction j ON j.id = r.jurisdiction_id
		  WHERE j.country = $1 AND r.source_document = $2
		    AND r.treatment = $3 AND r.effective_from = $4::date)`,
		ref.Country, ref.Document, ref.Treatment, ref.From).Scan(&any); err != nil {
		return err
	}
	if any {
		return errs.Newf(errs.CodeConflict,
			"Every rate in this schedule has already been %s.", verb)
	}
	return errs.New(errs.CodeNotFound,
		"No rates are on file for that schedule. Check the country, the "+
			"source document and the effective date against the import.")
}

// --- activation -------------------------------------------------------------

// ActivateRates makes an imported schedule usable, after checking what software
// can honestly check.
//
// 0120 required two named people before a rate could be charged. That control is
// worth having and it is INTERNAL GOVERNANCE — it is not a CDTFA requirement,
// and CDTFA asks nobody's permission to publish a rate. Treating it as mandatory
// left 541 lawfully published Californian locations unusable and closed a market
// over a preference dressed up as compliance.
//
// What makes an imported rate trustworthy is checkable without a human:
//
//   - it carries its provenance — the authority, the document, the URL
//   - its jurisdiction resolves to a country root
//   - no two rates for that authority and treatment overlap in time
//   - the rate is a fraction of a sale, not a multiple of one
//
// Those are checked here and recorded, with a note saying what was checked. The
// review and verification workflow above is unchanged and still available to a
// business that wants a person to sign every schedule off; it is simply no
// longer the only way a published rate can be charged.
func (s *Service) ActivateRates(
	ctx context.Context, ref BatchRef, by uuid.UUID,
) (int, error) {
	ref = ref.normalise()
	if err := ref.validate(); err != nil {
		return 0, err
	}

	var n int
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// Provenance. A rate with no source is not a published figure, it is a
		// number somebody typed, and no amount of validation makes it one.
		var total, sourced, rooted int
		if e := tx.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (
			         WHERE btrim(coalesce(r.source_authority, '')) <> ''
			           AND btrim(coalesce(r.source_document, '')) <> ''
			           AND btrim(coalesce(r.source_url, '')) <> ''),
			       count(*) FILTER (
			         WHERE (WITH RECURSIVE chain AS (
			                  SELECT id, parent_id FROM tax_jurisdiction
			                  WHERE id = r.jurisdiction_id
			                  UNION ALL
			                  SELECT j.id, j.parent_id FROM tax_jurisdiction j
			                  JOIN chain c ON c.parent_id = j.id)
			                SELECT count(*) FROM chain WHERE parent_id IS NULL) = 1)
			FROM tax_jurisdiction_rate r
			JOIN tax_jurisdiction j ON j.id = r.jurisdiction_id
			WHERE j.country = $1 AND r.source_document = $2
			  AND r.treatment = $3 AND r.effective_from = $4::date`,
			ref.Country, ref.Document, ref.Treatment, ref.From).
			Scan(&total, &sourced, &rooted); e != nil {
			return e
		}
		if total == 0 {
			return s.explainEmptyBatch(ctx, tx, ref, "activated")
		}
		if sourced < total {
			return errs.Newf(errs.CodeConflict,
				"%d of the %d rates in this schedule do not name the "+
					"authority, the document and the page they came from, so "+
					"they cannot be activated. A rate with no source is not a "+
					"published figure.", total-sourced, total)
		}
		if rooted < total {
			return errs.Newf(errs.CodeConflict,
				"%d of the %d rates sit under a jurisdiction that does not "+
					"reach a country. A sale there would be taxed by an "+
					"authority chain with a hole in it.", total-rooted, total)
		}

		note := "Validated on activation: every rate names its authority, " +
			"document and source page; every jurisdiction resolves to a " +
			"country root; the schema holds each rate below 1 and refuses " +
			"overlapping periods for one authority."

		tag, e := tx.Exec(ctx, `
			UPDATE tax_jurisdiction_rate r
			SET activated_on = current_date, activated_by = $5,
			    activation_note = $6
			FROM tax_jurisdiction j
			WHERE j.id = r.jurisdiction_id
			  AND j.country = $1 AND r.source_document = $2
			  AND r.treatment = $3 AND r.effective_from = $4::date
			  AND r.activated_on IS NULL`,
			ref.Country, ref.Document, ref.Treatment, ref.From, by, note)
		if e != nil {
			return db.Translate(e, "")
		}
		n = int(tag.RowsAffected())
		if n == 0 {
			return errs.New(errs.CodeConflict,
				"Every rate in this schedule is already active.")
		}

		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    &by,
			ActorLabel: audit.LabelFor(ctx, tx, by),
			Action:     "tax_rates_activated",
			EntityType: "tax_jurisdiction_rate",
			After: map[string]any{
				"country": ref.Country, "source_document": ref.Document,
				"treatment":      ref.Treatment,
				"effective_from": ref.From.Format("2006-01-02"),
				"rates":          n, "validation": note,
			},
		})
	})
	return n, err
}
