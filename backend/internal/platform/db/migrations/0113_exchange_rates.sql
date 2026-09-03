-- 0113 — Exchange rates, with a date and a source.
--
-- G2 asks for "base currency per tenant, transaction currency support,
-- exchange-rate management, currency gain/loss tracking". Most of that shape
-- was already in place: `sales_invoice.currency` and `fx_rate` exist, and
-- `accounting.Entry` carries an FX rate and allocates every journal line in the
-- company's base currency.
--
-- What was missing is where the rate comes from. Every caller in the repository
-- passes `decimal.NewFromInt(1)`, and the sale path takes whatever the till
-- sends. So a business could record a EUR invoice against a SAR book at par and
-- nothing would object.
--
-- # A rate is a fact with a date on it, like a tax rate
--
-- This table is deliberately the same shape as `tax_jurisdiction_rate`: a value,
-- the day it applies to, who says so, and room to record where it came from. An
-- invoice is translated at the rate in force ON ITS ISSUE DATE and keeps that
-- rate for ever, which is what makes a reprint match the original and what
-- makes a gain or loss on settlement measurable at all.
--
-- # Rates are NOT invented, and none ship
--
-- Nothing is seeded. A rate that nobody entered is a refusal, not a 1.0 —
-- exactly as an unrecorded tax rate refuses a sale rather than being treated as
-- zero. A business quoting in a currency it has not given the book a rate for
-- is a setup step somebody can take, and guessing on their behalf would put a
-- wrong number in the ledger silently.
--
-- `source` is free text on purpose. A shop may take its bank's morning rate, a
-- central bank reference, or a rate negotiated with a customer, and this
-- product has no standing to prefer one. What it can insist on is that whoever
-- entered it said which.
--
-- # Per tenant, not global
--
-- A4 gives the Platform Owner the global list of CURRENCIES; the rate a
-- business books at is its own bookkeeping decision, and two tenants closing
-- the same month may legitimately use different rates from different banks.

CREATE TABLE exchange_rate (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,

  -- ISO 4217, upper case. The pair reads "one FROM buys RATE of TO".
  from_currency text NOT NULL,
  to_currency   text NOT NULL,
  rate          numeric(20,10) NOT NULL,

  as_of         date NOT NULL,

  source        text NOT NULL,
  note          text,

  entered_by    uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT exchange_rate_currencies_iso CHECK (
    from_currency ~ '^[A-Z]{3}$' AND to_currency ~ '^[A-Z]{3}$'),
  -- A rate of zero or below is not a rate, and a self-pair is not a
  -- conversion: one riyal is one riyal and needs no row to say so.
  CONSTRAINT exchange_rate_positive CHECK (rate > 0),
  CONSTRAINT exchange_rate_pair_differs CHECK (from_currency <> to_currency),
  CONSTRAINT exchange_rate_source_not_blank CHECK (btrim(source) <> ''),

  -- One rate per pair per day. Re-entering the day's rate corrects it rather
  -- than adding a second opinion the resolver would have to choose between.
  UNIQUE (tenant_id, from_currency, to_currency, as_of)
);

-- The resolver's query: the latest rate for a pair not after a given day.
CREATE INDEX exchange_rate_lookup_idx
  ON exchange_rate (tenant_id, from_currency, to_currency, as_of DESC);

CREATE TRIGGER exchange_rate_touch BEFORE UPDATE ON exchange_rate
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE exchange_rate ENABLE ROW LEVEL SECURITY;
ALTER TABLE exchange_rate FORCE  ROW LEVEL SECURITY;
CREATE POLICY exchange_rate_isolation ON exchange_rate
  USING (tenant_id = current_tenant_id());

COMMENT ON TABLE exchange_rate IS
  'One currency pair''s rate on one day, with the source whoever entered it '
  'named. Nothing is seeded: a pair with no rate on file refuses rather than '
  'being treated as par.';
