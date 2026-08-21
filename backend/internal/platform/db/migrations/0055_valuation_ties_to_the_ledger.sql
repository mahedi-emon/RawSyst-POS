-- 0055 — Make C13's tie-out hold, instead of nearly holding.
--
-- "Inventory valuation report must always tie exactly to the Inventory account
-- balance in the General Ledger." It did not, whenever a cost carried more than
-- two decimals.
--
-- `cost_layer.unit_cost` is numeric(18,4) and the weighted-average pool carries
-- `total_value` at the same precision, so the valuation reported four decimals
-- of stock. `accounting.Post` rounds every base amount to two, because that is
-- what money is. `inventory_gl_difference` compared the two directly. Three
-- receipts at 33.3333, 16.6667 and 99.9999 valued at 716.6663 against a ledger
-- of 716.67, and it reached production: `finalize.go` posts COGSTotal through
-- that same engine.
--
-- # Rounding the total was tried first, and does not work
--
-- The obvious fix — round the valuation on the way out — was attempted and
-- reverted, because the ledger is SUM(round(each posting)) while a rounded
-- total is round(SUM(...)), and those are different numbers. Under weighted
-- average, two sales off that pool gave a rounded valuation of 525.56 against a
-- ledger of 525.55. It turned a drift of 0.0007 into a drift of 0.01.
--
-- # What actually holds
--
-- The rounding-remainder rule this product already applies in five places: when
-- a whole is split, the parts must add back to it. Applied here that means two
-- halves, and BOTH are needed:
--
--   1. Every amount posted against a stock movement is the difference between
--      the rounded valuation before it and the rounded valuation after — never
--      the rounded difference. That is `valuationDelta` in `costing.go`.
--
--   2. The valuation is the SUM OF ROUNDED ROWS, which is this migration. A
--      posting telescopes into the row it moved, so the sum of the postings is
--      the sum of the rows exactly. Rounding the grand total instead would put
--      the rounding somewhere no posting can reach.
--
-- Neither half works alone, which is why the first attempt failed: it had (2)
-- without (1).
--
-- # What this does not change
--
-- Costing keeps its four decimals. `unit_cost` is untouched, and the pool's
-- `total_value` still moves by the cost at cost precision, because design 02
-- §335 is explicit that the total is authoritative and the average derived from
-- it — nudging the total to make a journal amount convenient is the thing that
-- warning is about. Only the REPORTED valuation is expressed at money
-- precision, and only the LEDGER amount is differenced from it.
--
-- The body is 0047's, unchanged except for where `round` sits. Restated in full
-- deliberately: CREATE OR REPLACE takes the whole function, and a version that
-- rewrote only the part it cared about would silently drop the weighted-average
-- branch and the shortfall deduction. That mistake was made once already while
-- diagnosing this.

CREATE OR REPLACE FUNCTION inventory_valuation(p_company_id uuid)
RETURNS numeric
LANGUAGE plpgsql STABLE AS $$
DECLARE
  method    text;
  total     numeric(18,4);
  uncovered numeric(18,4);
BEGIN
  SELECT costing_method::text INTO method FROM company WHERE id = p_company_id;

  IF method = 'wac' THEN
    -- One row per variant and warehouse, each rounded before it is added. A
    -- sale differences that row's rounded value, so the two agree row by row.
    SELECT coalesce(sum(round(total_value, 2)), 0) INTO total
    FROM stock_valuation WHERE company_id = p_company_id;
  ELSE
    -- One row per open cost layer, on the same principle: FIFO charges a sale
    -- the rounded value each layer loses, so each layer's rounded value is what
    -- the ledger was told about.
    SELECT coalesce(sum(round(qty_remaining * unit_cost, 2)), 0) INTO total
    FROM cost_layer WHERE company_id = p_company_id AND qty_remaining > 0;
  END IF;

  -- P11 (0047): units sold that were never received are not stock, and the
  -- ledger carries their provisional cost until the goods land. Rounded per
  -- shortfall for the same reason — the COGS charged for them was rounded at
  -- money precision when the sale was costed.
  SELECT coalesce(sum(round((qty - qty_settled) * provisional_unit_cost, 2)), 0)
    INTO uncovered
  FROM cost_shortfall
  WHERE company_id = p_company_id AND qty_settled < qty;

  RETURN total - uncovered;
END;
$$;
