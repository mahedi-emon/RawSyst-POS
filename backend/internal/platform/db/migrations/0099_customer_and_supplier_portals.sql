-- 0099 — The customer self-service portal and the supplier portal
--        (blueprint F2, F3).
--
-- # Neither portal user is an app_user
--
-- `app_user` is a person who works for the shop: they hold roles, they carry
-- permissions, they appear in the audit trail as an actor, and every route in
-- the product is written on the assumption that a token names one of them.
--
-- A customer signing in to see their own invoices is not that. Neither is a
-- supplier accepting a purchase order. Giving them `app_user` rows would put
-- them inside the tenant's staff list, inside the user limit their plan
-- enforces, and one permission bug away from the till.
--
-- So each portal has its own identity table, its own session table, and its own
-- token audience. A portal session can NEVER be exchanged for a staff session:
-- they are different tables, and the middleware that reads one does not know
-- how to read the other.
--
-- # A customer signs in with a phone and a code
--
-- F2: "the tenant's own customers log in (phone + OTP)". No password, because a
-- password is a thing a shop's customers would forget and a thing this product
-- would then have to help them recover — a support burden the shop carries for
-- a screen that shows them their own receipts.
--
-- The code is hashed for the reason 0076 gives about reset codes and states in
-- as many words: for the minutes it is alive it is exactly as good as a
-- password, and a leaked backup that yields live codes yields accounts.
--
-- # A supplier signs in with an email and a password
--
-- Different because the act is different. A supplier accepting a fifty-thousand
-- riyal purchase order is committing their business, they do it from a desk,
-- and the person who does it is a named contact rather than whoever holds the
-- phone. F3 also has them uploading documents and invoices, which is not a
-- thing anybody does through a one-time code.
--
-- # What a portal may reach is fixed by the schema, not by a permission
--
-- There are no portal permissions and no portal roles. Every portal query is
-- written with the signed-in customer's or supplier's id in its WHERE clause,
-- and there is no parameter on any portal route that could name somebody else.
-- A permission would suggest that reading another customer's invoices is a
-- thing that can be granted, and it is not: there is no code path that serves
-- it.

-- ---------------------------------------------------------------------------
-- F2 — The customer portal
-- ---------------------------------------------------------------------------

-- One row per customer who has ever signed in. Created on first successful
-- sign-in rather than at onboarding: a shop with four thousand customers has
-- not signed four thousand people up to a portal, and rows for people who have
-- never used it would be personal data held for no purpose, which is exactly
-- what E4.1's data minimisation forbids.
CREATE TABLE customer_portal_user (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  customer_id  uuid NOT NULL REFERENCES customer(id) ON DELETE CASCADE,

  -- The number they sign in with, copied from the customer record at first
  -- sign-in. Copied rather than joined because a customer whose number the shop
  -- later corrects must not silently lose access to their own history, and
  -- because this column is what the sign-in looks up.
  phone        text NOT NULL,

  is_active    boolean NOT NULL DEFAULT true,
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz,

  CONSTRAINT customer_portal_phone_not_blank CHECK (btrim(phone) <> '')
);

-- One portal account per customer, and one per phone number within a company.
CREATE UNIQUE INDEX customer_portal_customer_uq
  ON customer_portal_user (customer_id);
CREATE UNIQUE INDEX customer_portal_phone_uq
  ON customer_portal_user (company_id, phone);
CREATE INDEX customer_portal_tenant_idx ON customer_portal_user (tenant_id);

ALTER TABLE customer_portal_user ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_portal_user FORCE  ROW LEVEL SECURITY;
CREATE POLICY customer_portal_user_isolation ON customer_portal_user
  USING (tenant_id = current_tenant_id());

-- A code sent to a phone. Shaped like password_reset_request in 0076 and for
-- the same reasons, including keeping the row after use so a replay can be
-- refused differently from a guess.
CREATE TABLE customer_portal_code (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  -- The phone, not the customer: a code is requested before anybody knows
  -- which customer will be found, and asking for one must not reveal whether
  -- the number is on file.
  phone        text NOT NULL,
  code_hash    text NOT NULL,

  requested_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  used_at      timestamptz,
  attempts     smallint NOT NULL DEFAULT 0,

  CONSTRAINT customer_portal_code_attempts_sane
    CHECK (attempts >= 0 AND attempts <= 10)
);

CREATE INDEX customer_portal_code_live_idx
  ON customer_portal_code (company_id, phone, expires_at DESC)
  WHERE used_at IS NULL;
CREATE INDEX customer_portal_code_tenant_idx
  ON customer_portal_code (tenant_id);

ALTER TABLE customer_portal_code ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_portal_code FORCE  ROW LEVEL SECURITY;
CREATE POLICY customer_portal_code_isolation ON customer_portal_code
  USING (tenant_id = current_tenant_id());

CREATE TABLE customer_portal_session (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  portal_user_id uuid NOT NULL
    REFERENCES customer_portal_user(id) ON DELETE CASCADE,

  -- SHA-256 of the token. The token itself is never stored, for the same
  -- reason the code is not: a leaked backup would otherwise be a set of live
  -- sessions.
  token_hash   text NOT NULL,

  issued_at    timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  revoked_at   timestamptz,
  last_used_at timestamptz,

  -- Kept so a customer can be told where their account has been used, which is
  -- E4's transparency applied to the portal itself.
  user_agent   text,
  ip           inet
);

CREATE UNIQUE INDEX customer_portal_session_token_uq
  ON customer_portal_session (token_hash);
CREATE INDEX customer_portal_session_user_idx
  ON customer_portal_session (portal_user_id, expires_at DESC);
CREATE INDEX customer_portal_session_tenant_idx
  ON customer_portal_session (tenant_id);

ALTER TABLE customer_portal_session ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_portal_session FORCE  ROW LEVEL SECURITY;
CREATE POLICY customer_portal_session_isolation ON customer_portal_session
  USING (tenant_id = current_tenant_id());

-- A saved address. F2 asks for "saved addresses" beside the saved sizes that
-- already exist in `customer_size`.
CREATE TABLE customer_address (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  customer_id  uuid NOT NULL REFERENCES customer(id) ON DELETE CASCADE,

  label        text NOT NULL,
  line1        text NOT NULL,
  line2        text,
  city         text,
  district     text,
  postcode     text,
  country      char(2),
  phone        text,
  -- Which one a new order defaults to. At most one per customer.
  is_default   boolean NOT NULL DEFAULT false,

  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT customer_address_label_not_blank CHECK (btrim(label) <> ''),
  CONSTRAINT customer_address_line1_not_blank CHECK (btrim(line1) <> ''),
  CONSTRAINT customer_address_country_lower
    CHECK (country IS NULL OR country = lower(country))
);

CREATE UNIQUE INDEX customer_address_default_uq
  ON customer_address (customer_id) WHERE is_default;
CREATE INDEX customer_address_customer_idx
  ON customer_address (customer_id, label);
CREATE INDEX customer_address_tenant_idx ON customer_address (tenant_id);

CREATE TRIGGER customer_address_touch BEFORE UPDATE ON customer_address
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE customer_address ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_address FORCE  ROW LEVEL SECURITY;
CREATE POLICY customer_address_isolation ON customer_address
  USING (tenant_id = current_tenant_id());

-- A customer asking to send something back.
--
-- F2: "Submit a return or exchange request (routes into the workflow engine for
-- approval)". It is a REQUEST, not a return: the return itself is posted by the
-- shop through the existing returns path, which is where the accounting and the
-- stock movement belong. This table is the conversation before that.
CREATE TABLE return_request (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  customer_id  uuid NOT NULL REFERENCES customer(id) ON DELETE CASCADE,

  request_no   text NOT NULL,
  invoice_id   uuid REFERENCES sales_invoice(id) ON DELETE SET NULL,

  -- return or exchange. F2 offers both and they end differently: one gives
  -- money back, the other gives a different garment.
  kind         text NOT NULL DEFAULT 'return',
  reason       text NOT NULL,
  -- What they want back, as they described it. Not variant ids: a customer
  -- reads a receipt, not a catalogue.
  items        text NOT NULL,

  -- requested → accepted / refused → completed
  status       text NOT NULL DEFAULT 'requested',
  decided_at   timestamptz,
  decided_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  decision_note text,

  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT return_request_kind_valid CHECK (kind IN ('return', 'exchange')),
  CONSTRAINT return_request_status_valid CHECK (status IN (
    'requested', 'accepted', 'refused', 'completed')),
  CONSTRAINT return_request_reason_not_blank CHECK (btrim(reason) <> ''),
  CONSTRAINT return_request_decided_is_stamped CHECK (
    (status IN ('requested')) = (decided_at IS NULL)),
  CONSTRAINT return_request_refusal_has_reason CHECK (
    status <> 'refused' OR btrim(coalesce(decision_note, '')) <> '')
);

CREATE UNIQUE INDEX return_request_no_uq
  ON return_request (company_id, request_no);
CREATE INDEX return_request_open_idx
  ON return_request (company_id, created_at DESC)
  WHERE status = 'requested';
CREATE INDEX return_request_customer_idx
  ON return_request (customer_id, created_at DESC);
CREATE INDEX return_request_tenant_idx ON return_request (tenant_id);

CREATE TRIGGER return_request_touch BEFORE UPDATE ON return_request
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE return_request ENABLE ROW LEVEL SECURITY;
ALTER TABLE return_request FORCE  ROW LEVEL SECURITY;
CREATE POLICY return_request_isolation ON return_request
  USING (tenant_id = current_tenant_id());

CREATE SEQUENCE IF NOT EXISTS return_request_seq START 1;

-- ---------------------------------------------------------------------------
-- F3 — The supplier portal
-- ---------------------------------------------------------------------------

CREATE TABLE supplier_portal_user (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  supplier_id  uuid NOT NULL REFERENCES supplier(id) ON DELETE CASCADE,

  -- A named contact, not "the supplier". A supplier with two people who both
  -- accept orders has two rows, and the audit trail can say which of them did.
  full_name    text NOT NULL,
  email        text NOT NULL,
  -- argon2id, like a staff password. See the file note on why a supplier gets
  -- a password where a customer gets a code.
  password_hash text NOT NULL,

  is_active    boolean NOT NULL DEFAULT true,
  invited_at   timestamptz NOT NULL DEFAULT now(),
  invited_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,
  last_seen_at timestamptz,

  CONSTRAINT supplier_portal_name_not_blank CHECK (btrim(full_name) <> ''),
  CONSTRAINT supplier_portal_email_lower CHECK (email = lower(email))
);

CREATE UNIQUE INDEX supplier_portal_email_uq
  ON supplier_portal_user (company_id, email);
CREATE INDEX supplier_portal_supplier_idx
  ON supplier_portal_user (supplier_id);
CREATE INDEX supplier_portal_tenant_idx ON supplier_portal_user (tenant_id);

ALTER TABLE supplier_portal_user ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_portal_user FORCE  ROW LEVEL SECURITY;
CREATE POLICY supplier_portal_user_isolation ON supplier_portal_user
  USING (tenant_id = current_tenant_id());

CREATE TABLE supplier_portal_session (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id) ON DELETE CASCADE,
  portal_user_id uuid NOT NULL
    REFERENCES supplier_portal_user(id) ON DELETE CASCADE,

  token_hash   text NOT NULL,
  issued_at    timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  revoked_at   timestamptz,
  last_used_at timestamptz,
  user_agent   text,
  ip           inet
);

CREATE UNIQUE INDEX supplier_portal_session_token_uq
  ON supplier_portal_session (token_hash);
CREATE INDEX supplier_portal_session_user_idx
  ON supplier_portal_session (portal_user_id, expires_at DESC);
CREATE INDEX supplier_portal_session_tenant_idx
  ON supplier_portal_session (tenant_id);

ALTER TABLE supplier_portal_session ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_portal_session FORCE  ROW LEVEL SECURITY;
CREATE POLICY supplier_portal_session_isolation ON supplier_portal_session
  USING (tenant_id = current_tenant_id());

-- A supplier's answer to a purchase order.
--
-- F3: "View issued Purchase Orders, and accept or reject them with comments."
-- A side table rather than a column on `purchase_order`, because the order's
-- own status is the SHOP's view of it — draft, issued, received, closed — and a
-- supplier declining does not move the shop's workflow on its own. The buyer
-- sees the response and decides what to do about it.
CREATE TABLE po_response (
  po_id        uuid PRIMARY KEY REFERENCES purchase_order(id) ON DELETE CASCADE,
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  portal_user_id uuid REFERENCES supplier_portal_user(id) ON DELETE SET NULL,
  response     text NOT NULL,
  comment      text,
  -- What they can actually deliver, when it differs from what was asked. F3's
  -- "with comments" in the only form a buyer can act on.
  promised_on  date,

  responded_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT po_response_valid CHECK (response IN (
    'accepted', 'rejected', 'accepted_with_changes')),
  CONSTRAINT po_response_rejection_has_reason CHECK (
    response <> 'rejected' OR btrim(coalesce(comment, '')) <> '')
);

CREATE INDEX po_response_tenant_idx ON po_response (tenant_id);
CREATE INDEX po_response_company_idx ON po_response (company_id, responded_at DESC);

ALTER TABLE po_response ENABLE ROW LEVEL SECURITY;
ALTER TABLE po_response FORCE  ROW LEVEL SECURITY;
CREATE POLICY po_response_isolation ON po_response
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

-- The STAFF side of the two portals: inviting a supplier contact, answering a
-- customer's return request, seeing who has signed in. The portal users
-- themselves hold no permissions at all — see the file note.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',            'portal.manage'),
  ('store_manager',    'portal.manage'),
  ('purchase_manager', 'portal.manage'),
  ('owner',            'portal.view'),
  ('store_manager',    'portal.view'),
  ('purchase_manager', 'portal.view'),
  ('accountant',       'portal.view'),
  ('cashier',          'portal.view')
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
      ('owner',            'portal.manage'),
      ('store_manager',    'portal.manage'),
      ('purchase_manager', 'portal.manage'),
      ('owner',            'portal.view'),
      ('store_manager',    'portal.view'),
      ('purchase_manager', 'portal.view'),
      ('accountant',       'portal.view'),
      ('cashier',          'portal.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;
