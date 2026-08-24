-- 0063 — The seller address ZATCA actually asks for.
--
-- # What was wrong
--
-- `store` carried one free-text `address` column. ZATCA wants six separate
-- fields, and says so in BR-KSA-09:
--
--   "Seller address must contain street name (BT-35), building number
--    (KSA-17), postal code (BT-38), city (BT-37), District (KSA-3), country
--    code (BT-40)."
--
-- with two format rules on top:
--
--   BR-KSA-37  the seller address building number must contain 4 digits
--   BR-KSA-66  seller postal code (BT-38) must be 5 digits
--
-- Asked directly, ZATCA's validator answered valid=true and returned all three
-- as WARNINGS — so invoices are accepted today. SA.ZATCA.API_RESPONSES already
-- records ZATCA warning that such warnings "might become rejections in the
-- future", which is what makes this worth fixing rather than tolerating.
--
-- These are the fields of the Saudi National Address, which BR-KSA-09 links to
-- directly: https://splonline.com.sa/en/national-address-1/
--
-- # Why the old column is not parsed into the new ones
--
-- It is tempting to split the free text on commas and backfill. It is also
-- wrong: whatever a shop happened to type would land in fields a tax authority
-- reads, and a building number that is really part of a street name is worse
-- than an empty one. The new columns start empty and a shop fills them in.
--
-- `address` is KEPT, not dropped. It is what existing shops have, it is shown
-- on screens that are not invoices, and dropping a populated column to make a
-- migration tidy loses data nobody agreed to lose.
--
-- # Why nullable, when ZATCA requires them
--
-- Because requiring them here would stop every existing shop from trading at
-- the moment of upgrade. The requirement is enforced where it belongs — at the
-- point an invoice is built, which already refuses a unit that cannot produce
-- a compliant document and names the field that is missing. That is blueprint
-- A4: compliance capability is derived state, not a toggle.
--
-- The CHECK constraints below apply only when a value is present, so a shop
-- cannot save a malformed one.

ALTER TABLE store
  ADD COLUMN street            text,
  ADD COLUMN building_number   text,
  ADD COLUMN additional_number text,
  ADD COLUMN district          text,
  ADD COLUMN city              text,
  ADD COLUMN postal_code       text,
  -- ISO 3166-1 alpha-2, upper case. Defaulted to the company's country at the
  -- application layer rather than here, so a non-Saudi tenant is not quietly
  -- given a Saudi address.
  ADD COLUMN country_code      text;

-- BR-KSA-37: exactly four digits. Checked as a format rather than a length so
-- "12A4" is refused as clearly as "123".
ALTER TABLE store
  ADD CONSTRAINT store_building_number_format
    CHECK (building_number IS NULL OR building_number ~ '^[0-9]{4}$');

-- The Saudi National Address secondary number is also four digits. Not
-- required by any BR rule, so it is optional — but if it is given it must be
-- the right shape.
ALTER TABLE store
  ADD CONSTRAINT store_additional_number_format
    CHECK (additional_number IS NULL OR additional_number ~ '^[0-9]{4}$');

-- BR-KSA-66: exactly five digits.
ALTER TABLE store
  ADD CONSTRAINT store_postal_code_format
    CHECK (postal_code IS NULL OR postal_code ~ '^[0-9]{5}$');

ALTER TABLE store
  ADD CONSTRAINT store_country_code_format
    CHECK (country_code IS NULL OR country_code ~ '^[A-Z]{2}$');

COMMENT ON COLUMN store.street IS
  'BT-35. Street name, required on an invoice by BR-KSA-09.';
COMMENT ON COLUMN store.building_number IS
  'KSA-17. Four digits, per BR-KSA-37.';
COMMENT ON COLUMN store.additional_number IS
  'Saudi National Address secondary number. Four digits. Optional: no BR rule requires it.';
COMMENT ON COLUMN store.district IS
  'KSA-3. Rendered as cbc:CitySubdivisionName.';
COMMENT ON COLUMN store.city IS
  'BT-37.';
COMMENT ON COLUMN store.postal_code IS
  'BT-38. Five digits, per BR-KSA-66.';
COMMENT ON COLUMN store.country_code IS
  'BT-40. ISO 3166-1 alpha-2.';
COMMENT ON COLUMN store.address IS
  'Free text, kept from before the structured fields existed. NOT used on an '
  'invoice: ZATCA needs the six separate fields above.';
