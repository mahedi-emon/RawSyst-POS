-- 0096 — Document management, the PDPL module, and the storefront disclosures
--        an online shop is required to publish (blueprint D6, E4, E5).
--
-- # Why one migration
--
-- These three are one subject seen from three angles. D6 is where a copy of a
-- customer's ID lives when a shop sells on instalments; E4 is the law that says
-- what may be done with it, for how long, and what happens when the customer
-- asks for it back; E5 is the same law's consumer-facing half — the shop has to
-- SAY, before it takes the money, what it will do. Splitting them would put the
-- classification of a document in one migration and the meaning of the
-- classification in another.
--
-- # A document is stored in the row, like the logo
--
-- 0054 argued this for company_logo and the argument holds here unchanged: the
-- object store J1 names is not built, standing one up needs a provider decision,
-- and a document in the row is protected by the same row-level security as the
-- record it is attached to and lands in the same backup. The cap is larger —
-- 8 MB rather than 512 KB, because a scanned CR certificate is not a logo — and
-- the storage a tenant may use in total is already governed by
-- tenant_limit.max_storage_mb.
--
-- # Consent is a ledger, not a checkbox
--
-- E4.1: "Marketing consent must be recorded separately from transactional
-- consent, with timestamp, channel, and proof — this is the single most
-- commonly enforced violation." A boolean on `customer` would answer "may we
-- text them today" and nothing else. SDAIA asks a different question: prove
-- they agreed, on what date, through what channel, and show that you stopped
-- when they withdrew. So consent is an append-mostly ledger, one row per
-- (subject, purpose, channel) grant, withdrawn by stamping rather than
-- deleting — a deleted grant is indistinguishable from one that never existed,
-- which is the wrong side of an audit to be on.
--
-- # The clocks are data, not code
--
-- Two deadlines matter and both already live in the registry, seeded in 0004:
-- SA.PDPL.DSR_RESPONSE_DAYS ({"days": 30, "extension_days": 30}) and
-- SA.PDPL.BREACH_NOTIFICATION_HOURS ({"hours": 72}). The columns here store the
-- RESOLVED deadline on the row, computed once when the request or incident is
-- opened. That is deliberate: a regulation that changes next year must not
-- silently move the due date of a request already running, and an auditor
-- asking "why was this due on the 14th" deserves an answer that does not
-- require replaying the registry.
--
-- # Retention beats deletion, and the law beats both
--
-- E4.1 ends on the conflict: a customer may ask to be erased, and E2.4 requires
-- the tax records that name them to be kept for six years. `legal_hold` is how
-- that conflict is resolved explicitly — a request that collides with a hold is
-- recorded as partially refused WITH the reason, rather than either deleting
-- records the Zakat authority will ask for or quietly ignoring the customer.

-- ---------------------------------------------------------------------------
-- Data classification (E4.1)
-- ---------------------------------------------------------------------------

-- Every field and every document carries one of these, so protection rules can
-- be applied by class instead of by remembering which column held a passport
-- number. The four names are E4.1's, unchanged.
CREATE TYPE data_class AS ENUM (
  'public',            -- a product name
  'internal',          -- a purchase price
  'personal',          -- a customer's phone number
  'sensitive_personal' -- an ID copy, a health note on a service job
);

-- ---------------------------------------------------------------------------
-- D6 — Document management
-- ---------------------------------------------------------------------------

CREATE TABLE document (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- What it is attached to. D6 lists purchase invoices, expense receipts,
  -- supplier documents, customer documents, employee documents, warranty
  -- documents and asset documents. Free text with a CHECK rather than an enum:
  -- an enum would need a migration every time a module becomes attachable, and
  -- the CHECK still refuses a typo.
  entity_type  text NOT NULL,
  entity_id    uuid NOT NULL,

  file_name    text NOT NULL,

  -- Sniffed from the bytes, never taken from what the uploader claimed. The
  -- same reasoning as company_logo: a caller that names its own content type
  -- can have a browser render whatever it likes from this origin.
  content_type text NOT NULL,
  byte_size    integer NOT NULL,
  checksum     text NOT NULL,
  bytes        bytea NOT NULL,

  -- E4.1's classification, applied to the whole file. An ID copy attached to an
  -- instalment agreement is sensitive_personal and the read route says so.
  classification data_class NOT NULL DEFAULT 'internal',

  -- Supplier compliance documents and employee documents expire, and E7 wants
  -- to warn before they do. NULL means "does not expire", which is the honest
  -- default for a delivery note.
  expires_on   date,

  note         text,
  uploaded_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT document_entity_valid CHECK (entity_type IN (
    'purchase_invoice', 'purchase_order', 'goods_receipt', 'expense',
    'supplier', 'customer', 'employee', 'service_job', 'warranty',
    'asset', 'sales_invoice', 'sales_order', 'installment_plan',
    'incident', 'data_subject_request', 'company')),
  CONSTRAINT document_name_not_blank CHECK (btrim(file_name) <> ''),
  CONSTRAINT document_size_sane CHECK (byte_size > 0 AND byte_size <= 8388608),
  CONSTRAINT document_size_matches CHECK (byte_size = length(bytes))
);

CREATE INDEX document_entity_idx
  ON document (company_id, entity_type, entity_id, created_at DESC);
CREATE INDEX document_tenant_idx ON document (tenant_id);
-- D6 asks for the store to be searchable, and E7 wants expiring documents
-- surfaced before they lapse.
CREATE INDEX document_name_trgm_idx ON document USING gin (file_name gin_trgm_ops);
CREATE INDEX document_expiry_idx ON document (company_id, expires_on)
  WHERE expires_on IS NOT NULL;

ALTER TABLE document ENABLE ROW LEVEL SECURITY;
ALTER TABLE document FORCE  ROW LEVEL SECURITY;
CREATE POLICY document_isolation ON document
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- E4 — Consent and lawful basis
-- ---------------------------------------------------------------------------

CREATE TABLE privacy_consent (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  subject_type text NOT NULL,   -- customer, employee, supplier_contact
  subject_id   uuid NOT NULL,

  -- WHY the data is held. E4.1's four bases, plus the two the PDPL
  -- Implementing Regulation adds for completeness.
  lawful_basis text NOT NULL,

  -- WHAT it is used for. Marketing is separate from transactional by
  -- construction — they are different rows, so one can be withdrawn without
  -- touching the other, which is exactly the failure E4.1 names.
  purpose      text NOT NULL,

  -- HOW they may be reached for it. A customer may accept email marketing and
  -- refuse SMS, and a single boolean cannot hold that.
  channel      text NOT NULL,

  granted      boolean NOT NULL DEFAULT true,
  granted_at   timestamptz NOT NULL DEFAULT now(),
  withdrawn_at timestamptz,

  -- The proof E4.1 requires: how the agreement was obtained. "Signed
  -- instalment agreement 2026-03-04", "till prompt, receipt 41822", "web form,
  -- IP recorded". Free text because the proof differs per channel, and
  -- required because an unevidenced consent is the violation.
  proof        text NOT NULL,

  recorded_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT privacy_consent_subject_valid CHECK (subject_type IN (
    'customer', 'employee', 'supplier_contact')),
  CONSTRAINT privacy_consent_basis_valid CHECK (lawful_basis IN (
    'consent', 'contract', 'legal_obligation', 'legitimate_interest',
    'vital_interest', 'public_interest')),
  CONSTRAINT privacy_consent_purpose_valid CHECK (purpose IN (
    'transactional', 'marketing', 'profiling', 'loyalty', 'credit_assessment')),
  CONSTRAINT privacy_consent_channel_valid CHECK (channel IN (
    'sms', 'email', 'whatsapp', 'phone', 'post', 'in_app', 'any')),
  CONSTRAINT privacy_consent_proof_not_blank CHECK (btrim(proof) <> ''),
  -- A withdrawn grant is stamped, not deleted. The two columns must agree.
  CONSTRAINT privacy_consent_withdrawal_consistent
    CHECK ((granted AND withdrawn_at IS NULL) OR
           (NOT granted AND withdrawn_at IS NOT NULL))
);

-- One live grant per subject, purpose and channel. Re-granting after a
-- withdrawal is a new row, and the partial index lets both exist.
CREATE UNIQUE INDEX privacy_consent_live_uq
  ON privacy_consent (company_id, subject_type, subject_id, purpose, channel)
  WHERE granted;
CREATE INDEX privacy_consent_subject_idx
  ON privacy_consent (company_id, subject_type, subject_id, granted_at DESC);
CREATE INDEX privacy_consent_tenant_idx ON privacy_consent (tenant_id);

CREATE TRIGGER privacy_consent_touch BEFORE UPDATE ON privacy_consent
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE privacy_consent ENABLE ROW LEVEL SECURITY;
ALTER TABLE privacy_consent FORCE  ROW LEVEL SECURITY;
CREATE POLICY privacy_consent_isolation ON privacy_consent
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- E4 — Data subject requests, with the SLA clock on the row
-- ---------------------------------------------------------------------------

CREATE TABLE data_subject_request (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  request_no   text NOT NULL,

  subject_type text NOT NULL,
  -- Nullable: somebody may write in asking what is held about them before
  -- anybody has worked out which customer record is theirs, and refusing to
  -- record the request until then would start the clock late.
  subject_id   uuid,
  subject_name text NOT NULL,
  subject_contact text NOT NULL,

  -- E4.1's six rights, named as the regulation names them.
  kind         text NOT NULL,

  status       text NOT NULL DEFAULT 'received',

  received_at  timestamptz NOT NULL DEFAULT now(),
  -- Resolved from SA.PDPL.DSR_RESPONSE_DAYS at the moment the request is
  -- opened, and then left alone. See the file note.
  due_at       timestamptz NOT NULL,
  -- The single further extension the Implementing Regulation allows, with the
  -- reason, because "extended" without a reason is not an extension.
  extended_to  timestamptz,
  extension_reason text,

  closed_at    timestamptz,
  -- fulfilled, partially_fulfilled, refused
  outcome      text,
  outcome_note text,

  -- Set when E2.4's six-year retention obligation blocked part of an erasure.
  -- The conflict is recorded rather than resolved silently in either
  -- direction.
  legal_hold_applied boolean NOT NULL DEFAULT false,

  handled_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  raised_by    uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT dsr_subject_valid CHECK (subject_type IN (
    'customer', 'employee', 'supplier_contact', 'unknown')),
  CONSTRAINT dsr_kind_valid CHECK (kind IN (
    'access', 'export', 'correction', 'deletion', 'objection', 'portability')),
  CONSTRAINT dsr_status_valid CHECK (status IN (
    'received', 'in_progress', 'awaiting_subject', 'extended',
    'fulfilled', 'refused')),
  CONSTRAINT dsr_outcome_valid CHECK (outcome IS NULL OR outcome IN (
    'fulfilled', 'partially_fulfilled', 'refused')),
  CONSTRAINT dsr_closed_has_outcome
    CHECK ((closed_at IS NULL) = (outcome IS NULL)),
  CONSTRAINT dsr_extension_has_reason
    CHECK (extended_to IS NULL OR btrim(coalesce(extension_reason, '')) <> ''),
  CONSTRAINT dsr_name_not_blank CHECK (btrim(subject_name) <> '')
);

CREATE UNIQUE INDEX dsr_no_uq ON data_subject_request (company_id, request_no);
-- The queue screen and the "about to breach" warning are both this index.
CREATE INDEX dsr_open_idx
  ON data_subject_request (company_id, coalesce(extended_to, due_at))
  WHERE closed_at IS NULL;
CREATE INDEX dsr_tenant_idx ON data_subject_request (tenant_id);

CREATE TRIGGER dsr_touch BEFORE UPDATE ON data_subject_request
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE data_subject_request ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_subject_request FORCE  ROW LEVEL SECURITY;
CREATE POLICY dsr_isolation ON data_subject_request
  USING (tenant_id = current_tenant_id());

CREATE SEQUENCE IF NOT EXISTS data_subject_request_seq START 1;

-- ---------------------------------------------------------------------------
-- E4 — Breach and incident management, with the 72-hour countdown
-- ---------------------------------------------------------------------------

CREATE TABLE privacy_incident (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  incident_no  text NOT NULL,
  title        text NOT NULL,

  -- E4.1 names exactly what the notification must contain, so each part is its
  -- own column rather than one description field somebody fills in halfway.
  what_happened   text NOT NULL,
  data_categories text NOT NULL,
  subjects_affected integer,
  consequences    text,
  containment     text,

  severity     text NOT NULL DEFAULT 'medium',
  status       text NOT NULL DEFAULT 'open',

  -- "the moment of becoming aware", which is not the moment the breach
  -- happened and not necessarily the moment somebody typed it in.
  discovered_at timestamptz NOT NULL,
  -- discovered_at + SA.PDPL.BREACH_NOTIFICATION_HOURS, resolved once. The
  -- countdown the blueprint asks for is this column minus now().
  notify_due_at timestamptz NOT NULL,

  sdaia_notified_at    timestamptz,
  subjects_notified_at timestamptz,

  closed_at    timestamptz,
  logged_by    uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT incident_severity_valid CHECK (severity IN (
    'low', 'medium', 'high', 'critical')),
  CONSTRAINT incident_status_valid CHECK (status IN (
    'open', 'contained', 'notified', 'closed')),
  CONSTRAINT incident_title_not_blank CHECK (btrim(title) <> ''),
  CONSTRAINT incident_subjects_sane
    CHECK (subjects_affected IS NULL OR subjects_affected >= 0),
  CONSTRAINT incident_closed_is_stamped
    CHECK ((status = 'closed') = (closed_at IS NOT NULL))
);

CREATE UNIQUE INDEX incident_no_uq ON privacy_incident (company_id, incident_no);
CREATE INDEX incident_open_idx ON privacy_incident (company_id, notify_due_at)
  WHERE closed_at IS NULL;
CREATE INDEX incident_tenant_idx ON privacy_incident (tenant_id);

CREATE TRIGGER incident_touch BEFORE UPDATE ON privacy_incident
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE privacy_incident ENABLE ROW LEVEL SECURITY;
ALTER TABLE privacy_incident FORCE  ROW LEVEL SECURITY;
CREATE POLICY incident_isolation ON privacy_incident
  USING (tenant_id = current_tenant_id());

CREATE SEQUENCE IF NOT EXISTS privacy_incident_seq START 1;

-- ---------------------------------------------------------------------------
-- E4 — Record of Processing Activities
-- ---------------------------------------------------------------------------

-- "producible on short notice; SDAIA audits have specifically requested
-- these." A RoPA is a document in every other product, which means it is out
-- of date the day after it is written. Here it is a table, so the export is
-- generated from what the shop actually configured.
CREATE TABLE processing_activity (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  name         text NOT NULL,
  purpose      text NOT NULL,
  lawful_basis text NOT NULL,

  data_categories    text NOT NULL,
  subject_categories text NOT NULL,
  recipients         text,

  -- E4.2. A tenant whose data never leaves the Kingdom answers "no" and the
  -- rest stays empty; one that uses an overseas SMS provider has to name the
  -- destination and the safeguard.
  cross_border        boolean NOT NULL DEFAULT false,
  destination_country text,
  transfer_safeguard  text,

  retention_note text,
  system_name    text,
  owner_name     text,
  reviewed_on    date,

  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT ropa_basis_valid CHECK (lawful_basis IN (
    'consent', 'contract', 'legal_obligation', 'legitimate_interest',
    'vital_interest', 'public_interest')),
  CONSTRAINT ropa_name_not_blank CHECK (btrim(name) <> ''),
  CONSTRAINT ropa_transfer_is_described CHECK (
    NOT cross_border OR (
      btrim(coalesce(destination_country, '')) <> '' AND
      btrim(coalesce(transfer_safeguard, '')) <> ''))
);

CREATE UNIQUE INDEX ropa_name_uq ON processing_activity (company_id, lower(name));
CREATE INDEX ropa_tenant_idx ON processing_activity (tenant_id);

CREATE TRIGGER ropa_touch BEFORE UPDATE ON processing_activity
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE processing_activity ENABLE ROW LEVEL SECURITY;
ALTER TABLE processing_activity FORCE  ROW LEVEL SECURITY;
CREATE POLICY ropa_isolation ON processing_activity
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- E4 — Retention, legal hold, and the destruction log
-- ---------------------------------------------------------------------------

CREATE TABLE retention_policy (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  data_category text NOT NULL,
  retain_months integer NOT NULL,
  -- archive → anonymise → destroy, E4.1's own progression.
  action        text NOT NULL DEFAULT 'anonymize',
  legal_note    text,
  is_active     boolean NOT NULL DEFAULT true,
  last_run_at   timestamptz,

  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT retention_action_valid CHECK (action IN (
    'archive', 'anonymize', 'destroy')),
  CONSTRAINT retention_months_sane CHECK (retain_months BETWEEN 1 AND 600),
  CONSTRAINT retention_category_not_blank CHECK (btrim(data_category) <> '')
);

CREATE UNIQUE INDEX retention_category_uq
  ON retention_policy (company_id, lower(data_category));
CREATE INDEX retention_tenant_idx ON retention_policy (tenant_id);

CREATE TRIGGER retention_touch BEFORE UPDATE ON retention_policy
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE retention_policy ENABLE ROW LEVEL SECURITY;
ALTER TABLE retention_policy FORCE  ROW LEVEL SECURITY;
CREATE POLICY retention_isolation ON retention_policy
  USING (tenant_id = current_tenant_id());

CREATE TABLE legal_hold (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  name         text NOT NULL,
  -- NOT NULL: a hold that stops a customer being erased has to be defensible
  -- to that customer, and "because" is not defensible.
  reason       text NOT NULL,

  -- Either a named subject or a whole category. A tax audit holds the
  -- category; a live dispute holds one person.
  subject_type text,
  subject_id   uuid,
  data_category text,

  placed_at    timestamptz NOT NULL DEFAULT now(),
  released_at  timestamptz,
  placed_by    uuid REFERENCES app_user(id) ON DELETE SET NULL,

  CONSTRAINT legal_hold_targets_something CHECK (
    subject_id IS NOT NULL OR btrim(coalesce(data_category, '')) <> ''),
  CONSTRAINT legal_hold_subject_pairs
    CHECK ((subject_id IS NULL) = (subject_type IS NULL)),
  CONSTRAINT legal_hold_reason_not_blank CHECK (btrim(reason) <> '')
);

CREATE INDEX legal_hold_live_idx
  ON legal_hold (company_id, subject_type, subject_id)
  WHERE released_at IS NULL;
CREATE INDEX legal_hold_tenant_idx ON legal_hold (tenant_id);

ALTER TABLE legal_hold ENABLE ROW LEVEL SECURITY;
ALTER TABLE legal_hold FORCE  ROW LEVEL SECURITY;
CREATE POLICY legal_hold_isolation ON legal_hold
  USING (tenant_id = current_tenant_id());

-- The permanent proof E4.1 asks for. Append-only by trigger: a destruction log
-- that can be edited proves nothing at all.
CREATE TABLE destruction_log (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  data_category text NOT NULL,
  entity_type   text,
  entity_id     uuid,

  action       text NOT NULL,
  row_count    integer NOT NULL DEFAULT 1,
  -- What set it off: a retention policy running, or a specific erasure
  -- request. Both are legitimate and an auditor will ask which.
  reason       text NOT NULL,
  request_id   uuid REFERENCES data_subject_request(id) ON DELETE SET NULL,

  executed_at  timestamptz NOT NULL DEFAULT now(),
  executed_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,

  CONSTRAINT destruction_action_valid CHECK (action IN (
    'archive', 'anonymize', 'destroy')),
  CONSTRAINT destruction_rows_sane CHECK (row_count >= 0),
  CONSTRAINT destruction_reason_not_blank CHECK (btrim(reason) <> '')
);

CREATE INDEX destruction_log_idx
  ON destruction_log (company_id, executed_at DESC);
CREATE INDEX destruction_log_tenant_idx ON destruction_log (tenant_id);

ALTER TABLE destruction_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE destruction_log FORCE  ROW LEVEL SECURITY;
CREATE POLICY destruction_log_isolation ON destruction_log
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER destruction_log_no_change
  BEFORE UPDATE OR DELETE ON destruction_log
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- E4 — Per-company privacy posture (DPO, SDAIA registration)
-- ---------------------------------------------------------------------------

CREATE TABLE privacy_settings (
  company_id   uuid PRIMARY KEY REFERENCES company(id) ON DELETE CASCADE,
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,

  -- E4.1: "allow assigning an internal or external Data Protection Officer per
  -- tenant". Per company rather than per tenant, because a group's entities
  -- can appoint different officers and E4's obligations attach to the
  -- controller, which is the legal entity.
  dpo_name     text,
  dpo_email    text,
  dpo_phone    text,
  dpo_external boolean NOT NULL DEFAULT false,

  -- E4.2: captured so it can be evidenced during an audit.
  sdaia_registration_ref text,
  controller_registered_on date,

  privacy_notice_url text,

  updated_at   timestamptz NOT NULL DEFAULT now(),
  updated_by   uuid REFERENCES app_user(id) ON DELETE SET NULL
);

CREATE INDEX privacy_settings_tenant_idx ON privacy_settings (tenant_id);

CREATE TRIGGER privacy_settings_touch BEFORE UPDATE ON privacy_settings
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE privacy_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE privacy_settings FORCE  ROW LEVEL SECURITY;
CREATE POLICY privacy_settings_isolation ON privacy_settings
  USING (tenant_id = current_tenant_id());

-- The platform's own PDPL posture. E4.1's last bullet: the platform operator is
-- a PROCESSOR for every tenant and needs sub-processor records for each cloud,
-- SMS and email vendor it uses. One list, held by the platform, visible to
-- tenants because a tenant's own RoPA has to name them.
CREATE TABLE subprocessor (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name         text NOT NULL,
  purpose      text NOT NULL,
  country      char(2) NOT NULL,
  data_categories text NOT NULL,
  safeguard    text,
  dpa_signed_on date,
  is_active    boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT subprocessor_name_not_blank CHECK (btrim(name) <> ''),
  CONSTRAINT subprocessor_country_lower CHECK (country = lower(country))
);

CREATE UNIQUE INDEX subprocessor_name_uq ON subprocessor (lower(name));

CREATE TRIGGER subprocessor_touch BEFORE UPDATE ON subprocessor
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- No tenant column, so no isolation predicate to write: the list is the same
-- for everybody and a tenant reading it is reading the platform's disclosure
-- about itself. Writes are guarded at the route by AccessSuperAdmin.
ALTER TABLE subprocessor ENABLE ROW LEVEL SECURITY;
ALTER TABLE subprocessor FORCE  ROW LEVEL SECURITY;
CREATE POLICY subprocessor_readable ON subprocessor USING (true);

-- ---------------------------------------------------------------------------
-- E5 — Storefront disclosures for the online channel
-- ---------------------------------------------------------------------------

-- E5 makes these a REQUIRED, VALIDATED onboarding step rather than an optional
-- settings field, because the penalties run to roughly SAR 1,000,000 and a
-- missing return policy is the easiest violation in the list to commit.
--
-- The CR number and VAT number are NOT repeated here — they are on `company`
-- already, and a second copy would be a second thing to keep true. What lives
-- here is what only the online channel needs.
CREATE TABLE storefront_disclosure (
  company_id   uuid PRIMARY KEY REFERENCES company(id) ON DELETE CASCADE,
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,

  -- E5: the registration channel has been moving between Maroof and
  -- business.sa, so the reference and the channel that issued it are both
  -- configurable per tenant. A code change to follow a government portal
  -- rename is exactly what this avoids.
  registration_ref     text,
  registration_channel text,
  verification_badge_url text,

  -- Rendered on the storefront, in Arabic, from tenant settings. Both
  -- languages are held because E5 requires Arabic and the shop will want
  -- English beside it.
  return_policy      text,
  return_policy_ar   text,
  delivery_terms     text,
  delivery_terms_ar  text,

  contact_email text,
  contact_phone text,
  support_hours text,

  -- SA.ECOMMERCE.COOLING_OFF_DAYS is 14 and lives in the registry; this is the
  -- shop's own figure, which may be MORE generous but is validated against the
  -- registry floor in the service rather than here, where the registry is not
  -- reachable.
  cooling_off_days smallint,

  updated_at   timestamptz NOT NULL DEFAULT now(),
  updated_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,

  CONSTRAINT storefront_cooling_off_sane
    CHECK (cooling_off_days IS NULL OR cooling_off_days BETWEEN 0 AND 365)
);

CREATE INDEX storefront_disclosure_tenant_idx ON storefront_disclosure (tenant_id);

CREATE TRIGGER storefront_disclosure_touch BEFORE UPDATE ON storefront_disclosure
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE storefront_disclosure ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_disclosure FORCE  ROW LEVEL SECURITY;
CREATE POLICY storefront_disclosure_isolation ON storefront_disclosure
  USING (tenant_id = current_tenant_id());

-- E5: "14-day cooling-off / return right for most categories (configurable per
-- product/category, since some categories are legally exempt)". Both columns
-- are nullable and mean "follow the company's figure", so an existing catalogue
-- keeps behaving exactly as it did.
ALTER TABLE product
  ADD COLUMN return_window_days smallint,
  ADD COLUMN return_exempt      boolean NOT NULL DEFAULT false;

ALTER TABLE product ADD CONSTRAINT product_return_window_sane
  CHECK (return_window_days IS NULL OR return_window_days BETWEEN 0 AND 365);

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  -- D6. Anybody who can read the record can read what is attached to it; the
  -- classification, not the role, is what keeps an ID copy out of the wrong
  -- hands, and that is enforced on the read route.
  ('owner',            'document.view'),   ('owner',            'document.manage'),
  ('store_manager',    'document.view'),   ('store_manager',    'document.manage'),
  ('accountant',       'document.view'),   ('accountant',       'document.manage'),
  ('purchase_manager', 'document.view'),   ('purchase_manager', 'document.manage'),
  ('hr_manager',       'document.view'),   ('hr_manager',       'document.manage'),
  ('auditor',          'document.view'),

  -- E4. Deliberately narrow. A data-subject request names a person and says
  -- what they asked to have deleted; a cashier has no business in that queue.
  ('owner',            'privacy.view'),    ('owner',            'privacy.manage'),
  ('accountant',       'privacy.view'),
  ('hr_manager',       'privacy.view'),
  ('auditor',          'privacy.view'),

  -- E7 answers "am I legally exposed right now", which is an owner's question
  -- and an auditor's.
  ('owner',            'compliance.view'),
  ('accountant',       'compliance.view'),
  ('auditor',          'compliance.view')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

DO $$
DECLARE
  t record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    INSERT INTO role_permission (role_id, permission)
    SELECT r.id, p.permission
    FROM role r
    JOIN role template ON template.id = r.cloned_from
    JOIN (VALUES
      ('owner',            'document.view'),   ('owner',            'document.manage'),
      ('store_manager',    'document.view'),   ('store_manager',    'document.manage'),
      ('accountant',       'document.view'),   ('accountant',       'document.manage'),
      ('purchase_manager', 'document.view'),   ('purchase_manager', 'document.manage'),
      ('hr_manager',       'document.view'),   ('hr_manager',       'document.manage'),
      ('auditor',          'document.view'),
      ('owner',            'privacy.view'),    ('owner',            'privacy.manage'),
      ('accountant',       'privacy.view'),
      ('hr_manager',       'privacy.view'),
      ('auditor',          'privacy.view'),
      ('owner',            'compliance.view'),
      ('accountant',       'compliance.view'),
      ('auditor',          'compliance.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;

-- ---------------------------------------------------------------------------
-- What every shop starts with
-- ---------------------------------------------------------------------------

-- A RoPA nobody has written is worse than none: it looks like the question was
-- considered. These four are the processing every retail company here actually
-- does on day one, derived from what the product stores rather than from a
-- template, so an owner edits and extends rather than starting at a blank page.
DO $$
DECLARE
  t record;
  c record;
BEGIN
  PERFORM set_config('app.platform_admin', 'on', true);

  FOR t IN SELECT id FROM tenant LOOP
    PERFORM set_config('app.tenant_id', t.id::text, true);

    FOR c IN SELECT id FROM company WHERE tenant_id = t.id LOOP
      INSERT INTO processing_activity (
        tenant_id, company_id, name, purpose, lawful_basis,
        data_categories, subject_categories, recipients, retention_note,
        system_name)
      VALUES
        (t.id, c.id, 'Sales and invoicing',
         'Issuing invoices and meeting tax record obligations',
         'legal_obligation',
         'Name, phone number, VAT number where the buyer is a business',
         'Customers',
         'Tax authority on request',
         'Six years from the end of the tax year, per the record-retention rule',
         'RawSyst POS'),
        (t.id, c.id, 'Customer loyalty',
         'Awarding and redeeming points and store credit',
         'consent',
         'Name, phone number, purchase history',
         'Enrolled customers',
         NULL,
         'While the membership is active, then anonymised',
         'RawSyst POS'),
        (t.id, c.id, 'Employment and payroll',
         'Paying staff and meeting labour and social-insurance obligations',
         'legal_obligation',
         'Name, national or Iqama number, bank details, salary, attendance',
         'Employees',
         'Bank, social insurance authority, wage-protection system',
         'Duration of employment plus the statutory retention period',
         'RawSyst POS'),
        (t.id, c.id, 'Supplier management',
         'Placing orders and settling supplier accounts',
         'contract',
         'Contact name, phone number, email, bank details',
         'Supplier contacts',
         NULL,
         'While the supplier relationship is active plus the record-retention period',
         'RawSyst POS')
      ON CONFLICT DO NOTHING;

      -- One row per company so the settings screen has something to edit
      -- rather than a create-then-edit dance.
      INSERT INTO privacy_settings (company_id, tenant_id)
      VALUES (c.id, t.id) ON CONFLICT DO NOTHING;

      INSERT INTO storefront_disclosure (company_id, tenant_id)
      VALUES (c.id, t.id) ON CONFLICT DO NOTHING;
    END LOOP;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
