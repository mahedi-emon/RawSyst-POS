-- 0026 — The friendly invoice number.
--
-- sales_invoice.human_number has existed since 0014 and nothing ever filled it
-- in. Customers and shop assistants refer to an invoice by a number they can
-- read out over a telephone; nobody quotes a UUID, and asking a cashier to is
-- how a support call takes ten minutes instead of one.
--
-- # Deliberately NOT the ICV
--
-- Blueprint I3 warns that letting a custom invoice number drive the
-- tamper-evident counter is exactly the mistake to avoid. They are separate
-- columns for that reason and they are allocated by separate mechanisms here:
-- the ICV is claimed from egs_unit and must never reset, while this number is
-- per store per year and RESETS every January, which is what most shops expect
-- and what makes it useless as a tamper signal.
--
-- Sales and credit notes are numbered in separate series. A shop that files a
-- return needs to see at a glance that CRN-MAIN-2026-000012 is a credit note
-- and not the twelfth sale of the year.

CREATE TABLE invoice_number_series (
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  store_id    uuid NOT NULL REFERENCES store(id)  ON DELETE CASCADE,

  -- 'INV' for a sale, 'CRN' for a credit note, 'DBN' for a debit note.
  series      text NOT NULL,
  year        integer NOT NULL,

  next_no     bigint NOT NULL DEFAULT 1,

  PRIMARY KEY (store_id, series, year),
  CONSTRAINT invoice_series_format CHECK (series ~ '^[A-Z]{3}$'),
  CONSTRAINT invoice_series_year_sane CHECK (year BETWEEN 2000 AND 2200),
  CONSTRAINT invoice_series_no_positive CHECK (next_no >= 1)
);

CREATE INDEX invoice_number_series_tenant_idx ON invoice_number_series (tenant_id);

ALTER TABLE invoice_number_series ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_number_series FORCE  ROW LEVEL SECURITY;
CREATE POLICY invoice_number_series_isolation ON invoice_number_series
  USING (tenant_id = current_tenant_id());

-- Claims the next number for a store, series and year.
--
-- The same row-lock mechanism as egs_unit.last_icv and company.next_entry_no,
-- and for the same reasons. Reading the maximum and adding one collides under
-- load, and the sale that loses is a customer standing at a counter. A sequence
-- would not do either: sequences are not transactional, so a rolled back sale
-- would burn a number and leave a gap — and while a gap in THIS series is not
-- the compliance problem an ICV gap is, it is still the first thing an auditor
-- asks about.
--
-- INSERT ... ON CONFLICT DO UPDATE both creates the series on its first use and
-- claims from it thereafter, in one statement, so two tills opening a new
-- year at midnight cannot both create it.
CREATE OR REPLACE FUNCTION claim_invoice_number(
  p_tenant_id uuid, p_store_id uuid, p_series text, p_year integer
) RETURNS bigint
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
  claimed bigint;
BEGIN
  INSERT INTO invoice_number_series (tenant_id, store_id, series, year, next_no)
  VALUES (p_tenant_id, p_store_id, p_series, p_year, 2)
  ON CONFLICT (store_id, series, year) DO UPDATE
    SET next_no = invoice_number_series.next_no + 1
  RETURNING next_no - 1 INTO claimed;

  RETURN claimed;
END;
$$;

-- The store code appears in the number, so an invoice says which branch issued
-- it without a lookup. Branch is the first thing anyone asks about a disputed
-- receipt.
CREATE OR REPLACE FUNCTION format_invoice_number(
  p_store_code text, p_series text, p_year integer, p_no bigint
) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
  SELECT p_series || '-' || p_store_code || '-' || p_year::text || '-'
       || lpad(p_no::text, 6, '0')
$$;

-- A favourable variance is the same entry the other way round. Posting a
-- negative amount instead would make every report that sums a column wrong, so
-- the amount stays positive and the sides swap.
INSERT INTO posting_rule
  (rule_key, country, version, lines, description, effective_from)
VALUES
('inventory.variance_favourable', NULL, 1,
 '[{"role": "inventory",     "side": "debit",  "amount": "variance"},
   {"role": "cost_variance", "side": "credit", "amount": "variance"}]'::jsonb,
 'Stock cost less than standard: a favourable variance.',
 '2020-01-01');
