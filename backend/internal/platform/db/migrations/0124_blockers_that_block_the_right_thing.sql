-- 0124 — two gates that refused more than they had any business refusing.
--
-- Both were mine, both were built for good reasons, and both turned a caution
-- into a wall across a market. Neither was ever required by an authority.
--
-- ---------------------------------------------------------------------------
-- 1. A release blocker should block what it guards, not everything
-- ---------------------------------------------------------------------------
--
-- `requireMarketIsUsable` refuses to create a business in a market while ANY
-- release-blocking rule for that market is unverified. That is right for a rule
-- the first sale depends on — a shop cannot ring anything up in Saudi Arabia
-- without ZATCA's XML and QR formats, so onboarding one would be selling them a
-- till that cannot trade.
--
-- It is wrong for `SA.EOSB.ENTITLEMENT`. End of service is what an employer
-- owes somebody who LEAVES. A coffee shop can be onboarded, trade for a year
-- and hire nobody who resigns, and the entitlement bands never come into it.
-- Refusing to create the business is refusing a sale today over a calculation
-- that may never be performed.
--
-- Nothing is loosened by fixing this. `gate()` already refuses an unverified
-- rule AT THE POINT OF USE, so an unverified EOSB rule still makes an
-- end-of-service calculation impossible — which is the protection that
-- mattered. The provisioning gate was a second, coarser copy of it, and the
-- coarseness is the whole defect.
--
-- So a blocker now says what it blocks:
--
--   onboarding — the market cannot be sold into at all until this is verified
--   feature    — this capability is unavailable until this is verified
--
-- `feature` is the default, because it is the weaker claim and a rule that
-- genuinely stops a market should have to say so.

ALTER TABLE regulatory_rule
  ADD COLUMN blocks text NOT NULL DEFAULT 'feature';

ALTER TABLE regulatory_rule
  ADD CONSTRAINT regulatory_rule_blocks_known
  CHECK (blocks IN ('onboarding', 'feature'));

-- The rules a Saudi shop's FIRST SALE cannot happen without. Every one of them
-- shapes the invoice document itself: without them nothing can be issued, so
-- onboarding a business would be selling them a till that cannot trade.
UPDATE regulatory_rule
SET blocks = 'onboarding'
WHERE release_blocker AND rule_key LIKE 'SA.ZATCA.%';

COMMENT ON COLUMN regulatory_rule.blocks IS
  'What an unverified release blocker prevents: onboarding a business in that '
  'market, or only the capability the rule governs. Payroll and end-of-service '
  'rules are ''feature'' — a shop can trade for a year without either, and the '
  'point-of-use gate already refuses to compute them from a placeholder.';

-- ---------------------------------------------------------------------------
-- 2. Tax rates: a human signature is not what makes published data usable
-- ---------------------------------------------------------------------------
--
-- 0120 built a review-then-verify workflow for imported tax rates, requiring
-- two different people before a rate could be charged. That control is worth
-- having and it is INTERNAL GOVERNANCE. It is not a CDTFA requirement, and
-- CDTFA asks nobody's permission to publish a rate: the schedule is a public
-- document and a shop that charges what it says is charging correctly.
--
-- Treating it as mandatory made 541 Californian locations unusable and closed
-- the American market over an internal preference dressed up as compliance.
--
-- What actually makes an imported rate trustworthy is checkable by software:
--
--   * it carries its provenance — the authority, the document, the URL
--   * every authority in its chain answers on the date being priced
--   * no two rates for one authority overlap in time
--   * the rate is a fraction, not a multiple
--
-- Three of those the schema already enforces. The fourth the resolver already
-- checks. So a rate that has been imported with its source and validated
-- against those rules is usable, and `activated_on` records that this happened
-- and when.
--
-- `verified_on` stays exactly as it was, and stays optional: a business that
-- wants a named person to sign every schedule off can still require it, and the
-- review workflow 0120 built is unchanged. What it no longer does is stand
-- between a lawfully published rate and the shop that has to charge it.

ALTER TABLE tax_jurisdiction_rate
  ADD COLUMN activated_on date,
  ADD COLUMN activated_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  ADD COLUMN activation_note text;

ALTER TABLE tax_jurisdiction_rate
  ADD CONSTRAINT tax_rate_activation_coherent
  CHECK ((activated_on IS NULL) = (activation_note IS NULL));

CREATE INDEX tax_jurisdiction_rate_inactive_idx
  ON tax_jurisdiction_rate (activated_on)
  WHERE activated_on IS NULL AND verified_on IS NULL;

COMMENT ON COLUMN tax_jurisdiction_rate.activated_on IS
  'When this rate passed validation and became usable. Validation is what '
  'software can honestly assert: full provenance, a chain that resolves, no '
  'overlapping periods. `verified_on` remains a separate, optional record that '
  'a named person checked the figure against the authority.';

-- The frozen-column trigger has to let the new stamps be written. Rebuilt with
-- the same frozen list — the figure, its dates and its provenance still cannot
-- move under an activation, which is the property that matters.
DROP TRIGGER IF EXISTS tax_jurisdiction_rate_frozen_fields ON tax_jurisdiction_rate;
CREATE TRIGGER tax_jurisdiction_rate_frozen_fields
  BEFORE UPDATE ON tax_jurisdiction_rate
  FOR EACH ROW EXECUTE FUNCTION reject_column_change(
    'jurisdiction_id', 'treatment', 'rate', 'effective_from',
    'source_authority', 'source_document');

-- ---------------------------------------------------------------------------
-- 3. Activate the schedule 0118 shipped
-- ---------------------------------------------------------------------------
--
-- 0118 loaded CDTFA's published file and left it inert, so every deployment
-- starts with California unable to trade. Activating it here is the same act
-- the route performs, applied to the data this migration series shipped — and
-- the WHERE clause is the validation, so only rows that genuinely pass are
-- activated.
--
-- Nothing is marked VERIFIED. That stamp still means a named person checked the
-- figure by hand, and nobody has.

UPDATE tax_jurisdiction_rate r
SET activated_on = current_date,
    activation_note =
      'Activated with migration 0124. Validated: names its authority, its '
      'document and the page it came from; its jurisdiction resolves to a '
      'country root; the schema holds the rate below 1 and refuses overlapping '
      'periods for one authority. NOT verified — no person has checked these '
      'figures against CDTFA''s page by hand, and verified_on stays null.'
FROM tax_jurisdiction j
WHERE j.id = r.jurisdiction_id
  AND r.source_authority = 'cdtfa'
  AND r.activated_on IS NULL
  AND btrim(coalesce(r.source_document, '')) <> ''
  AND btrim(coalesce(r.source_url, '')) <> ''
  AND (WITH RECURSIVE chain AS (
         SELECT id, parent_id FROM tax_jurisdiction WHERE id = r.jurisdiction_id
         UNION ALL
         SELECT p.id, p.parent_id FROM tax_jurisdiction p
         JOIN chain c ON c.parent_id = p.id)
       SELECT count(*) FROM chain WHERE parent_id IS NULL) = 1;

-- The United States country root carries an explicit zero (0110). It is a rate
-- like any other and the chain refuses without it, so it is activated on the
-- same terms.
UPDATE tax_jurisdiction_rate r
SET activated_on = current_date,
    activation_note =
      'Activated with migration 0124. The federation levies no sales tax, and '
      '0110 records that as an explicit zero rather than an absence.'
FROM tax_jurisdiction j
WHERE j.id = r.jurisdiction_id
  AND j.country = 'us' AND j.level = 'country'
  AND r.activated_on IS NULL AND r.verified_on IS NULL;
