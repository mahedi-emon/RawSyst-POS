-- 0003 — Identity: users, sessions, roles, permissions and scoping.
--
-- Blueprint A6 requires more than role-based access. On top of the verb grant
-- sit four scope dimensions (store, warehouse, transaction amount, time
-- window) and field-level masking. A cashier may hold catalog.view and still
-- be structurally unable to see a cost price.
--
-- Every check happens server-side. Blueprint A6.2: "a hidden button in the UI
-- is never treated as real security." QA gate M7 tests this by calling every
-- restricted route directly as a Cashier.

-- ---------------------------------------------------------------------------
-- Users
-- ---------------------------------------------------------------------------

CREATE TABLE app_user (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  -- NULL tenant_id marks a platform Super Admin, who sits above every tenant
  -- and belongs to none. Their RLS policy is separate, below.
  tenant_id      uuid REFERENCES tenant(id) ON DELETE CASCADE,

  email          citext NOT NULL,
  phone          text,
  full_name      text NOT NULL,

  -- argon2id. Blueprint A4.2 is explicit that a password is stored as an
  -- irreversible hash, "a security requirement, not just a policy choice" —
  -- which is why Super Admin can reset a password but never reveal one.
  password_hash  text NOT NULL,
  must_change_password boolean NOT NULL DEFAULT true,

  mfa_enabled    boolean NOT NULL DEFAULT false,
  mfa_secret_enc bytea,                      -- envelope-encrypted, never plain

  status         user_status NOT NULL DEFAULT 'invited',
  failed_attempts integer NOT NULL DEFAULT 0,
  locked_until   timestamptz,
  last_login_at  timestamptz,

  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT app_user_email_shape CHECK (position('@' in email) > 1),
  CONSTRAINT app_user_mfa_secret_present CHECK (NOT mfa_enabled OR mfa_secret_enc IS NOT NULL)
);

-- Email is unique within a tenant; Super Admins (tenant_id IS NULL) are unique
-- globally. Two partial indexes express that precisely.
CREATE UNIQUE INDEX app_user_email_tenant_uq
  ON app_user (tenant_id, email) WHERE tenant_id IS NOT NULL;
CREATE UNIQUE INDEX app_user_email_platform_uq
  ON app_user (email) WHERE tenant_id IS NULL;

CREATE TRIGGER app_user_touch BEFORE UPDATE ON app_user
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE app_user ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_user FORCE  ROW LEVEL SECURITY;
CREATE POLICY app_user_isolation ON app_user
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Sessions
-- ---------------------------------------------------------------------------

CREATE TABLE user_session (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid REFERENCES tenant(id) ON DELETE CASCADE,
  user_id       uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,

  -- Refresh tokens are stored hashed. A leaked database backup must not yield
  -- usable credentials.
  refresh_token_hash text NOT NULL,

  device_label  text,
  ip            inet,
  user_agent    text,

  created_at    timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  revoked_at    timestamptz,                  -- remote revoke, blueprint H1
  revoked_reason text
);

CREATE UNIQUE INDEX user_session_refresh_uq ON user_session (refresh_token_hash);
CREATE INDEX user_session_user_idx ON user_session (user_id) WHERE revoked_at IS NULL;

ALTER TABLE user_session ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_session FORCE  ROW LEVEL SECURITY;
CREATE POLICY user_session_isolation ON user_session
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Roles and permissions
-- ---------------------------------------------------------------------------

CREATE TABLE role (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  -- NULL tenant_id marks a platform-provided template. Blueprint A6.1 lists
  -- twelve predefined roles; a tenant clones one and edits the copy.
  tenant_id   uuid REFERENCES tenant(id) ON DELETE CASCADE,

  key         text NOT NULL,                 -- 'cashier', 'accountant', ...
  name        text NOT NULL,
  name_ar     text,
  description text,
  is_system   boolean NOT NULL DEFAULT false,
  cloned_from uuid REFERENCES role(id),

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT role_key_format CHECK (key ~ '^[a-z][a-z0-9_]*$')
);

CREATE UNIQUE INDEX role_key_tenant_uq
  ON role (tenant_id, key) WHERE tenant_id IS NOT NULL;
CREATE UNIQUE INDEX role_key_template_uq
  ON role (key) WHERE tenant_id IS NULL;

CREATE TRIGGER role_touch BEFORE UPDATE ON role
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE role ENABLE ROW LEVEL SECURITY;
ALTER TABLE role FORCE  ROW LEVEL SECURITY;
-- Templates (tenant_id IS NULL) are readable by every tenant so the role
-- builder can offer them as a starting point; only Super Admin may write them,
-- which is enforced in the application authorization layer.
CREATE POLICY role_isolation ON role
  USING (tenant_id = current_tenant_id() OR tenant_id IS NULL);

-- Permissions are '<module>.<verb>' strings rather than a fixed enum.
-- Blueprint A6.2 lists fourteen verbs but its own worked example uses Hold,
-- Exchange and Receive Payment, none of which are in that list — so the verb
-- set must be extensible per module. Modules declare theirs at startup.
CREATE TABLE role_permission (
  role_id    uuid NOT NULL REFERENCES role(id) ON DELETE CASCADE,
  permission text NOT NULL,
  PRIMARY KEY (role_id, permission),
  CONSTRAINT role_permission_format CHECK (permission ~ '^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$')
);

CREATE INDEX role_permission_perm_idx ON role_permission (permission);

-- ---------------------------------------------------------------------------
-- Role assignment with the four scope dimensions (blueprint A6.2)
-- ---------------------------------------------------------------------------

CREATE TABLE user_role_assignment (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  user_id       uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  role_id       uuid NOT NULL REFERENCES role(id) ON DELETE RESTRICT,

  -- Scope 0: legal entity. NULL means every company in the tenant.
  company_id    uuid REFERENCES company(id) ON DELETE CASCADE,

  -- Scope 1: store. Empty array means every store.
  store_ids     uuid[] NOT NULL DEFAULT '{}',

  -- Scope 2: warehouse. Empty array means every warehouse.
  warehouse_ids uuid[] NOT NULL DEFAULT '{}',

  -- Scope 3: transaction amount ceiling. NULL means unlimited.
  -- Blueprint example: cashier discount up to SAR 50, manager up to SAR 500,
  -- owner unlimited.
  amount_limit  numeric(18,4),

  -- Scope 4: validity window, for temporary and seasonal staff.
  valid_from    timestamptz,
  valid_until   timestamptz,

  created_at    timestamptz NOT NULL DEFAULT now(),
  created_by    uuid REFERENCES app_user(id),

  CONSTRAINT ura_amount_limit_non_negative CHECK (amount_limit IS NULL OR amount_limit >= 0),
  CONSTRAINT ura_window_ordered CHECK (
    valid_from IS NULL OR valid_until IS NULL OR valid_until > valid_from)
);

CREATE INDEX ura_user_idx   ON user_role_assignment (user_id);
CREATE INDEX ura_tenant_idx ON user_role_assignment (tenant_id);

ALTER TABLE user_role_assignment ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_role_assignment FORCE  ROW LEVEL SECURITY;
CREATE POLICY ura_isolation ON user_role_assignment
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Audit log (blueprint D4)
-- ---------------------------------------------------------------------------

-- Exactly six fields are specified: who, what, when, where, before, after.
-- Append-only, and the trigger below means that holds against direct SQL, not
-- merely against the application.
CREATE TABLE audit_log (
  id           bigserial PRIMARY KEY,
  tenant_id    uuid,

  actor_id     uuid,                          -- WHO   (NULL for system actions)
  actor_label  text,                          -- denormalised: survives user deletion
  action       text NOT NULL,                 -- WHAT
  occurred_at  timestamptz NOT NULL DEFAULT now(),  -- WHEN
  ip           inet,                          -- WHERE
  device_label text,                          --   "

  entity_type  text NOT NULL,
  entity_id    uuid,
  before_value jsonb,                         -- BEFORE
  after_value  jsonb,                         -- AFTER

  -- Set when a data-subject erasure request has anonymised the personal
  -- content. The row itself always survives: the evidence that a change
  -- happened outlives the personal data inside it.
  anonymised_at timestamptz
);

CREATE INDEX audit_log_tenant_time_idx ON audit_log (tenant_id, occurred_at DESC);
CREATE INDEX audit_log_entity_idx      ON audit_log (entity_type, entity_id);
CREATE INDEX audit_log_actor_idx       ON audit_log (actor_id, occurred_at DESC);

-- Blueprint D4: logs "cannot be edited or deleted by any user, including
-- Owner, to preserve evidentiary integrity". PDPL anonymisation is the single
-- exception and runs as a privileged maintenance path outside this trigger.
CREATE TRIGGER audit_log_append_only
  BEFORE UPDATE OR DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION reject_always();

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE  ROW LEVEL SECURITY;
CREATE POLICY audit_log_isolation ON audit_log
  USING (tenant_id = current_tenant_id());
