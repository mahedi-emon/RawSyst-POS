-- 0093 — Approval engine, notifications, webhooks, support tickets, imports
--        and backups (blueprint F1, D3, D5, H4, H6, H7, H10).
--
-- # Why one migration
--
-- These are five small modules that share one property: they are all about the
-- platform noticing something and telling somebody. An approval is a request
-- waiting for a person, a notification is a fact delivered to a person, a
-- webhook is a fact delivered to a system, a ticket is a person asking the
-- platform owner, and a backup is the platform telling itself it is safe.
-- Splitting them into five migrations would give five nearly identical
-- outbox-shaped tables written a week apart.
--
-- # The approval engine is rules, not code
--
-- F1's whole argument: "Hard-coding approval rules means every client with a
-- slightly different process needs custom development. A configurable engine
-- means one codebase serves a 3-person shop and a 300-person chain."
--
-- So `approval_rule` holds a CONDITION and a ROUTE, both as data, and the
-- runtime evaluates them on the commit path of whatever is being approved.
-- Phase 1's manager-PIN discount gate is the simplified precursor design 20
-- names; it becomes one configured rule rather than a second mechanism.
--
-- # A notification is not a message
--
-- `notification` is the FACT — low stock, a payment due, a submission that
-- failed. `notification_delivery` is each attempt to get that fact to somebody
-- through a channel. One fact, many deliveries: the same low-stock warning
-- goes in-app to the owner and by SMS to the buyer, and one of those can fail
-- and be retried without re-raising the underlying event or telling the owner
-- twice.

-- ---------------------------------------------------------------------------
-- The approval engine (F1, D5)
-- ---------------------------------------------------------------------------

CREATE TABLE approval_rule (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  name        text NOT NULL,
  is_active   boolean NOT NULL DEFAULT true,

  -- What kind of thing this rule watches: expense, purchase_order,
  -- stock_adjustment, discount, refund, payroll_run, stock_transfer,
  -- salary_change, permission_change. Free text rather than an enumeration
  -- because a module that adds a new approvable thing should not need a
  -- migration to make it approvable.
  subject     text NOT NULL,

  -- The condition, as data. F1's triggers are amount, percentage, quantity,
  -- product/category, store, employee, customer exposure and time of day, and
  -- they compose:
  --   {"amount_over": "5000", "store_id": "..."}
  -- An empty object means "always", which is how a shop says every expense
  -- needs sign-off regardless of size.
  condition   jsonb NOT NULL DEFAULT '{}'::jsonb,

  -- What to do. F1's actions: require approval (single or sequential),
  -- second-person PIN, block, warn, notify.
  action      text NOT NULL DEFAULT 'require_approval',

  -- Who decides, in order. Each step is a role or a named person:
  --   [{"role": "store_manager"}, {"user_id": "..."}]
  -- Sequential by position, which is what F1's worked example shows:
  -- Manager → Accountant → Owner.
  steps       jsonb NOT NULL DEFAULT '[]'::jsonb,

  -- F1: "if an approver doesn't respond within X hours, escalate". Null means
  -- it waits indefinitely, which is the right default: a request that
  -- escalates past the person who understands it is worse than a slow one.
  escalate_after_hours integer,

  priority    integer NOT NULL DEFAULT 0,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT approval_rule_action_valid CHECK (action IN (
    'require_approval', 'require_pin', 'block', 'warn', 'notify')),
  CONSTRAINT approval_rule_condition_is_an_object CHECK (
    jsonb_typeof(condition) = 'object'),
  CONSTRAINT approval_rule_steps_is_a_list CHECK (
    jsonb_typeof(steps) = 'array'),
  CONSTRAINT approval_rule_name_not_blank CHECK (btrim(name) <> ''),
  -- An approval that routes nowhere can never be granted, so the request would
  -- sit forever with nobody able to act on it.
  CONSTRAINT approval_rule_routes_somewhere CHECK (
    action <> 'require_approval' OR jsonb_array_length(steps) > 0),
  CONSTRAINT approval_rule_escalation_sane CHECK (
    escalate_after_hours IS NULL OR escalate_after_hours > 0)
);

CREATE INDEX approval_rule_subject_idx
  ON approval_rule (company_id, subject, priority DESC)
  WHERE is_active;
CREATE INDEX approval_rule_tenant_idx ON approval_rule (tenant_id);

CREATE TRIGGER approval_rule_touch BEFORE UPDATE ON approval_rule
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE approval_rule ENABLE ROW LEVEL SECURITY;
ALTER TABLE approval_rule FORCE  ROW LEVEL SECURITY;
CREATE POLICY approval_rule_isolation ON approval_rule
  USING (tenant_id = current_tenant_id());

-- One thing waiting for a decision. D5's Approval Center is a query over this.
CREATE TABLE approval_request (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  rule_id     uuid REFERENCES approval_rule(id) ON DELETE SET NULL,

  subject     text NOT NULL,
  -- What is waiting. Deliberately not a foreign key: the table it points at
  -- differs per subject, and a polymorphic FK would mean either a column per
  -- approvable type or a constraint that cannot be written.
  subject_id  uuid NOT NULL,
  -- Enough to render the queue without joining to nine different tables.
  summary     text NOT NULL,
  amount      numeric(18,4),
  currency    text,

  -- pending — waiting on the current step
  -- approved / rejected — decided
  -- cancelled — the underlying thing went away
  -- escalated — nobody answered in time and it moved up
  status      text NOT NULL DEFAULT 'pending',

  -- Which step of the rule's chain it is on, 1-based. A multi-step approval
  -- moves forward one step at a time and is only granted at the last.
  current_step integer NOT NULL DEFAULT 1,

  requested_by uuid REFERENCES app_user(id) ON DELETE SET NULL,
  requested_at timestamptz NOT NULL DEFAULT now(),
  decided_at   timestamptz,
  escalate_at  timestamptz,

  CONSTRAINT approval_request_status_valid CHECK (status IN (
    'pending', 'approved', 'rejected', 'cancelled', 'escalated')),
  CONSTRAINT approval_request_step_positive CHECK (current_step > 0),
  CONSTRAINT approval_request_summary_not_blank CHECK (btrim(summary) <> '')
);

-- One open request per thing. A second would let the same expense be approved
-- twice down two different chains.
CREATE UNIQUE INDEX approval_request_open_uq
  ON approval_request (subject, subject_id)
  WHERE status IN ('pending', 'escalated');
CREATE INDEX approval_request_queue_idx
  ON approval_request (company_id, status, requested_at);
CREATE INDEX approval_request_escalation_idx ON approval_request (escalate_at)
  WHERE status = 'pending' AND escalate_at IS NOT NULL;
CREATE INDEX approval_request_tenant_idx ON approval_request (tenant_id);

ALTER TABLE approval_request ENABLE ROW LEVEL SECURITY;
ALTER TABLE approval_request FORCE  ROW LEVEL SECURITY;
CREATE POLICY approval_request_isolation ON approval_request
  USING (tenant_id = current_tenant_id());

-- Every decision, kept. F1: "Every workflow execution is recorded in the audit
-- log with the full decision chain" — so a three-step approval leaves three
-- rows, and a rejection at step two still shows who granted step one.
CREATE TABLE approval_decision (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  request_id  uuid NOT NULL REFERENCES approval_request(id) ON DELETE CASCADE,

  step        integer NOT NULL,
  decision    text NOT NULL,
  reason      text,

  decided_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  decided_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT approval_decision_valid CHECK (decision IN (
    'approved', 'rejected', 'escalated', 'delegated')),
  -- A refusal a requester cannot act on is a refusal that wastes everybody's
  -- time, the same rule the rest of this product follows.
  CONSTRAINT approval_decision_rejection_says_why CHECK (
    decision <> 'rejected' OR btrim(coalesce(reason, '')) <> '')
);

CREATE INDEX approval_decision_request_idx
  ON approval_decision (request_id, step);
CREATE INDEX approval_decision_tenant_idx ON approval_decision (tenant_id);

ALTER TABLE approval_decision ENABLE ROW LEVEL SECURITY;
ALTER TABLE approval_decision FORCE  ROW LEVEL SECURITY;
CREATE POLICY approval_decision_isolation ON approval_decision
  USING (tenant_id = current_tenant_id());

CREATE TRIGGER approval_decision_no_change
  BEFORE UPDATE OR DELETE ON approval_decision
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- F1: "approvers can delegate while on leave".
CREATE TABLE approval_delegation (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  from_user_id uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  to_user_id   uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,

  starts_on   date NOT NULL,
  ends_on     date NOT NULL,
  note        text,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT approval_delegation_dates_ordered CHECK (ends_on >= starts_on),
  -- Delegating to yourself is a no-op that would look like cover and provide
  -- none.
  CONSTRAINT approval_delegation_is_to_somebody_else CHECK (
    from_user_id <> to_user_id)
);

CREATE INDEX approval_delegation_window_idx
  ON approval_delegation (company_id, from_user_id, starts_on, ends_on);
CREATE INDEX approval_delegation_tenant_idx ON approval_delegation (tenant_id);

ALTER TABLE approval_delegation ENABLE ROW LEVEL SECURITY;
ALTER TABLE approval_delegation FORCE  ROW LEVEL SECURITY;
CREATE POLICY approval_delegation_isolation ON approval_delegation
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Notifications (D3)
-- ---------------------------------------------------------------------------

CREATE TABLE notification (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid REFERENCES company(id) ON DELETE CASCADE,

  -- D3's trigger list: low_stock, online_order, purchase_request,
  -- payment_due, supplier_payment_due, credit_limit, expense_approval,
  -- stock_transfer, submission_failed, backup_failed, suspicious_login,
  -- warranty_expiring, batch_expiring, id_expiring.
  kind        text NOT NULL,
  severity    text NOT NULL DEFAULT 'info',

  title       text NOT NULL,
  body        text,
  -- Where the notification points, so tapping it goes somewhere useful.
  subject     text,
  subject_id  uuid,

  -- Who it is for. Null means everybody in the company who may see this kind,
  -- which is how a low-stock warning reaches whoever is looking.
  user_id     uuid REFERENCES app_user(id) ON DELETE CASCADE,

  read_at     timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT notification_severity_valid CHECK (severity IN (
    'info', 'warning', 'critical')),
  CONSTRAINT notification_title_not_blank CHECK (btrim(title) <> '')
);

CREATE INDEX notification_unread_idx
  ON notification (company_id, user_id, created_at DESC)
  WHERE read_at IS NULL;
CREATE INDEX notification_tenant_idx ON notification (tenant_id);
-- The index the de-duplicator reads: the same low-stock fact must not be
-- raised every time somebody looks at the product.
CREATE INDEX notification_recent_kind_idx
  ON notification (company_id, kind, subject_id, created_at DESC);

ALTER TABLE notification ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification FORCE  ROW LEVEL SECURITY;
CREATE POLICY notification_isolation ON notification
  USING (tenant_id = current_tenant_id());

-- One attempt to deliver one fact through one channel.
CREATE TABLE notification_delivery (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  notification_id uuid NOT NULL REFERENCES notification(id) ON DELETE CASCADE,

  channel     text NOT NULL,
  -- Where it went: an address, a number, a device token. Kept so a support
  -- call can establish what was actually tried.
  destination text,

  status      text NOT NULL DEFAULT 'queued',
  attempts    integer NOT NULL DEFAULT 0,
  last_error  text,

  sent_at     timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT notification_delivery_channel_valid CHECK (channel IN (
    'in_app', 'email', 'sms', 'push', 'whatsapp')),
  CONSTRAINT notification_delivery_status_valid CHECK (status IN (
    'queued', 'sent', 'failed', 'suppressed'))
);

CREATE INDEX notification_delivery_pending_idx
  ON notification_delivery (status, created_at)
  WHERE status = 'queued';
CREATE INDEX notification_delivery_note_idx
  ON notification_delivery (notification_id);
CREATE INDEX notification_delivery_tenant_idx
  ON notification_delivery (tenant_id);

ALTER TABLE notification_delivery ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_delivery FORCE  ROW LEVEL SECURITY;
CREATE POLICY notification_delivery_isolation ON notification_delivery
  USING (tenant_id = current_tenant_id());

-- What each person wants to hear about, and how. D3 gates marketing on consent
-- (E4/PDPL), and this is where a person's own choice lives.
CREATE TABLE notification_preference (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  user_id     uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,

  kind        text NOT NULL,
  in_app      boolean NOT NULL DEFAULT true,
  email       boolean NOT NULL DEFAULT false,
  sms         boolean NOT NULL DEFAULT false,
  push        boolean NOT NULL DEFAULT false,

  updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX notification_preference_uq
  ON notification_preference (user_id, kind);
CREATE INDEX notification_preference_tenant_idx
  ON notification_preference (tenant_id);

CREATE TRIGGER notification_preference_touch
  BEFORE UPDATE ON notification_preference
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE notification_preference ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_preference FORCE  ROW LEVEL SECURITY;
CREATE POLICY notification_preference_isolation ON notification_preference
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Webhooks and API keys (H6)
-- ---------------------------------------------------------------------------

CREATE TABLE webhook_endpoint (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  name        text NOT NULL,
  url         text NOT NULL,
  is_active   boolean NOT NULL DEFAULT true,

  -- Which events this endpoint wants: ["sale.completed", "order.delivered"].
  events      jsonb NOT NULL DEFAULT '[]'::jsonb,

  -- The signing secret, encrypted at rest like every other secret this product
  -- holds. A receiver verifies the signature to know the delivery is genuinely
  -- from here and not from anybody who learned the URL.
  secret_enc  bytea NOT NULL,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT webhook_endpoint_events_is_a_list CHECK (
    jsonb_typeof(events) = 'array'),
  -- Plain HTTP would put a tenant's sales over the wire in clear. There is no
  -- configuration option for this on purpose.
  CONSTRAINT webhook_endpoint_is_https CHECK (url LIKE 'https://%'),
  CONSTRAINT webhook_endpoint_name_not_blank CHECK (btrim(name) <> '')
);

CREATE INDEX webhook_endpoint_active_idx ON webhook_endpoint (company_id)
  WHERE is_active;
CREATE INDEX webhook_endpoint_tenant_idx ON webhook_endpoint (tenant_id);

CREATE TRIGGER webhook_endpoint_touch BEFORE UPDATE ON webhook_endpoint
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE webhook_endpoint ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_endpoint FORCE  ROW LEVEL SECURITY;
CREATE POLICY webhook_endpoint_isolation ON webhook_endpoint
  USING (tenant_id = current_tenant_id());

CREATE TABLE webhook_delivery (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  endpoint_id uuid NOT NULL REFERENCES webhook_endpoint(id) ON DELETE CASCADE,

  event       text NOT NULL,
  payload     jsonb NOT NULL,

  status      text NOT NULL DEFAULT 'queued',
  attempts    integer NOT NULL DEFAULT 0,
  response_status integer,
  last_error  text,

  -- When the next retry is due. Backed off, so a receiver that is down does
  -- not get hammered and a transient failure still recovers.
  next_attempt_at timestamptz,
  delivered_at    timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT webhook_delivery_status_valid CHECK (status IN (
    'queued', 'delivered', 'failed', 'abandoned'))
);

CREATE INDEX webhook_delivery_pending_idx
  ON webhook_delivery (next_attempt_at)
  WHERE status = 'queued';
CREATE INDEX webhook_delivery_endpoint_idx
  ON webhook_delivery (endpoint_id, created_at DESC);
CREATE INDEX webhook_delivery_tenant_idx ON webhook_delivery (tenant_id);

ALTER TABLE webhook_delivery ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_delivery FORCE  ROW LEVEL SECURITY;
CREATE POLICY webhook_delivery_isolation ON webhook_delivery
  USING (tenant_id = current_tenant_id());

CREATE TABLE api_key (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  name        text NOT NULL,
  -- The key is stored HASHED, like a password: an API key is a credential, and
  -- a readable key column is a breach that hands over every integration.
  key_hash    text NOT NULL,
  -- The first few characters, so a person can tell two keys apart in a list
  -- without the platform being able to reconstruct either.
  key_prefix  text NOT NULL,

  -- What it may do. A subset of the permissions its creator holds, so a key
  -- can never be an escalation.
  permissions jsonb NOT NULL DEFAULT '[]'::jsonb,

  last_used_at timestamptz,
  expires_at   timestamptz,
  revoked_at   timestamptz,
  revoked_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT api_key_permissions_is_a_list CHECK (
    jsonb_typeof(permissions) = 'array'),
  CONSTRAINT api_key_name_not_blank CHECK (btrim(name) <> '')
);

CREATE UNIQUE INDEX api_key_hash_uq ON api_key (key_hash);
CREATE INDEX api_key_live_idx ON api_key (company_id)
  WHERE revoked_at IS NULL;
CREATE INDEX api_key_tenant_idx ON api_key (tenant_id);

ALTER TABLE api_key ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_key FORCE  ROW LEVEL SECURITY;
CREATE POLICY api_key_isolation ON api_key
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Import / export (H7)
-- ---------------------------------------------------------------------------
--
-- H7's flow: "Old system export → CSV/Excel → Field Mapping → Validation →
-- Preview → Import → Error Report → Completed". The rows are staged and
-- validated BEFORE anything is written, because a half-finished import of a
-- customer list is worse than none: nobody knows which half.

CREATE TABLE import_batch (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id  uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- products, customers, suppliers, opening_stock, opening_balances, employees
  kind        text NOT NULL,
  filename    text,

  -- uploaded — rows staged
  -- validated — checked, with errors listed
  -- committed — written
  -- failed / cancelled
  status      text NOT NULL DEFAULT 'uploaded',

  -- Which incoming column feeds which field: {"Name": "full_name"}. H7's
  -- field mapping step, kept so a repeat import from the same old system does
  -- not have to be mapped again.
  mapping     jsonb NOT NULL DEFAULT '{}'::jsonb,

  total_rows   integer NOT NULL DEFAULT 0,
  valid_rows   integer NOT NULL DEFAULT 0,
  error_rows   integer NOT NULL DEFAULT 0,
  imported_rows integer NOT NULL DEFAULT 0,

  created_by  uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  committed_at timestamptz,

  CONSTRAINT import_batch_kind_valid CHECK (kind IN (
    'products', 'customers', 'suppliers', 'opening_stock',
    'opening_balances', 'employees')),
  CONSTRAINT import_batch_status_valid CHECK (status IN (
    'uploaded', 'validated', 'committed', 'failed', 'cancelled')),
  CONSTRAINT import_batch_counts_sane CHECK (
    total_rows >= 0 AND valid_rows >= 0 AND error_rows >= 0
    AND imported_rows >= 0)
);

CREATE INDEX import_batch_company_idx
  ON import_batch (company_id, created_at DESC);
CREATE INDEX import_batch_tenant_idx ON import_batch (tenant_id);

ALTER TABLE import_batch ENABLE ROW LEVEL SECURITY;
ALTER TABLE import_batch FORCE  ROW LEVEL SECURITY;
CREATE POLICY import_batch_isolation ON import_batch
  USING (tenant_id = current_tenant_id());

CREATE TABLE import_row (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  batch_id    uuid NOT NULL REFERENCES import_batch(id) ON DELETE CASCADE,

  row_no      integer NOT NULL,
  raw         jsonb NOT NULL,

  -- pending, valid, invalid, imported, skipped
  status      text NOT NULL DEFAULT 'pending',
  -- Why a row was refused, in words the person who exported the file can act
  -- on. H7's "Error Report" is a query over this.
  error       text,
  created_id  uuid,

  CONSTRAINT import_row_status_valid CHECK (status IN (
    'pending', 'valid', 'invalid', 'imported', 'skipped')),
  CONSTRAINT import_row_invalid_says_why CHECK (
    status <> 'invalid' OR btrim(coalesce(error, '')) <> '')
);

CREATE UNIQUE INDEX import_row_no_uq ON import_row (batch_id, row_no);
CREATE INDEX import_row_status_idx ON import_row (batch_id, status);
CREATE INDEX import_row_tenant_idx ON import_row (tenant_id);

ALTER TABLE import_row ENABLE ROW LEVEL SECURITY;
ALTER TABLE import_row FORCE  ROW LEVEL SECURITY;
CREATE POLICY import_row_isolation ON import_row
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Backups (H4)
-- ---------------------------------------------------------------------------
--
-- H4: "a backup that can't be restored is not a real backup". So the record
-- carries a verification state of its own, separate from whether the backup
-- was taken — and the dashboard reads the most recent VERIFIED one, not the
-- most recent one.

CREATE TABLE backup_record (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid REFERENCES tenant(id) ON DELETE CASCADE,

  -- scheduled — the nightly run
  -- manual — H4's one-click button
  kind        text NOT NULL DEFAULT 'scheduled',

  status      text NOT NULL DEFAULT 'running',
  -- Where it went, and how big. A location rather than the bytes: a database
  -- dump does not belong in the database it is a dump of.
  location    text,
  size_bytes  bigint,
  checksum    text,

  -- H4's verification. Null means nobody has checked yet, which is different
  -- from checked-and-broken and must not read as success.
  verified_at timestamptz,
  verify_error text,

  started_at  timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  error       text,

  requested_by uuid REFERENCES app_user(id) ON DELETE SET NULL,

  CONSTRAINT backup_record_kind_valid CHECK (kind IN ('scheduled', 'manual')),
  CONSTRAINT backup_record_status_valid CHECK (status IN (
    'running', 'succeeded', 'failed')),
  CONSTRAINT backup_record_failure_says_why CHECK (
    status <> 'failed' OR btrim(coalesce(error, '')) <> ''),
  CONSTRAINT backup_record_size_sane CHECK (
    size_bytes IS NULL OR size_bytes >= 0)
);

CREATE INDEX backup_record_tenant_idx
  ON backup_record (tenant_id, started_at DESC);
CREATE INDEX backup_record_recent_idx ON backup_record (started_at DESC);

ALTER TABLE backup_record ENABLE ROW LEVEL SECURITY;
ALTER TABLE backup_record FORCE  ROW LEVEL SECURITY;
-- A tenant sees its own backups; the platform sees all of them, because H8
-- puts backup status across all tenants on the Super Admin dashboard. This is
-- one of the few tables where that is right: it is metadata about the tenant's
-- data, never the data itself.
CREATE POLICY backup_record_isolation ON backup_record
  USING (tenant_id = current_tenant_id() OR current_setting(
    'app.platform_admin', true) = 'on');

-- ---------------------------------------------------------------------------
-- Support tickets (H10)
-- ---------------------------------------------------------------------------

CREATE TABLE support_ticket (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  company_id  uuid REFERENCES company(id) ON DELETE SET NULL,

  ticket_no   text NOT NULL,
  subject     text NOT NULL,
  body        text NOT NULL,

  -- question, bug, feature_request, billing, outage
  kind        text NOT NULL DEFAULT 'question',
  priority    text NOT NULL DEFAULT 'normal',
  -- open, waiting_on_customer, waiting_on_support, resolved, closed
  status      text NOT NULL DEFAULT 'open',

  raised_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,

  CONSTRAINT support_ticket_kind_valid CHECK (kind IN (
    'question', 'bug', 'feature_request', 'billing', 'outage')),
  CONSTRAINT support_ticket_priority_valid CHECK (priority IN (
    'low', 'normal', 'high', 'urgent')),
  CONSTRAINT support_ticket_status_valid CHECK (status IN (
    'open', 'waiting_on_customer', 'waiting_on_support', 'resolved', 'closed')),
  CONSTRAINT support_ticket_subject_not_blank CHECK (btrim(subject) <> '')
);

CREATE UNIQUE INDEX support_ticket_no_uq ON support_ticket (ticket_no);
CREATE INDEX support_ticket_open_idx ON support_ticket (status, created_at DESC)
  WHERE status NOT IN ('resolved', 'closed');
CREATE INDEX support_ticket_tenant_idx ON support_ticket (tenant_id);

CREATE TRIGGER support_ticket_touch BEFORE UPDATE ON support_ticket
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE support_ticket ENABLE ROW LEVEL SECURITY;
ALTER TABLE support_ticket FORCE  ROW LEVEL SECURITY;
-- H10 is a conversation BETWEEN a tenant and the platform owner, so both sides
-- must see it. That is the whole point of the module, and it is why a ticket
-- holds a subject and a description rather than a tenant's business records.
CREATE POLICY support_ticket_isolation ON support_ticket
  USING (tenant_id = current_tenant_id() OR current_setting(
    'app.platform_admin', true) = 'on');

CREATE TABLE support_message (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  ticket_id   uuid NOT NULL REFERENCES support_ticket(id) ON DELETE CASCADE,

  body        text NOT NULL,
  -- Whether the platform owner wrote it. A tenant must be able to tell an
  -- answer from their own question.
  from_platform boolean NOT NULL DEFAULT false,
  author_label  text,

  author_id   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT support_message_body_not_blank CHECK (btrim(body) <> '')
);

CREATE INDEX support_message_ticket_idx
  ON support_message (ticket_id, created_at);
CREATE INDEX support_message_tenant_idx ON support_message (tenant_id);

ALTER TABLE support_message ENABLE ROW LEVEL SECURITY;
ALTER TABLE support_message FORCE  ROW LEVEL SECURITY;
CREATE POLICY support_message_isolation ON support_message
  USING (tenant_id = current_tenant_id() OR current_setting(
    'app.platform_admin', true) = 'on');

CREATE TRIGGER support_message_no_change
  BEFORE UPDATE OR DELETE ON support_message
  FOR EACH ROW EXECUTE FUNCTION reject_always();

-- ---------------------------------------------------------------------------
-- Numbering
-- ---------------------------------------------------------------------------

CREATE SEQUENCE IF NOT EXISTS support_ticket_seq START 1000;

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  -- The Approval Center. Seeing what is waiting and deciding it are separate:
  -- a requester needs to watch their own request without being able to grant
  -- it, which is the whole point of an approval.
  ('owner',            'approval.view'),   ('owner',            'approval.decide'),
  ('owner',            'approval.manage_rules'),
  ('store_manager',    'approval.view'),   ('store_manager',    'approval.decide'),
  ('accountant',       'approval.view'),   ('accountant',       'approval.decide'),
  ('purchase_manager', 'approval.view'),
  ('hr_manager',       'approval.view'),
  ('auditor',          'approval.view'),

  -- Notifications are about the caller themselves, so everybody who signs in
  -- may read their own. There is no permission to read somebody else's.
  ('owner',            'notification.manage'),
  ('store_manager',    'notification.manage'),

  -- Integration is an owner-level act: a webhook sends a shop's sales
  -- somewhere, and an API key is a credential that acts on their behalf.
  ('owner',            'integration.view'), ('owner',            'integration.manage'),
  ('accountant',       'integration.view'),

  -- H7's migration wizard rewrites a shop's master data.
  ('owner',            'data.import'),      ('owner',            'data.export'),
  ('accountant',       'data.export'),
  ('auditor',          'data.export'),

  ('owner',            'backup.view'),      ('owner',            'backup.run'),
  ('accountant',       'backup.view'),

  ('owner',            'support.raise'),
  ('store_manager',    'support.raise'),
  ('accountant',       'support.raise')
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
      ('owner',            'approval.view'),   ('owner',            'approval.decide'),
      ('owner',            'approval.manage_rules'),
      ('store_manager',    'approval.view'),   ('store_manager',    'approval.decide'),
      ('accountant',       'approval.view'),   ('accountant',       'approval.decide'),
      ('purchase_manager', 'approval.view'),
      ('hr_manager',       'approval.view'),
      ('auditor',          'approval.view'),
      ('owner',            'notification.manage'),
      ('store_manager',    'notification.manage'),
      ('owner',            'integration.view'), ('owner',            'integration.manage'),
      ('accountant',       'integration.view'),
      ('owner',            'data.import'),      ('owner',            'data.export'),
      ('accountant',       'data.export'),
      ('auditor',          'data.export'),
      ('owner',            'backup.view'),      ('owner',            'backup.run'),
      ('accountant',       'backup.view'),
      ('owner',            'support.raise'),
      ('store_manager',    'support.raise'),
      ('accountant',       'support.raise')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
