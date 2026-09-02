-- 0106 — Tax jurisdictions, for markets where tax is not set nationally.
--
-- # The problem this exists for
--
-- `regulatory_rule` answers "what is the rule for this country on this date".
-- That is the right shape for a VAT: Saudi Arabia has one standard rate,
-- Bangladesh has a standard rate plus reduced rates, and both are set
-- nationally. It is the wrong shape for the United States, where sales tax is
-- levied by states, counties, cities and special districts, several of which
-- apply to the same sale at once and each of which changes on its own schedule.
--
-- Modelling that as a country-level decimal would mean inventing a national US
-- rate, which does not exist. So the country stays in `regulatory_rule` and a
-- second, hierarchical shape is introduced beside it for the markets that need
-- one. A Saudi or Bangladeshi sale never touches these tables.
--
-- # What is deliberately NOT here
--
-- **No rates.** Not one row is seeded. Every rate in this table is a legal
-- value and must arrive with its source and a verification date, exactly as
-- Part N requires of the registry. Seeding a plausible 8.25% for somewhere
-- would be the precise failure this project keeps guarding against — the more
-- so because it would look right.
--
-- **No nexus, no sourcing rules, no exemption certificates.** Whether a seller
-- must collect in a jurisdiction at all (economic nexus), whether a sale is
-- taxed where it ships from or ships to (origin- vs destination-based), and
-- whether a customer holds a valid exemption certificate are three separate
-- bodies of law. The schema below leaves room for each and asserts none of
-- them. `is_origin_based` is recorded per jurisdiction because it is a property
-- OF the jurisdiction; it is not consulted by any code yet.
--
-- # Hierarchy
--
-- `parent_id` makes the chain explicit — city inside county inside state inside
-- country — so the rates applying to a sale are the rates of a jurisdiction and
-- all of its ancestors, summed. A flat table keyed by postcode would answer
-- today's question and none of the ones that follow it: which authority levies
-- which portion, what to remit to whom, and what changed when a city raised its
-- share.
--
-- # Tenant scope
--
-- Jurisdictions are reference data shared by every tenant — a city's tax is not
-- a property of one shop — so they carry no tenant_id and no RLS, the same as
-- `regulatory_rule`. Which jurisdiction a given SHOP sells in is a property of
-- that shop's address and belongs on `store`; that link is not created here
-- because nothing resolves it yet, and a column nothing reads is a column that
-- drifts.

CREATE TABLE tax_jurisdiction (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  parent_id   uuid REFERENCES tax_jurisdiction(id) ON DELETE RESTRICT,

  country     char(2) NOT NULL,

  -- 'country' | 'state' | 'county' | 'city' | 'district'
  --
  -- Text with a CHECK rather than an enum: a market with a level this list does
  -- not name should cost one ALTER, not a type rewrite that every dependent
  -- column has to be talked through.
  level       text NOT NULL,

  -- The authority's own code where it has one (a state's postal abbreviation, a
  -- FIPS code). Not generated here: an invented code would be mistaken for an
  -- official one.
  code        text NOT NULL,
  name        text NOT NULL,

  -- Whether this jurisdiction taxes on where the sale originates rather than
  -- where it is delivered. Recorded because it is a fact about the
  -- jurisdiction; NOT yet consulted by any code path.
  is_origin_based boolean,

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT tax_jurisdiction_level_known
    CHECK (level IN ('country', 'state', 'county', 'city', 'district')),
  CONSTRAINT tax_jurisdiction_country_lower
    CHECK (country = lower(country)),
  -- A country-level jurisdiction is the root of its own tree; everything else
  -- hangs off something. Without this a city could be parentless and its
  -- state's share would silently never apply.
  CONSTRAINT tax_jurisdiction_root_is_country
    CHECK ((level = 'country') = (parent_id IS NULL))
);

CREATE UNIQUE INDEX tax_jurisdiction_code_uq
  ON tax_jurisdiction (country, level, code);
CREATE INDEX tax_jurisdiction_parent_idx ON tax_jurisdiction (parent_id);

CREATE TRIGGER tax_jurisdiction_touch BEFORE UPDATE ON tax_jurisdiction
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- ---------------------------------------------------------------------------
-- Rates
-- ---------------------------------------------------------------------------
--
-- One row is one authority's rate for one treatment over one date range. A sale
-- sums the rows applying to its jurisdiction chain at its transaction date.
--
-- Dated the same way `regulatory_rule` is, and for the same reason: an invoice
-- issued last year must still be explainable by the rules in force then. The
-- exclusion constraint makes two overlapping rates for the same
-- (jurisdiction, treatment) unrepresentable rather than merely discouraged —
-- overlapping rows would make "the rate" ambiguous, and the ambiguity would be
-- resolved differently by different queries.
CREATE TABLE tax_jurisdiction_rate (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  jurisdiction_id uuid NOT NULL REFERENCES tax_jurisdiction(id) ON DELETE RESTRICT,

  -- Matches the treatment names in the country's TAX_TREATMENTS rule, so a US
  -- row says 'taxable' and never 'standard'.
  treatment       text NOT NULL,

  rate            numeric(9,6) NOT NULL,

  effective_from  date NOT NULL,
  effective_to    date,

  -- Provenance, on the same terms as regulatory_rule. A rate with no source is
  -- not a legal value, it is a number somebody typed.
  source_authority text NOT NULL,
  source_document  text NOT NULL,
  source_url       text,
  notes            text,

  -- NULL until somebody has checked this against the authority and recorded
  -- who and when. Resolution refuses an unverified rate wherever the deployment
  -- requires verification, exactly as registry.gate() does.
  verified_on      date,
  verified_by      uuid REFERENCES app_user(id) ON DELETE SET NULL,

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT tax_rate_not_negative CHECK (rate >= 0),
  -- A single jurisdiction's share of a sales tax is a fraction, not a multiple.
  -- 1.0 would mean an authority taking the entire sale.
  CONSTRAINT tax_rate_below_one CHECK (rate < 1),
  CONSTRAINT tax_rate_dates_ordered
    CHECK (effective_to IS NULL OR effective_to > effective_from),
  CONSTRAINT tax_rate_verification_coherent
    CHECK ((verified_on IS NULL) = (verified_by IS NULL))
);

-- Overlapping date ranges for one jurisdiction and treatment are
-- unrepresentable. Same device as 0004 used for the registry.
ALTER TABLE tax_jurisdiction_rate
  ADD CONSTRAINT tax_rate_no_overlap
  EXCLUDE USING gist (
    jurisdiction_id WITH =,
    treatment WITH =,
    daterange(effective_from, effective_to, '[)') WITH &&
  );

CREATE INDEX tax_jurisdiction_rate_lookup_idx
  ON tax_jurisdiction_rate (jurisdiction_id, treatment, effective_from);

CREATE TRIGGER tax_jurisdiction_rate_touch BEFORE UPDATE ON tax_jurisdiction_rate
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

COMMENT ON TABLE tax_jurisdiction IS
  'Tax authorities for markets where tax is not set nationally. Reference data, '
  'shared across tenants, no rows seeded.';
COMMENT ON TABLE tax_jurisdiction_rate IS
  'One authority''s rate for one treatment over one date range, with its source '
  'and verification. No rows seeded: every rate is a legal value that must '
  'arrive with evidence.';
