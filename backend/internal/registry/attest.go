package registry

// Recording a legal value without a browser.
//
// # The gap this closes
//
// `sources.go` turned "compose a JSON payload from six placeholder keys" into
// "type six labelled figures", which is the whole of what software can do about
// a number that lives in a statute. But the only way to type them was a form in
// Super Admin — an authenticated HTTP request from a person at a screen.
//
// That left one manual step in the middle of a DEPLOYMENT. A Saudi-serving
// production process refuses to start while `SA.EOSB.ENTITLEMENT` is a
// placeholder, so bringing up a new environment meant: migrate, start something
// that refuses to start, sign in to a service that is not running, fill a form.
// Multiply by every environment and every rebuild. The figures were established
// once and could not be carried anywhere.
//
// So the attestation is a FILE. One person reads the official documents once,
// writes down what they read and puts their name to it, and every deployment
// after that consumes the same file with no human in the loop.
//
// # Why this is not a way to invent law
//
// It is deliberately narrower than the form it complements, in five ways:
//
//   - It can only record rules the embedded source pack describes. A key the
//     pack does not name is refused, so this is not a general writer for the
//     registry.
//   - It can only fill fields that currently hold `__VERIFY__`. Correcting a
//     figure already on record is a different act with a different risk, and it
//     stays on the screen where a person sees what they are replacing.
//   - Every field is checked against what the pack says it is: a choice must be
//     one of the choices, a fraction must lie between nothing and everything,
//     a count of days may not be negative.
//   - `read_by` must resolve to a platform operator on this installation. A
//     name in a file is not an attestation; a named person who exists here is.
//   - The figures are still not in the product. This ships the SHAPE of the
//     answer, and the answer comes from whoever read the article.
//
// # Why the effective date moves forward, and cannot move back
//
// A rule resolves at the date of the document being processed, so re-running
// last March must give March's answer. While a placeholder was in force, that
// answer was a refusal — and it has to stay one, or a report re-run later would
// silently restate a period the product had no figure for.
//
// So an attestation takes effect from a date AFTER the placeholder it replaces,
// never on or before it. `effective_from` is the date this installation begins
// computing with the figures, which is not the same thing as the date the
// article came into force; the article's own date belongs in the citation, and
// the template says so.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/db"
	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// AttestationVersion is the file format this build reads and writes.
const AttestationVersion = "1"

// sourceFor is the pack lookup, indirected so a test can describe a rule of its
// own.
//
// The alternative was a test rule in the pack every deployment ships, which
// would appear on the Platform Owner's screen as a legal value somebody has to
// go and read. The seam is unexported and has exactly one non-default caller.
var sourceFor = SourceFor

// Attestation is one person's statement of what the official documents say.
type Attestation struct {
	Version string `json:"version"`

	// ReadBy is the sign-in address of the platform operator who read the
	// documents. Resolved against this installation before anything is
	// written, so the trail names somebody who exists here.
	ReadBy string `json:"read_by"`

	// ReadOn is when they read them, as they would write it in a citation.
	ReadOn string `json:"read_on"`

	// Statement is what they are attesting, in their own words. Required, and
	// kept as the rule's note: a figure with no statement beside it is a number
	// in a table, and six months later nobody can tell it from a guess.
	Statement string `json:"statement"`

	Rules []AttestedRule `json:"rules"`
}

// AttestedRule is one rule's figures.
type AttestedRule struct {
	Key  string `json:"rule_key"`
	From string `json:"effective_from"`

	// Values are the figures, keyed by the field names the source pack uses.
	// Strings throughout: a decimal that goes through a float is a decimal that
	// can come back different, and every payload in this registry is written
	// this way already.
	Values map[string]string `json:"values"`

	// Guidance is what the template wrote to help somebody fill the file in —
	// the citation, the articles, the unit and the meaning of each field. Read
	// back and ignored, so a filled-in template can be handed straight back
	// without stripping anything out.
	Guidance json.RawMessage `json:"guidance,omitempty"`
}

// AttestationOutcome is what happened, or would happen, to one rule.
type AttestationOutcome struct {
	Key     string `json:"rule_key"`
	Country string `json:"country"`

	// Action is one of: recorded, would record, already recorded.
	Action string `json:"action"`
	From   string `json:"effective_from,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ParseAttestation reads the file and checks everything that can be checked
// without a database.
//
// Separated from applying it so `-check` is a real check: a file with a
// mistyped field name or a fraction above one is refused on a laptop rather
// than half-applied on a server.
func ParseAttestation(raw []byte) (Attestation, error) {
	var att Attestation
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&att); err != nil {
		return Attestation{}, errs.Wrap(err, errs.CodeInvalidInput,
			"That regulatory source file could not be read as JSON.")
	}

	if v := strings.TrimSpace(att.Version); v != "" && v != AttestationVersion {
		return Attestation{}, errs.Newf(errs.CodeInvalidInput,
			"This build reads version %s of the regulatory source file and "+
				"that one says version %s.", AttestationVersion, v)
	}
	if strings.TrimSpace(att.ReadBy) == "" {
		return Attestation{}, errs.Validation(
			"Say who read the official documents.").
			WithField("read_by",
				"The sign-in address of the platform operator putting their "+
					"name to these figures. Verification is a person's act, "+
					"and the registry records whose.")
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(att.ReadOn)); err != nil {
		return Attestation{}, errs.Validation("Say when they read them.").
			WithField("read_on", "A date like 2026-09-09.")
	}
	if len(strings.TrimSpace(att.Statement)) < 20 {
		return Attestation{}, errs.Validation("Say what is being attested.").
			WithField("statement",
				"What was read, in which document, and what it said. This is "+
					"kept beside the figure, and it is what tells a reader "+
					"six months from now that somebody stood behind it.")
	}
	if len(att.Rules) == 0 {
		return Attestation{}, errs.Validation(
			"That file records no rules.").
			WithField("rules",
				"Write one entry per legal value. `-template` produces the "+
					"entries this installation is waiting for.")
	}

	seen := map[string]bool{}
	for i, r := range att.Rules {
		key := strings.ToUpper(strings.TrimSpace(r.Key))
		if key == "" {
			return Attestation{}, errs.Newf(errs.CodeInvalidInput,
				"Entry %d names no rule.", i+1)
		}
		if seen[key] {
			return Attestation{}, errs.Newf(errs.CodeInvalidInput,
				"%s appears twice in that file. One entry per rule: two would "+
					"be two different answers to the same question.", key)
		}
		seen[key] = true

		src, ok := sourceFor(key)
		if !ok {
			return Attestation{}, errs.Newf(errs.CodeInvalidInput,
				"%s is not a rule this file can record. It carries the values "+
					"the regulatory source pack describes, which is what makes "+
					"each figure checkable; anything else is recorded in Super "+
					"Admin, where the person doing it can see what they are "+
					"replacing.", key)
		}
		if strings.TrimSpace(r.From) != "" {
			if _, err := time.Parse("2006-01-02", strings.TrimSpace(r.From)); err != nil {
				return Attestation{}, errs.Newf(errs.CodeInvalidInput,
					"%s: effective_from must be a date like 2026-09-09.", key)
			}
		}
		if err := checkValues(src, r.Values); err != nil {
			return Attestation{}, err
		}
		att.Rules[i].Key = key
	}
	return att, nil
}

// checkValues holds each figure to what the pack says it is.
//
// The pack is the only thing that knows a fraction from a count of days, and
// this is the whole reason it carries a kind and a unit rather than only a
// label. A resignation fraction of 33 instead of 0.33 would otherwise pay
// somebody thirty-three times their award, and nothing downstream would
// question it: the arithmetic is valid, only the figure is absurd.
func checkValues(src SourceRule, values map[string]string) error {
	if len(values) == 0 {
		return errs.Newf(errs.CodeInvalidInput,
			"%s: no figures. It needs %s.", src.RuleKey, fieldList(src)).
			WithField("payload", fieldList(src))
	}

	known := make(map[string]SourceField, len(src.Fields))
	for _, f := range src.Fields {
		known[f.Name] = f
	}
	for name := range values {
		if _, ok := known[name]; !ok {
			return errs.Newf(errs.CodeInvalidInput,
				"%s has no field called %q. It takes %s.",
				src.RuleKey, name, fieldList(src)).
				WithField(name, "Not a field of this rule. It takes "+
					fieldList(src)+".")
		}
	}

	for _, f := range src.Fields {
		v, ok := values[f.Name]
		if !ok {
			return errs.Newf(errs.CodeInvalidInput,
				"%s.%s is missing: %s. Every field of a rule is recorded "+
					"together, because a payload with one figure filled in and "+
					"the rest still placeholders would refuse exactly as it "+
					"does now.", src.RuleKey, f.Name, f.Label).
				WithField(f.Name, f.Label+". "+f.Help)
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return errs.Newf(errs.CodeInvalidInput,
				"%s.%s is blank: %s", src.RuleKey, f.Name, f.Help).
				WithField(f.Name, f.Help)
		}
		if strings.Contains(v, Placeholder) {
			return errs.Newf(errs.CodeInvalidInput,
				"%s.%s still holds %s. Replace it with the figure from %s.",
				src.RuleKey, f.Name, Placeholder, src.Document).
				WithField(f.Name, "Still "+Placeholder+". "+f.Help)
		}

		switch f.Kind {
		case "choice":
			if !containsString(f.Choices, v) {
				return errs.Newf(errs.CodeInvalidInput,
					"%s.%s is %q, and this product understands only: %s. "+
						"%s", src.RuleKey, f.Name, v,
					strings.Join(f.Choices, ", "), f.Help).
					WithField(f.Name, "One of: "+
						strings.Join(f.Choices, ", ")+". "+f.Help)
			}
		case "decimal":
			d, err := decimal.NewFromString(v)
			if err != nil {
				return errs.Newf(errs.CodeInvalidInput,
					"%s.%s is %q, which is not a number. %s",
					src.RuleKey, f.Name, v, f.Help).
					WithField(f.Name, "Not a number. "+f.Help)
			}
			if d.IsNegative() {
				return errs.Newf(errs.CodeInvalidInput,
					"%s.%s is negative. %s", src.RuleKey, f.Name, f.Help).
					WithField(f.Name, "Cannot be negative. "+f.Help)
			}
			if f.Unit == "fraction" && d.GreaterThan(decimal.NewFromInt(1)) {
				return errs.Newf(errs.CodeInvalidInput,
					"%s.%s is %s, and it is a fraction of the award — "+
						"somewhere between 0 and 1. A third is 0.3333, not 33.",
					src.RuleKey, f.Name, v).
					WithField(f.Name, "A fraction between 0 and 1. "+
						"A third is 0.3333, not 33.")
			}
		case "int":
			n, err := strconv.Atoi(v)
			if err != nil {
				return errs.Newf(errs.CodeInvalidInput,
					"%s.%s is %q, which is not a whole number. %s",
					src.RuleKey, f.Name, v, f.Help).
					WithField(f.Name, "Not a whole number. "+f.Help)
			}
			if n < 0 {
				return errs.Newf(errs.CodeInvalidInput,
					"%s.%s is negative. %s", src.RuleKey, f.Name, f.Help).
					WithField(f.Name, "Cannot be negative. "+f.Help)
			}
		case "text":
			// Nothing beyond non-empty: the pack uses this kind exactly where
			// the answer is prose, and inventing a shape for prose is how a
			// legitimate answer gets refused.
		default:
			return errs.Newf(errs.CodeInternal,
				"%s.%s is described with an unknown kind %q in the source pack.",
				src.RuleKey, f.Name, f.Kind)
		}
	}
	return nil
}

// ValidatePayload checks a rule payload against the source pack that describes
// it, and is the same check the attestation file goes through.
//
// # Why the screen needs it too
//
// `ApplyAttestation` has validated every figure against the pack since it was
// written: the field names, the units, the choices, the signs, and the rule
// that a fraction of an award is between 0 and 1. `RecordRule` — the Super
// Admin screen and the HTTP route behind it — validated none of it. It refused
// an empty payload and a payload still containing the placeholder, and wrote
// anything else.
//
// So the two doors into the registry disagreed about what a legal value is.
// A platform operator recording end-of-service through the screen could write
// `days_per_year_first_five: "fifteen"`, or a resignation fraction of 33 where
// the article says a third, or invent a field name the calculation never
// reads. Nothing refused it at the point of entry. The engine catches some of
// it later — `eosbEntitlement` refuses a fraction above 1 and a decimal that
// will not parse — but by then the wrong figure is on record as the law, it is
// in the audit trail as somebody's assertion, and what surfaces is a payroll
// run failing rather than a form field saying what is wrong with what was
// typed.
//
// Validating here closes that. The screen gets the same refusals as the file,
// field by field, so the guided form can point at the box that is wrong.
//
// # A key the pack does not describe is not an error
//
// The pack describes the rules whose figures somebody has to go and read out
// of a published document. Plenty of rules are not like that, and refusing a
// payload merely because it is not in the pack would make the registry
// closed to anything the pack has not caught up with yet.
func ValidatePayload(key string, payload []byte) error {
	src, ok := SourceFor(key)
	if !ok {
		return nil
	}
	if len(payload) == 0 {
		return errs.Newf(errs.CodeInvalidInput,
			"%s: no figures. It needs %s.", src.RuleKey, fieldList(src)).
			WithField("payload", fieldList(src))
	}

	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return errs.Newf(errs.CodeInvalidInput,
			"%s: the value must be a JSON object of figures. It needs %s.",
			src.RuleKey, fieldList(src)).
			WithField("payload", fieldList(src))
	}

	// Strings throughout, as every payload in this registry is written and as
	// `AttestedRule.Values` requires. A decimal that goes through a JSON number
	// is a decimal that can come back different, so a bare number here is
	// refused by name rather than quietly stringified into the registry.
	values := make(map[string]string, len(raw))
	for name, v := range raw {
		s, isString := v.(string)
		if !isString {
			return errs.Newf(errs.CodeInvalidInput,
				"%s.%s must be quoted. Every figure in this registry is a "+
					"string, because a decimal that goes through a JSON "+
					"number can come back different.", src.RuleKey, name).
				WithField(name, "Quote the figure: \"15\", not 15.")
		}
		values[name] = s
	}
	return checkValues(src, values)
}

func fieldList(src SourceRule) string {
	names := make([]string, 0, len(src.Fields))
	for _, f := range src.Fields {
		names = append(names, f.Name)
	}
	return strings.Join(names, ", ")
}

func containsString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// --- what is still outstanding ---------------------------------------------

// Unrecorded is one rule this installation is still waiting on.
type Unrecorded struct {
	Key     string   `json:"rule_key"`
	Country string   `json:"country"`
	Blocker bool     `json:"release_blocker"`
	From    string   `json:"effective_from"`
	Fields  []string `json:"unfilled_fields"`

	// Blocks is what an unverified blocker actually prevents — BlocksOnboarding
	// or BlocksFeature (0124). Carried so a report can tell "no shop in that
	// market can trade" from "one capability cannot be computed", which are
	// different enough that a deployment pipeline should fail on the first and
	// not on the second.
	Blocks string `json:"blocks"`

	// Described says the source pack knows where this value comes from, and so
	// whether an attestation file can carry it at all.
	Described bool `json:"described"`
}

// Outstanding lists every rule in force whose payload still holds a
// placeholder, and which of its fields are unfilled.
//
// Read from the registry rather than from the pack, in that direction on
// purpose: the pack says what CAN be recorded and the database says what still
// has to be, and only the second one is the truth about this installation.
func (s *Service) Outstanding(
	ctx context.Context, country string,
) ([]Unrecorded, error) {
	country = strings.ToLower(strings.TrimSpace(country))

	out := []Unrecorded{}
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `
			SELECT rule_key, country, release_blocker, blocks,
			       to_char(effective_from, 'YYYY-MM-DD'), payload
			FROM regulatory_rule
			WHERE effective_to IS NULL
			  AND payload::text LIKE '%' || $1 || '%'
			  AND ($2 = '' OR country = $2)
			ORDER BY release_blocker DESC, country, rule_key`,
			Placeholder, country)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var u Unrecorded
			var payload []byte
			if e := rows.Scan(&u.Key, &u.Country, &u.Blocker, &u.Blocks, &u.From,
				&payload); e != nil {
				return e
			}
			u.Fields = placeholderFields(payload)
			_, u.Described = sourceFor(u.Key)
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, db.Translate(err, "")
}

// placeholderFields names the top-level fields of a payload that are still
// unfilled.
//
// Top level only, and a nested placeholder is reported as the object that holds
// it. A rule whose shape is a matrix — the social insurance schedule was one —
// is not something a flat list of labelled figures can express, and pretending
// otherwise would produce a file somebody could fill in wrongly.
func placeholderFields(payload []byte) []string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil
	}
	var out []string
	for k, v := range m {
		if strings.Contains(string(v), Placeholder) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// --- the template ----------------------------------------------------------

// Template builds the file somebody takes away and fills in.
//
// Generated from what this installation is actually waiting for, not from a
// fixed list: an environment with nothing outstanding gets a file with no rules
// in it, which is the honest answer and is also how you find out you are done.
func (s *Service) Template(
	ctx context.Context, country string,
) ([]byte, error) {
	open, err := s.Outstanding(ctx, country)
	if err != nil {
		return nil, err
	}

	att := Attestation{
		Version:   AttestationVersion,
		ReadBy:    "",
		ReadOn:    "",
		Statement: "",
		Rules:     []AttestedRule{},
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, u := range open {
		src, ok := sourceFor(u.Key)
		if !ok {
			// Reported by `-check`, not silently dropped into a file that
			// cannot record it.
			continue
		}

		values := map[string]string{}
		fields := make([]map[string]any, 0, len(src.Fields))
		for _, f := range src.Fields {
			values[f.Name] = ""
			g := map[string]any{
				"field": f.Name,
				"ask":   f.Label,
				"help":  f.Help,
			}
			if f.Unit != "" {
				g["unit"] = f.Unit
			}
			if f.Article != "" {
				g["article"] = f.Article
			}
			if len(f.Choices) > 0 {
				g["one_of"] = f.Choices
			}
			fields = append(fields, g)
		}

		guidance, _ := json.Marshal(map[string]any{
			"title":     src.Title,
			"authority": src.Authority,
			"document":  src.Document,
			"url":       src.URL,
			"articles":  src.Articles,
			"reading":   src.Reading,
			"effective_from": "The date THIS INSTALLATION begins computing " +
				"with these figures. It is not the date the article came " +
				"into force — that belongs in the statement, with the " +
				"citation. It must fall after " + u.From + ", the day the " +
				"placeholder it replaces took effect, because a period the " +
				"product had no verified figure for has to keep refusing " +
				"when a report is re-run.",
			"fields": fields,
		})

		att.Rules = append(att.Rules, AttestedRule{
			Key:      u.Key,
			From:     firstEffectiveDate(u.From, today),
			Values:   values,
			Guidance: guidance,
		})
	}

	return json.MarshalIndent(att, "", "  ")
}

// firstEffectiveDate is the earliest date an attestation may take effect.
//
// The day after the placeholder started, or today, whichever is later. Today,
// almost always: the placeholders were seeded by migrations long before anybody
// reads a statute. The other branch matters on a database migrated this morning.
func firstEffectiveDate(placeholderFrom string, today time.Time) string {
	from, err := time.Parse("2006-01-02", placeholderFrom)
	if err != nil {
		return today.Format("2006-01-02")
	}
	next := from.AddDate(0, 0, 1)
	if next.After(today) {
		return next.Format("2006-01-02")
	}
	return today.Format("2006-01-02")
}

// --- applying it -----------------------------------------------------------

// ApplyAttestation records every figure in the file, or reports what it would
// record.
//
// `dryRun` runs every check and every read and writes nothing, so the answer a
// deployment gets from `-check` is the answer `-apply` will act on.
func (s *Service) ApplyAttestation(
	ctx context.Context, att Attestation, dryRun bool,
) ([]AttestationOutcome, error) {
	by, err := s.resolveAttestor(ctx, att.ReadBy)
	if err != nil {
		return nil, err
	}

	outcomes := make([]AttestationOutcome, 0, len(att.Rules))
	for _, r := range att.Rules {
		outcome, err := s.applyOne(ctx, att, r, by, dryRun)
		if err != nil {
			return outcomes, err
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

// resolveAttestor turns the address in the file into a person on this
// installation.
//
// A platform operator, specifically: a business owner cannot record what every
// business in the market computes from, and that boundary is the same one the
// Super Admin screen enforces. An address that matches nobody is refused rather
// than recorded as a name, because "verified by somebody who does not exist" is
// worse evidence than no verification at all.
func (s *Service) resolveAttestor(
	ctx context.Context, email string,
) (uuid.UUID, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	var id uuid.UUID
	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			SELECT id FROM app_user
			WHERE tenant_id IS NULL AND lower(email) = $1
			  AND status = 'active'`, email).Scan(&id)
		if e == pgx.ErrNoRows {
			return errs.Newf(errs.CodeNotFound,
				"%s is not an active platform operator on this installation, "+
					"so nothing can be recorded as verified by them. Create "+
					"the operator first, or name one who is already here.",
				email)
		}
		return e
	})
	if err != nil {
		return uuid.Nil, db.Translate(err, "")
	}
	return id, nil
}

// current is the rule in force for a key, as the applier needs to see it.
type current struct {
	from      time.Time
	verified  bool
	blocker   bool
	payload   map[string]json.RawMessage
	rawStatus string
}

func (s *Service) applyOne(
	ctx context.Context, att Attestation, r AttestedRule,
	by uuid.UUID, dryRun bool,
) (AttestationOutcome, error) {
	src, ok := sourceFor(r.Key)
	if !ok {
		return AttestationOutcome{}, errs.Newf(errs.CodeInvalidInput,
			"%s is not described by the regulatory source pack.", r.Key)
	}
	out := AttestationOutcome{Key: r.Key, Country: src.Country}

	cur, err := s.currentRule(ctx, r.Key, src.Country)
	if err != nil {
		return out, err
	}

	// Already done. The idempotency that lets a deployment run this on every
	// boot without a second thought: the same file applied twice records once,
	// rather than superseding a rule with an identical copy of itself and
	// filling the registry with a history of nothing happening.
	if cur.verified && !payloadHasPlaceholder(cur.payload) {
		if samePayload(cur.payload, r.Values) {
			out.Action = "already recorded"
			out.From = cur.from.Format("2006-01-02")
			out.Detail = "the figures in force are the ones in this file"
			return out, nil
		}
		return out, errs.Newf(errs.CodeConflict,
			"%s is already recorded and verified, with figures that differ "+
				"from this file. Changing a value already in force is done in "+
				"Super Admin, where the person doing it sees what they are "+
				"replacing and from when.", r.Key)
	}

	// Only fields that are actually unfilled.
	for name := range r.Values {
		v, present := cur.payload[name]
		if !present {
			return out, errs.Newf(errs.CodeInvalidInput,
				"%s: the rule in force has no field called %q, so this file "+
					"would be adding one rather than filling one in.",
				r.Key, name)
		}
		if !strings.Contains(string(v), Placeholder) {
			return out, errs.Newf(errs.CodeConflict,
				"%s.%s already holds a figure. This file fills in what is "+
					"unrecorded; correcting what is recorded is done in Super "+
					"Admin.", r.Key, name)
		}
	}

	merged, err := mergePayload(cur.payload, r.Values)
	if err != nil {
		return out, err
	}
	if strings.Contains(string(merged), Placeholder) {
		return out, errs.Newf(errs.CodeInvalidInput,
			"%s would still hold %s after this file is applied, so it would "+
				"go on refusing. The unfilled fields are: %s.",
			r.Key, Placeholder,
			strings.Join(placeholderFields(merged), ", "))
	}

	from, err := effectiveFrom(r, cur)
	if err != nil {
		return out, err
	}
	out.From = from.Format("2006-01-02")

	if dryRun {
		out.Action = "would record"
		out.Detail = fmt.Sprintf("%d figure(s) from %s",
			len(r.Values), src.Document)
		return out, nil
	}

	// Through the same path the screen uses. Supersession, the placeholder
	// refusal, the audit entry and the cache invalidation are one behaviour
	// with one implementation, not two that agree until one of them changes.
	if _, err := s.RecordRule(ctx, NewRule{
		Key:       r.Key,
		Country:   src.Country,
		Payload:   merged,
		From:      from,
		Authority: src.Authority,
		Document:  src.Document,
		URL:       src.URL,
		Blocker:   cur.blocker,
		Verified:  true,
		Notes:     attestationNote(att, src),
	}, by); err != nil {
		return out, err
	}

	out.Action = "recorded"
	out.Detail = fmt.Sprintf("verified by %s, read %s", att.ReadBy, att.ReadOn)
	return out, nil
}

// attestationNote is what the registry carries beside the figure.
//
// The statement in the reader's own words, then the citation the product
// supplied. Both, because the first is evidence that a person stood behind it
// and the second is how the next person checks.
func attestationNote(att Attestation, src SourceRule) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(att.Statement))
	b.WriteString("\n\nRead by ")
	b.WriteString(strings.TrimSpace(att.ReadBy))
	b.WriteString(" on ")
	b.WriteString(strings.TrimSpace(att.ReadOn))
	b.WriteString(" in ")
	b.WriteString(src.Document)
	if len(src.Articles) > 0 {
		b.WriteString(", article")
		if len(src.Articles) > 1 {
			b.WriteString("s")
		}
		b.WriteString(" ")
		b.WriteString(strings.Join(src.Articles, " and "))
	}
	b.WriteString(".")
	if src.URL != "" {
		b.WriteString(" ")
		b.WriteString(src.URL)
	}
	b.WriteString("\nRecorded from a regulatory source file.")
	return b.String()
}

func (s *Service) currentRule(
	ctx context.Context, key, country string,
) (current, error) {
	var cur current
	var payload []byte
	var verifiedOn *time.Time

	err := s.pool.TxAsPlatform(ctx, func(tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `
			SELECT effective_from, verified_on, release_blocker, payload
			FROM regulatory_rule
			WHERE rule_key = $1 AND country = $2 AND effective_to IS NULL`,
			key, country).
			Scan(&cur.from, &verifiedOn, &cur.blocker, &payload)
		if e == pgx.ErrNoRows {
			return errs.Newf(errs.CodeNotFound,
				"%s is not in this installation's registry. Rules are seeded "+
					"by migration; a key that is absent means the migration "+
					"chain is behind, not that a figure is missing.", key)
		}
		return e
	})
	if err != nil {
		return current{}, db.Translate(err, "")
	}

	cur.verified = verifiedOn != nil
	if err := json.Unmarshal(payload, &cur.payload); err != nil {
		return current{}, errs.Newf(errs.CodeInternal,
			"%s holds a payload that is not an object, so a labelled figure "+
				"cannot be placed in it.", key)
	}
	return cur, nil
}

// effectiveFrom decides the date, and refuses one that would rewrite the past.
func effectiveFrom(r AttestedRule, cur current) (time.Time, error) {
	raw := strings.TrimSpace(r.From)
	if raw == "" {
		return time.Time{}, errs.Newf(errs.CodeInvalidInput,
			"%s: say from when this installation computes with these figures.",
			r.Key)
	}
	from, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, errs.Newf(errs.CodeInvalidInput,
			"%s: effective_from must be a date like 2026-09-09.", r.Key)
	}
	if !from.After(cur.from) {
		return time.Time{}, errs.Newf(errs.CodeInvalidInput,
			"%s: %s is not after %s, the day the placeholder it replaces took "+
				"effect. A period the product had no verified figure for has "+
				"to go on refusing when a report is re-run, so a recorded "+
				"value starts after the placeholder rather than over it. Put "+
				"the date the article came into force in the statement.",
			r.Key, raw, cur.from.Format("2006-01-02"))
	}
	return from, nil
}

func mergePayload(
	cur map[string]json.RawMessage, values map[string]string,
) (json.RawMessage, error) {
	merged := make(map[string]json.RawMessage, len(cur))
	for k, v := range cur {
		merged[k] = v
	}
	for k, v := range values {
		b, err := json.Marshal(strings.TrimSpace(v))
		if err != nil {
			return nil, errs.Wrap(err, errs.CodeInternal,
				"That figure could not be recorded.")
		}
		merged[k] = b
	}
	return json.Marshal(merged)
}

func payloadHasPlaceholder(p map[string]json.RawMessage) bool {
	for _, v := range p {
		if strings.Contains(string(v), Placeholder) {
			return true
		}
	}
	return false
}

// samePayload reports whether what is on record already says what the file
// says. Compared field by field as text, because the payload stores figures as
// strings and "0.3333" and "0.33330" are the same number and different records.
func samePayload(cur map[string]json.RawMessage, values map[string]string) bool {
	for k, v := range values {
		raw, ok := cur[k]
		if !ok {
			return false
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return false
		}
		if s != strings.TrimSpace(v) {
			return false
		}
	}
	return true
}
