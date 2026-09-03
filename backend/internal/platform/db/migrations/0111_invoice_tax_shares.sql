-- 0111 — Which authority each riyal of tax on an invoice belongs to.
--
-- A Saudi invoice has one tax figure and one authority to send it to, so
-- `sales_invoice.tax_total` is the whole story. An American one is not: a sale
-- in a city is taxed by the state and by the city, at rates they each set, and
-- the shop files a return with EACH of them for its own share.
--
-- registry.JurisdictionRate has always returned the combined rate broken down
-- by authority, with a comment saying why — "a total that cannot be broken down
-- cannot be filed" — and the sale path summed it to a single decimal and threw
-- the parts away. A shop could therefore charge a correct 8.25% and have no way
-- to tell the state's 6.25 from the city's 2.00 when the return was due. This is
-- where the breakdown is kept.
--
-- # Recorded at the time of sale, not recomputed at filing time
--
-- The obvious alternative is to work the split out when the return is prepared,
-- from the invoice's jurisdiction and the rate table. That breaks the first time
-- a rate changes: rerunning last quarter's invoices through today's rates would
-- reapportion tax that was charged, printed and collected under the old ones.
-- What was actually charged is a fact about the sale, so it is stored with the
-- sale.
--
-- # The shares sum to the tax on the invoice, exactly
--
-- Apportioned by the writer with the same rounding-remainder rule used
-- everywhere else in this codebase: the last share takes what is left, so the
-- parts always add to the whole and no fraction is invented or lost.

CREATE TABLE sales_invoice_tax_share (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id        uuid NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,
  invoice_id       uuid NOT NULL REFERENCES sales_invoice(id) ON DELETE CASCADE,
  jurisdiction_id  uuid NOT NULL REFERENCES tax_jurisdiction(id) ON DELETE RESTRICT,
  treatment        text NOT NULL,

  -- The authority as it stood when the sale was made. Denormalised on purpose:
  -- a city that is renamed or re-coded later must not silently rewrite the
  -- description of a return that has already been filed.
  level            text NOT NULL,
  code             text NOT NULL,
  name             text NOT NULL,

  rate             numeric(9,6)  NOT NULL CHECK (rate >= 0),
  tax_amount       numeric(18,4) NOT NULL,

  created_at       timestamptz NOT NULL DEFAULT now(),

  -- One row per authority per treatment on an invoice.
  UNIQUE (invoice_id, jurisdiction_id, treatment)
);

CREATE INDEX sales_invoice_tax_share_invoice_idx
  ON sales_invoice_tax_share (invoice_id);

-- The filing query: everything one authority is owed over a period.
CREATE INDEX sales_invoice_tax_share_jurisdiction_idx
  ON sales_invoice_tax_share (tenant_id, jurisdiction_id);

ALTER TABLE sales_invoice_tax_share ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_invoice_tax_share FORCE  ROW LEVEL SECURITY;
CREATE POLICY sales_invoice_tax_share_isolation ON sales_invoice_tax_share
  USING (tenant_id = current_tenant_id());

-- A tax return is a filed document. Its supporting detail is history the moment
-- the invoice exists, exactly like the invoice lines it came from.
CREATE TRIGGER sales_invoice_tax_share_no_delete
  BEFORE DELETE ON sales_invoice_tax_share
  FOR EACH ROW EXECUTE FUNCTION reject_delete();

COMMENT ON TABLE sales_invoice_tax_share IS
  'One authority''s share of the tax on one invoice, as charged. Written only '
  'where tax is levied below national level (US); a market with a single '
  'national rate has nothing to apportion.';
