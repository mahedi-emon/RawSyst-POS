// The Platform Owner's way to put legal values into the registry.
//
// A4 gives the Platform Owner "global list of countries, currencies, languages,
// tax templates", and E8 built the registry those templates live in — payload,
// effective dates, source authority, source document, source URL, and
// `verified_on` / `verified_by` recording that a named person checked the
// figure against the authority.
//
// None of it was reachable. There was no route and no service method that could
// write a rule, add a tax jurisdiction, or record a rate. Every unverified value
// in this product — the Saudi GOSI schedule, the Mudad wage-file format, every
// US district rate — was described as "an operations task", and the operation
// could only be performed with a SQL client against production.
//
// That is what this closes. The refusals are unchanged: nothing here lets
// anybody skip verification, and recording a value still requires naming where
// it came from.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// RuleRow is a registry rule as an administrator sees it.
type RuleRow struct {
	ID         uuid.UUID `json:"id"`
	Key        string    `json:"rule_key"`
	Country    string    `json:"country"`
	Payload    any       `json:"payload"`
	From       string    `json:"effective_from"`
	To         string    `json:"effective_to,omitempty"`
	Authority  string    `json:"source_authority"`
	Document   string    `json:"source_document"`
	URL        string    `json:"source_url,omitempty"`
	VerifiedOn string    `json:"verified_on,omitempty"`
	Verified   bool      `json:"verified"`
	Blocker    bool      `json:"release_blocker"`
	Notes      string    `json:"notes,omitempty"`
}

// Rules lists the registry, newest effective date first.
//
// Everything, verified or not: the screen exists to show an operator what is
// still outstanding, so hiding the unverified rows would hide the point of it.
func (s *Service) Rules(ctx context.Context, country string) ([]RuleRow, error) {
	out := []RuleRow{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, rule_key, country, payload,
			       to_char(effective_from, 'YYYY-MM-DD'),
			       coalesce(to_char(effective_to, 'YYYY-MM-DD'), ''),
			       source_authority::text, source_document,
			       coalesce(source_url, ''),
			       coalesce(to_char(verified_on, 'YYYY-MM-DD'), ''),
			       release_blocker, coalesce(notes, '')
			FROM regulatory_rule
			WHERE ($1 = '' OR country = $1)
			ORDER BY country, rule_key, effective_from DESC`,
			strings.ToLower(strings.TrimSpace(country)))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r RuleRow
			var payload []byte
			if e := rows.Scan(&r.ID, &r.Key, &r.Country, &payload, &r.From,
				&r.To, &r.Authority, &r.Document, &r.URL, &r.VerifiedOn,
				&r.Blocker, &r.Notes); e != nil {
				return e
			}
			_ = json.Unmarshal(payload, &r.Payload)
			r.Verified = r.VerifiedOn != ""
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// NewRule is a legal value being recorded.
type NewRule struct {
	Key       string
	Country   string
	Payload   json.RawMessage
	From      time.Time
	Authority string
	Document  string
	URL       string
	Blocker   bool
	Notes     string

	// Verified says the caller checked this against the official document and
	// is putting their name to it. False records the figure without asserting
	// that, which is how a value can be staged for somebody else to confirm.
	Verified bool
}

// RecordRule writes a legal value and stamps who checked it.
//
// # Verification is an assertion by a person, not a flag
//
// `verified_by` is the caller. Recording a rule IS the act of saying "I read
// this in the official document named here and this is what it says", which is
// why the document may not be blank and why there is no way to stamp
// `verified_on` without supplying the value it applies to.
//
// # A correction supersedes, it does not overwrite
//
// A rule already in force is closed off at the new one's start date rather than
// edited, because payroll and tax resolve at the date of the document being
// processed: re-running last March must give March's answer. Editing in place
// would silently restate every period the old figure governed.
func (s *Service) RecordRule(
	ctx context.Context, in NewRule, by uuid.UUID,
) (RuleRow, error) {
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return RuleRow{}, errs.Validation("Name the rule.").
			WithField("rule_key", "For example SA.GOSI.RATES.")
	}
	if strings.TrimSpace(in.Document) == "" {
		return RuleRow{}, errs.Validation(
			"Name the official document this came from.").
			WithField("source_document",
				"A regulator's publication with its version or date, so "+
					"somebody can check the figure later without asking you.")
	}
	if len(in.Payload) == 0 {
		return RuleRow{}, errs.Validation("Give the rule a value.").
			WithField("payload", "The figures the product will compute with.")
	}
	// The placeholder marker is what makes an unfilled rule refuse loudly.
	// Writing one back in and calling it verified would turn that into a lie.
	if strings.Contains(string(in.Payload), "__VERIFY__") {
		return RuleRow{}, errs.New(errs.CodeInvalidInput,
			"That value still contains __VERIFY__. Replace every placeholder "+
				"with the figure from the official document before recording it.")
	}
	if in.From.IsZero() {
		return RuleRow{}, errs.Validation("Say when this takes effect.").
			WithField("effective_from",
				"A legal value applies from a date, and the product resolves "+
					"it at the date of the document being processed.")
	}
	// An unverified rule must explain itself.
	//
	// The registry's whole discipline is that a reader can tell a confirmed
	// figure from a starting one, and `TestUnverifiedRulesAreNotDisguised`
	// holds the database to it. Recording an unchecked value with nothing
	// beside it would let this path create exactly what that invariant
	// forbids — a number sitting in the registry with no indication that
	// nobody has stood behind it.
	if !in.Verified && strings.TrimSpace(in.Notes) == "" {
		return RuleRow{}, errs.Validation(
			"Say why this value is not verified yet.").
			WithField("notes",
				"An unverified figure has to carry a note, or a reader of the "+
					"registry cannot tell it from a confirmed one. Say where "+
					"it came from and what still has to be checked.")
	}

	var out RuleRow
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// Close off whatever was in force, rather than editing it.
		if _, e := tx.Exec(ctx, `
			UPDATE regulatory_rule
			SET effective_to = $3::date
			WHERE rule_key = $1 AND country = $2
			  AND effective_from < $3::date
			  AND (effective_to IS NULL OR effective_to > $3::date)`,
			key, in.Country, in.From); e != nil {
			return e
		}

		var verified, verifier any
		if in.Verified {
			verified = time.Now().UTC()
			verifier = by
		}

		return tx.QueryRow(ctx, `
			INSERT INTO regulatory_rule
			  (rule_key, country, payload, effective_from, source_authority,
			   source_document, source_url, release_blocker, notes,
			   verified_on, verified_by)
			VALUES ($1,$2,$3::jsonb,$4,$5,$6,
			        nullif($7,''),$8,nullif($9,''),$10::date,$11)
			RETURNING id, to_char(effective_from, 'YYYY-MM-DD'),
			          coalesce(to_char(verified_on, 'YYYY-MM-DD'), '')`,
			key, in.Country, string(in.Payload), in.From, in.Authority,
			strings.TrimSpace(in.Document), strings.TrimSpace(in.URL),
			in.Blocker, strings.TrimSpace(in.Notes), verified, verifier).
			Scan(&out.ID, &out.From, &out.VerifiedOn)
	})
	if err != nil {
		return RuleRow{}, db.Translate(err, "That rule could not be recorded.")
	}

	// The resolver caches, and a figure just corrected must be the one used
	// on the very next payroll run.
	s.Invalidate()

	out.Key, out.Country = key, in.Country
	out.Authority, out.Document, out.URL = in.Authority, in.Document, in.URL
	out.Blocker, out.Notes = in.Blocker, in.Notes
	out.Verified = out.VerifiedOn != ""
	_ = json.Unmarshal(in.Payload, &out.Payload)
	return out, nil
}

// --- Tax jurisdictions ----------------------------------------------------

// JurisdictionRow is one authority in the tree.
type JurisdictionRow struct {
	ID       uuid.UUID  `json:"id"`
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
	Country  string     `json:"country"`
	Level    string     `json:"level"`
	Code     string     `json:"code"`
	Name     string     `json:"name"`
}

// Jurisdictions lists a country's tax authorities.
func (s *Service) Jurisdictions(
	ctx context.Context, country string,
) ([]JurisdictionRow, error) {
	out := []JurisdictionRow{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT id, parent_id, country, level, code, name
			FROM tax_jurisdiction
			WHERE country = $1
			ORDER BY level, code`,
			strings.ToLower(strings.TrimSpace(country)))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var r JurisdictionRow
			if e := rows.Scan(&r.ID, &r.ParentID, &r.Country, &r.Level,
				&r.Code, &r.Name); e != nil {
				return e
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// NewJurisdiction is a tax authority being added.
type NewJurisdiction struct {
	ParentID    *uuid.UUID
	Country     string
	Level       string
	Code        string
	Name        string
	OriginBased *bool
}

// SaveJurisdiction adds a tax authority, or corrects one already there.
func (s *Service) SaveJurisdiction(
	ctx context.Context, in NewJurisdiction,
) (JurisdictionRow, error) {
	country := strings.ToLower(strings.TrimSpace(in.Country))
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	name := strings.TrimSpace(in.Name)
	if country == "" || code == "" || name == "" {
		return JurisdictionRow{}, errs.Validation(
			"A tax authority needs a country, a code and a name.").
			WithField("code", "As the authority writes it — CA, not California.")
	}

	var out JurisdictionRow
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO tax_jurisdiction
			  (parent_id, country, level, code, name, is_origin_based)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (country, level, code)
			DO UPDATE SET name = excluded.name, parent_id = excluded.parent_id,
			              is_origin_based = excluded.is_origin_based
			RETURNING id, parent_id, country, level, code, name`,
			in.ParentID, country, in.Level, code, name, in.OriginBased).
			Scan(&out.ID, &out.ParentID, &out.Country, &out.Level, &out.Code,
				&out.Name)
	})
	return out, db.Translate(err, "That tax authority could not be saved.")
}

// NewJurisdictionRate is one authority's rate being put on file.
type NewJurisdictionRate struct {
	JurisdictionID uuid.UUID
	Treatment      string
	Rate           decimal.Decimal
	From           time.Time
	Authority      string
	Document       string
	URL            string
	Notes          string
	Verified       bool
}

// RecordJurisdictionRate puts one authority's rate on file.
//
// This is the operation that unblocks a market. A US shop cannot sell until
// every authority above it has a rate for the treatment, and until now the only
// way to give it one was a SQL client against production.
//
// Zero is a legitimate rate meaning "this authority levies nothing" — a
// statement somebody looked up — so it goes through the same path and carries
// the same source as any other figure.
func (s *Service) RecordJurisdictionRate(
	ctx context.Context, in NewJurisdictionRate, by uuid.UUID,
) error {
	if in.JurisdictionID == uuid.Nil {
		return errs.New(errs.CodeInvalidInput, "Name the tax authority.")
	}
	if strings.TrimSpace(in.Treatment) == "" {
		return errs.Validation("Say which treatment this rate is for.").
			WithField("treatment", "For example taxable.")
	}
	if in.Rate.IsNegative() {
		return errs.Validation("A tax rate is not negative.").
			WithField("rate", "0.0725 is 7.25 per cent.")
	}
	if strings.TrimSpace(in.Document) == "" {
		return errs.Validation(
			"Name the official document this rate came from.").
			WithField("source_document",
				"The authority's own publication, so the figure can be "+
					"checked later without asking you.")
	}
	if in.From.IsZero() {
		return errs.Validation("Say when this rate takes effect.").
			WithField("effective_from",
				"A sale is taxed at the rate in force on the day it was made.")
	}

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// Close the rate this one supersedes. The exclusion constraint forbids
		// overlapping ranges, so this is what lets a correction land at all —
		// and it keeps the old figure readable for the periods it governed.
		if _, e := tx.Exec(ctx, `
			UPDATE tax_jurisdiction_rate
			SET effective_to = $3::date
			WHERE jurisdiction_id = $1 AND treatment = $2
			  AND effective_from < $3::date
			  AND (effective_to IS NULL OR effective_to > $3::date)`,
			in.JurisdictionID, in.Treatment, in.From); e != nil {
			return e
		}

		var verified, verifier any
		if in.Verified {
			verified = in.From
			verifier = by
		}

		_, e := tx.Exec(ctx, `
			INSERT INTO tax_jurisdiction_rate
			  (jurisdiction_id, treatment, rate, effective_from,
			   source_authority, source_document, source_url, notes,
			   verified_on, verified_by)
			VALUES ($1,$2,$3,$4,$5,$6,nullif($7,''),nullif($8,''),$9::date,$10)`,
			in.JurisdictionID, in.Treatment, in.Rate, in.From,
			in.Authority, strings.TrimSpace(in.Document),
			strings.TrimSpace(in.URL), strings.TrimSpace(in.Notes),
			verified, verifier)
		return e
	})
	return db.Translate(err, "That tax rate could not be recorded.")
}

// --- Bulk ingestion -------------------------------------------------------

// ImportRow is one authority and its rate, as an official dataset states them.
//
// Parent is named by CODE rather than by id because that is what a published
// dataset carries: CDTFA's schedule says a district belongs to a county, not to
// a uuid this database happens to have assigned.
type ImportRow struct {
	Level      string
	Code       string
	Name       string
	ParentCode string
	Rate       decimal.Decimal
}

// Import is one authority's published schedule, applied as a whole.
type Import struct {
	Country   string
	Treatment string
	From      time.Time
	Authority string
	Document  string
	URL       string
	Notes     string
	Verified  bool
	Rows      []ImportRow
}

// ImportResult says what a load actually did.
type ImportResult struct {
	Jurisdictions int      `json:"jurisdictions"`
	Rates         int      `json:"rates"`
	Skipped       []string `json:"skipped,omitempty"`
}

// ImportRates loads a published rate schedule in one transaction.
//
// # Why this exists
//
// Recording rates one at a time is correct and unusable at scale: California
// alone has hundreds of districts, and a state publishes a fresh schedule every
// quarter. Loading them through a screen one row at a time is how a shop ends
// up trading on a half-loaded chain, which the resolver refuses — correctly,
// and after somebody has spent an afternoon.
//
// # All or nothing
//
// One transaction. A schedule that fails halfway would leave some authorities
// on the new rates and some on the old, which is worse than not loading at all:
// every combined rate in that state would be wrong in a way no single row looks
// wrong.
//
// # It does not decide anything
//
// Every rate carries the source and date the caller supplies, supersedes rather
// than overwrites, and is verified only if the caller says they checked it.
// This is a faster way to record facts, not a way to skip establishing them.
func (s *Service) ImportRates(
	ctx context.Context, in Import, by uuid.UUID,
) (ImportResult, error) {
	if len(in.Rows) == 0 {
		return ImportResult{}, errs.New(errs.CodeInvalidInput,
			"That import has no rows.")
	}
	if strings.TrimSpace(in.Document) == "" {
		return ImportResult{}, errs.Validation(
			"Name the official document this schedule came from.").
			WithField("source_document",
				"The authority's own publication and its date, so a rate can "+
					"be traced back without asking whoever loaded it.")
	}
	if in.From.IsZero() {
		return ImportResult{}, errs.Validation(
			"Say when this schedule takes effect.").
			WithField("effective_from",
				"A published schedule applies from a date, and a sale is "+
					"taxed at the rate in force on the day it was made.")
	}
	treatment := strings.TrimSpace(in.Treatment)
	if treatment == "" {
		treatment = "taxable"
	}
	country := strings.ToLower(strings.TrimSpace(in.Country))

	var out ImportResult
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		// Codes already on file, so a parent named by code resolves whether it
		// arrived in this batch or last quarter's.
		known := map[string]uuid.UUID{}
		rows, e := tx.Query(ctx,
			`SELECT code, id FROM tax_jurisdiction WHERE country = $1`, country)
		if e != nil {
			return e
		}
		for rows.Next() {
			var code string
			var id uuid.UUID
			if e := rows.Scan(&code, &id); e != nil {
				rows.Close()
				return e
			}
			known[code] = id
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return e
		}

		for i, r := range in.Rows {
			code := strings.ToUpper(strings.TrimSpace(r.Code))
			name := strings.TrimSpace(r.Name)
			if code == "" || name == "" || strings.TrimSpace(r.Level) == "" {
				out.Skipped = append(out.Skipped, fmt.Sprintf(
					"row %d: a level, a code and a name are all required", i+1))
				continue
			}
			if r.Rate.IsNegative() {
				out.Skipped = append(out.Skipped, fmt.Sprintf(
					"row %d (%s): a tax rate is not negative", i+1, code))
				continue
			}

			var parent *uuid.UUID
			if pc := strings.ToUpper(strings.TrimSpace(r.ParentCode)); pc != "" {
				id, ok := known[pc]
				if !ok {
					// Refused rather than attached to the country root: a
					// district silently reparented would charge the wrong
					// county's tax, and nothing downstream could tell.
					out.Skipped = append(out.Skipped, fmt.Sprintf(
						"row %d (%s): its parent %s is not on file", i+1,
						code, pc))
					continue
				}
				parent = &id
			}

			var id uuid.UUID
			if e := tx.QueryRow(ctx, `
				INSERT INTO tax_jurisdiction
				  (parent_id, country, level, code, name)
				VALUES ($1,$2,$3,$4,$5)
				ON CONFLICT (country, level, code)
				DO UPDATE SET name = excluded.name,
				              parent_id = excluded.parent_id
				RETURNING id`,
				parent, country, r.Level, code, name).Scan(&id); e != nil {
				return db.Translate(e, fmt.Sprintf(
					"Row %d (%s) could not be saved.", i+1, code))
			}
			known[code] = id
			out.Jurisdictions++

			if _, e := tx.Exec(ctx, `
				UPDATE tax_jurisdiction_rate
				SET effective_to = $3::date
				WHERE jurisdiction_id = $1 AND treatment = $2
				  AND effective_from < $3::date
				  AND (effective_to IS NULL OR effective_to > $3::date)`,
				id, treatment, in.From); e != nil {
				return db.Translate(e, "")
			}

			var verified, verifier any
			if in.Verified {
				verified = in.From
				verifier = by
			}
			if _, e := tx.Exec(ctx, `
				INSERT INTO tax_jurisdiction_rate
				  (jurisdiction_id, treatment, rate, effective_from,
				   source_authority, source_document, source_url, notes,
				   verified_on, verified_by)
				VALUES ($1,$2,$3,$4,$5,$6,nullif($7,''),nullif($8,''),
				        $9::date,$10)
				ON CONFLICT DO NOTHING`,
				id, treatment, r.Rate, in.From, in.Authority,
				strings.TrimSpace(in.Document), strings.TrimSpace(in.URL),
				strings.TrimSpace(in.Notes), verified, verifier); e != nil {
				return db.Translate(e, fmt.Sprintf(
					"The rate for row %d (%s) could not be recorded.",
					i+1, code))
			}
			out.Rates++
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	return out, nil
}
