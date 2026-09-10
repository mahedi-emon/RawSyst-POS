-- 0133 — the document a legal value came out of.
--
-- ---------------------------------------------------------------------------
-- What was missing
-- ---------------------------------------------------------------------------
--
-- `regulatory_rule` has recorded WHERE a figure came from since 0004:
-- `source_authority`, `source_document` and `source_url`. Those are a citation.
-- They are what somebody typed about a document, and they are not the document.
--
-- That distinction is the whole of this migration. A citation cannot answer:
--
--   * what did that page actually say on the day it was read?
--   * has it changed since?
--   * which sentence supports this particular field?
--   * did a person transcribe the figure, or did the software read it?
--
-- Every one of those is asked of a regulatory value eventually, usually by
-- somebody who was not there. `regulatory_source_document` keeps the artefact
-- itself — the bytes that were fetched or uploaded, their SHA-256, the URL they
-- came from and the moment they were retrieved — and the extraction that was
-- taken out of it, field by field, with the sentence that supports each one.
--
-- ---------------------------------------------------------------------------
-- Why the bytes are stored and not just the hash
-- ---------------------------------------------------------------------------
--
-- A hash proves a document has not changed. It cannot show anybody what it
-- said. Storing the hash alone would mean that the moment a ministry reorganises
-- its website — which is a thing ministries do — this installation could prove
-- its figures came from something, and could no longer show what.
--
-- The artefact is small: the Labour Law as the Ministry publishes it is a third
-- of a megabyte. A regulatory source is fetched at most a handful of times a
-- year per rule, so this table grows by kilobytes, not by rows per transaction.
--
-- ---------------------------------------------------------------------------
-- Why a document is immutable once written
-- ---------------------------------------------------------------------------
--
-- The point of the row is that it is evidence. Evidence that can be edited is
-- an assertion with extra steps. `status` moves, and the applied and rejected
-- bookkeeping is filled in once; the bytes, the hash, the URL, the retrieval
-- date and the extraction cannot be touched afterwards. A corrected reading is
-- a NEW retrieval of the document, which is also the honest description of what
-- happened.
--
-- ---------------------------------------------------------------------------
-- Global, like the rule it supports
-- ---------------------------------------------------------------------------
--
-- No `tenant_id`. The Saudi Labour Law is not one business's setting, and a
-- tenant that could add a document behind a legal value would be choosing what
-- it owes. Platform only, through TxAsPlatform, exactly as `regulatory_rule`.

CREATE TABLE regulatory_source_document (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  -- Which rule this document is evidence for. Not a foreign key: a document is
  -- retrieved BEFORE the rule version it will support exists, and the whole
  -- workflow depends on being able to hold a candidate that has not been
  -- applied to anything yet.
  rule_key         text    NOT NULL,
  country          char(2) NOT NULL,

  -- text with a foreign key, matching `regulatory_rule.source_authority`.
  --
  -- Not the type named `regulatory_authority`: 0004 created that as an enum,
  -- 0010 replaced it with a lookup table of the same name, and the table's row
  -- type still answers to it. A column declared with it takes a composite
  -- literal and refuses 'mhrsd' with "malformed record literal", which is a
  -- confusing way to find out you have referenced the wrong thing.
  source_authority text NOT NULL
    REFERENCES regulatory_authority(code),

  -- What the document calls itself, as printed on it where possible.
  title            text NOT NULL,

  -- Where it came from. NULL only for an upload, where the operator supplied
  -- the bytes directly and there is no address to re-fetch.
  url              text,
  origin           text NOT NULL,

  media_type       text   NOT NULL,
  byte_size        bigint NOT NULL,
  content_sha256   char(64) NOT NULL,
  content          bytea  NOT NULL,

  retrieved_on     timestamptz NOT NULL DEFAULT now(),
  retrieved_by     uuid REFERENCES app_user(id),

  -- What was read out of it: field name -> {value, article, evidence}. NULL
  -- while extraction has not been attempted; `extraction_error` says why when
  -- it was attempted and failed.
  --
  -- Derived, and deliberately NOT frozen with the rest of the row. A reading is
  -- a function of the document AND the build that read it, and this product's
  -- readings improve — the first version of the Saudi reader found six of
  -- Article 85's seven figures because the Ministry's PDF prints "the ful l
  -- award" with the word broken in half. Freezing the reading would have meant
  -- that a fixed reader could never be applied to a document already retrieved,
  -- because the same bytes cannot be retrieved twice.
  --
  -- Nothing evidential is lost by that. The bytes and their hash are frozen, so
  -- the reading can always be re-derived and checked; and what was read AT THE
  -- MOMENT OF APPLYING is captured permanently in the rule's own payload and in
  -- the audit entry beside it.
  extracted        jsonb,
  extraction_error text,

  status           text NOT NULL DEFAULT 'candidate',

  applied_rule_id  uuid REFERENCES regulatory_rule(id),
  applied_at       timestamptz,
  applied_by       uuid REFERENCES app_user(id),

  -- Why a candidate was rejected, for the operator who finds it later and
  -- wonders whether to try again.
  rejected_reason  text,

  notes            text,

  CONSTRAINT regulatory_source_document_key_format
    CHECK (rule_key ~ '^[A-Z]{2}\.[A-Z0-9_]+\.[A-Z0-9_]+$'),
  CONSTRAINT regulatory_source_document_country_lower
    CHECK (country = lower(country)),
  CONSTRAINT regulatory_source_document_origin_known
    CHECK (origin IN ('fetch', 'upload', 'refresh')),
  CONSTRAINT regulatory_source_document_status_known
    CHECK (status IN ('candidate', 'applied', 'superseded', 'rejected')),
  CONSTRAINT regulatory_source_document_hash_lower
    CHECK (content_sha256 = lower(content_sha256)),
  CONSTRAINT regulatory_source_document_size_positive
    CHECK (byte_size > 0),

  -- An upload has no address; a fetch must have one, or nothing can ever check
  -- whether the source has changed.
  CONSTRAINT regulatory_source_document_fetch_has_url
    CHECK (origin = 'upload' OR url IS NOT NULL),

  -- Applied means applied TO something, at a time, by somebody.
  CONSTRAINT regulatory_source_document_applied_is_complete
    CHECK (status <> 'applied'
           OR (applied_rule_id IS NOT NULL AND applied_at IS NOT NULL))
);

COMMENT ON TABLE regulatory_source_document IS
  'The artefact a legal value was read out of: the bytes as retrieved, their '
  'SHA-256, where and when they came from, and the extraction taken out of '
  'them field by field. Immutable once written; a corrected reading is a new '
  'retrieval.';

COMMENT ON COLUMN regulatory_source_document.origin IS
  'fetch = the platform retrieved it from the authority''s address on request; '
  'upload = an operator supplied the bytes; refresh = the scheduled job '
  'retrieved it again and found it changed.';

COMMENT ON COLUMN regulatory_source_document.extracted IS
  'field name -> {value, article, evidence, derived}. `evidence` is the '
  'sentence the value was read out of, so a reader can check the software''s '
  'reading against the document without trusting it.';

CREATE INDEX regulatory_source_document_by_rule
  ON regulatory_source_document (rule_key, country, retrieved_on DESC);

-- One row per distinct document per rule.
--
-- Re-fetching an unchanged page must not fill the table with identical copies:
-- the refresh job exists precisely to run repeatedly, and a source that has not
-- changed is the expected outcome every time it does.
CREATE UNIQUE INDEX regulatory_source_document_one_per_hash
  ON regulatory_source_document (rule_key, country, content_sha256);

-- ---------------------------------------------------------------------------
-- Immutability
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION regulatory_source_document_frozen()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.rule_key       IS DISTINCT FROM OLD.rule_key
  OR NEW.country        IS DISTINCT FROM OLD.country
  OR NEW.url            IS DISTINCT FROM OLD.url
  OR NEW.origin         IS DISTINCT FROM OLD.origin
  OR NEW.media_type     IS DISTINCT FROM OLD.media_type
  OR NEW.byte_size      IS DISTINCT FROM OLD.byte_size
  OR NEW.content_sha256 IS DISTINCT FROM OLD.content_sha256
  OR NEW.content        IS DISTINCT FROM OLD.content
  OR NEW.retrieved_on   IS DISTINCT FROM OLD.retrieved_on
  OR NEW.retrieved_by   IS DISTINCT FROM OLD.retrieved_by
  THEN
    RAISE EXCEPTION
      'A regulatory source document is evidence and cannot be edited. '
      'Retrieve the document again to record a different reading of it.';
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER regulatory_source_document_frozen_fields
  BEFORE UPDATE ON regulatory_source_document
  FOR EACH ROW EXECUTE FUNCTION regulatory_source_document_frozen();

CREATE OR REPLACE FUNCTION regulatory_source_document_no_delete()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION
    'A regulatory source document cannot be deleted. A rule recorded from it '
    'cites it, and a citation whose document has been removed is worse than '
    'no citation. Reject it instead.';
END $$;

CREATE TRIGGER regulatory_source_document_no_delete_trigger
  BEFORE DELETE ON regulatory_source_document
  FOR EACH ROW EXECUTE FUNCTION regulatory_source_document_no_delete();

-- ---------------------------------------------------------------------------
-- The rule points back at its evidence
-- ---------------------------------------------------------------------------
--
-- Nullable, and it stays nullable. Forty-four rules were seeded by migration
-- from documents nobody retrieved through this workflow, and back-filling a
-- document id for them would be inventing provenance — the exact thing this
-- table exists to make impossible.

ALTER TABLE regulatory_rule
  ADD COLUMN source_document_id uuid REFERENCES regulatory_source_document(id);

COMMENT ON COLUMN regulatory_rule.source_document_id IS
  'The retrieved artefact this figure was read out of, where the value came '
  'through the source-document workflow. NULL for a rule seeded by migration '
  'or recorded by hand from a document held elsewhere.';

-- The frozen-fields trigger on regulatory_rule guards the columns that make a
-- rule what it is. The new column is written once at INSERT and never after,
-- so it needs no addition there; it is listed here so the next reader of that
-- trigger knows it was considered rather than forgotten.
