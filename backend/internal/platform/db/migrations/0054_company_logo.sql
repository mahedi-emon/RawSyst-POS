-- 0054 — A client's own logo, blueprint I2.
--
-- I2 makes the logo customizable per template type. This migration builds only
-- the half that can honestly be built today: storing one logo per company, so a
-- client sets their own branding through the Back Office without anybody
-- editing source. Nothing renders it yet — the only document surface that
-- exists is `receipt.ts`, a 42-column PLAIN TEXT thermal receipt that cannot
-- carry an image by design, and no A4 or PDF surface exists at all. The
-- rendering half arrives with the first template that can hold a picture.
--
-- # Why the bytes live in Postgres
--
-- J1 names an S3-compatible object-storage abstraction for images, and that is
-- the right home for product photographs and invoice PDFs at volume. It is also
-- not built, and standing one up needs a provider decision and credentials that
-- are not a logo's to make.
--
-- A logo is a different shape of object from the ones J1 is about: one per
-- company, capped here at 512 KB, read rarely and written almost never. Holding
-- it in the row gives tenant isolation from the same row-level security that
-- protects every other table — which is the requirement that actually matters —
-- and puts it in the backup with the company it belongs to rather than in a
-- second system that has to be restored in step. When the object store lands,
-- moving these rows into it is a migration of a few hundred kilobytes per
-- tenant, not a redesign.
--
-- # One row per company
--
-- The primary key is the company, so replacing a logo is an upsert and removing
-- one is a delete. There is no history: a logo is current configuration, not a
-- posted document, and keeping every version a client ever tried would grow the
-- table with things nobody will look at. Documents already issued are unaffected
-- either way — nothing stamps a logo into them today, and when something does it
-- must resolve the logo as of issue, not follow this row.

CREATE TABLE company_logo (
  -- One per company. A group with three companies has three logos, because
  -- each is a separate legal entity issuing its own invoices under its own VAT
  -- registration (F4).
  company_id   uuid PRIMARY KEY REFERENCES company(id) ON DELETE CASCADE,
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,

  -- Determined by SNIFFING the bytes, never taken from what the client said it
  -- was uploading. A caller that could name its own content type could have a
  -- browser render whatever it liked from this origin.
  content_type text NOT NULL,
  bytes        bytea NOT NULL,
  byte_size    integer NOT NULL,

  -- Read out of the image header, so a screen can lay it out and an owner can
  -- be told why an upload was refused in terms they can act on.
  width        integer NOT NULL,
  height       integer NOT NULL,

  -- SHA-256 of the bytes. Serves as the cache validator on the read route, so a
  -- till that already has this logo is answered with a 304 rather than the
  -- image again.
  checksum     text NOT NULL,

  uploaded_by  uuid NOT NULL REFERENCES app_user(id) ON DELETE RESTRICT,
  uploaded_at  timestamptz NOT NULL DEFAULT now(),

  -- PNG and JPEG only, and SVG deliberately absent. An SVG is executable markup
  -- — it can carry script and external references — and serving one a client
  -- uploaded from this origin is a stored cross-site-scripting vector. The two
  -- raster formats cover every logo a shop actually has, and a client with only
  -- an SVG exports it once rather than the product accepting a live document as
  -- an image.
  CONSTRAINT company_logo_type_valid
    CHECK (content_type IN ('image/png', 'image/jpeg')),

  -- 512 KB. A logo far larger than this is a photograph somebody has mistaken
  -- for a mark, and it would be reprinted on every receipt.
  CONSTRAINT company_logo_size_sane
    CHECK (byte_size > 0 AND byte_size <= 524288),

  -- Bounds rather than an exact size: a logo is laid out to fit, and dictating
  -- one shape would refuse perfectly good marks. The floor exists because
  -- something smaller than 32px is not a logo, and the ceiling because a
  -- 4000px image on an 80mm roll is 512 KB spent on detail no printer resolves.
  CONSTRAINT company_logo_dimensions_sane
    CHECK (width BETWEEN 32 AND 2048 AND height BETWEEN 32 AND 2048),

  CONSTRAINT company_logo_checksum_format CHECK (checksum ~ '^[0-9a-f]{64}$')
);

-- The isolation that makes this table safe to hold bytes at all. FORCE, so the
-- policy applies to the table owner too — without it the application role's own
-- tables would be exempt and one client's mark would be readable by another.
ALTER TABLE company_logo ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_logo FORCE  ROW LEVEL SECURITY;

CREATE POLICY company_logo_isolation ON company_logo
  USING (tenant_id = current_tenant_id());

-- Deliberately NOT on the platform plane. A Super Admin has no business reading
-- a client's branding, and 0006's rule is that business tables carry no
-- platform predicate — TestPlatformAdminHasNoBusinessDataAccess enforces it.

COMMENT ON TABLE company_logo IS
  'Blueprint I2: the client''s own logo, one per company. Storage only until a '
  'template surface exists that can render an image.';
