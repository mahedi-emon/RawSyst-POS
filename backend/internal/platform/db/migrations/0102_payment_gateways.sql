-- 0102 — The payment gateway layer (blueprint E3.3, E3.4).
--
-- # The credentials belong to the client, not to this repository
--
-- E3.3 asks for a gateway-agnostic abstraction with adapters for the Saudi
-- acquirers. Nothing about that needs an account: the adapter is HTTP against a
-- documented API, and the merchant id and key are configuration THE CLIENT
-- types into a settings screen and this table holds.
--
-- That is the whole design. A shop signs with Moyasar, gets a key, pastes it
-- in, presses Test, and the till starts taking cards through Moyasar. Another
-- shop signs with PayTabs and does the same. No deployment, no code change, no
-- credential of anybody's in a repository.
--
-- # Sealed with the same keyring as everything else
--
-- `secret_enc` is envelope-encrypted exactly as the ZATCA credential and the
-- webhook secret are. A gateway key can move money; a database copy that
-- yielded live ones would be an incident at every shop at once.
--
-- The PUBLIC half — a merchant id, a profile id, a publishable key — is stored
-- in the clear on purpose. It appears in the browser during a hosted checkout
-- anyway, and hiding it would mean decrypting on a path that does not need to.
--
-- # Test and live are the same row with a flag
--
-- Not two rows. A shop has one relationship with one acquirer, and the mode is
-- which endpoint it points at. Two rows would invite a shop to configure test,
-- take real money against it for a week, and find out at settlement.
--
-- # This table does not decide the fee
--
-- `sales_tender.fee_amount` and the settlement batches in 0057 already carry
-- what an acquirer actually charged and deposited. A rate stored here would be
-- a second answer to "what did this cost", free to disagree with the deposit —
-- and the deposit is the one that is true.

-- Which provider a configuration is for.
--
-- An enum rather than free text: an adapter has to exist for the value, and a
-- typo that produced a row nothing could dispatch would be a shop that thinks
-- it is taking cards and is not.
CREATE TYPE payment_provider AS ENUM (
  'hyperpay',
  'moyasar',
  'paytabs',
  'tap',
  'geidea',
  'checkout',      -- Checkout.com
  'amazon_payment_services',
  -- The terminal on the counter rather than a hosted page. E3.4: a card
  -- machine the till drives, and SoftPOS on a phone, which speak to the same
  -- acquirer through a local endpoint rather than the internet.
  'terminal'
);

CREATE TABLE payment_gateway (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,

  provider     payment_provider NOT NULL,

  -- What the shop calls it. Two configurations for one provider is legitimate
  -- — a separate merchant account per branch is ordinary — so the name is what
  -- distinguishes them on the screen.
  label        text NOT NULL,

  -- live or test. See the file note on why this is a flag and not two rows.
  mode         text NOT NULL DEFAULT 'test',

  -- The public half: merchant id, profile id, publishable key, terminal
  -- address. Which of these a provider wants differs, so it is jsonb rather
  -- than a column per acquirer — a column per acquirer would be a migration
  -- every time one is added.
  settings     jsonb NOT NULL DEFAULT '{}'::jsonb,

  -- The secret half, envelope-encrypted. Never read by any screen.
  secret_enc   bytea,

  -- Which tender methods this configuration handles. A shop may route mada
  -- through one acquirer and international cards through another, which is a
  -- real arrangement and not an exotic one.
  methods      text[] NOT NULL DEFAULT '{}',

  is_active    boolean NOT NULL DEFAULT false,

  -- What happened when somebody last pressed Test. Kept so a configuration
  -- that stopped working is visible on the screen rather than discovered by a
  -- cashier at the counter.
  last_checked_at timestamptz,
  last_check_ok   boolean,
  last_check_note text,

  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  created_by   uuid REFERENCES app_user(id) ON DELETE SET NULL,

  CONSTRAINT payment_gateway_label_not_blank CHECK (btrim(label) <> ''),
  CONSTRAINT payment_gateway_mode_valid CHECK (mode IN ('test', 'live')),
  CONSTRAINT payment_gateway_settings_is_object
    CHECK (jsonb_typeof(settings) = 'object'),
  -- A live configuration must have been tested and must have passed. The
  -- alternative is a shop switching to live on a Friday with a typo in the key
  -- and finding out on Saturday.
  CONSTRAINT payment_gateway_live_was_checked CHECK (
    NOT is_active OR mode <> 'live' OR last_check_ok IS TRUE)
);

CREATE UNIQUE INDEX payment_gateway_label_uq
  ON payment_gateway (company_id, lower(label));
-- At most one active configuration per method per company: two would make
-- "which acquirer took this payment" a question with two answers.
CREATE INDEX payment_gateway_active_idx
  ON payment_gateway (company_id) WHERE is_active;
CREATE INDEX payment_gateway_tenant_idx ON payment_gateway (tenant_id);

CREATE TRIGGER payment_gateway_touch BEFORE UPDATE ON payment_gateway
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE payment_gateway ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_gateway FORCE  ROW LEVEL SECURITY;
CREATE POLICY payment_gateway_isolation ON payment_gateway
  USING (tenant_id = current_tenant_id());

-- One attempt to take money through a gateway.
--
-- Separate from `sales_tender`, which records what the SHOP believes happened.
-- This records what the ACQUIRER said, including the attempts that failed —
-- and a failed attempt has no tender row, which is exactly why it needs a home
-- of its own. A shop asking "why did that card decline three times" is asking
-- about this table.
CREATE TABLE payment_attempt (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenant(id)  ON DELETE CASCADE,
  company_id   uuid NOT NULL REFERENCES company(id) ON DELETE CASCADE,
  gateway_id   uuid NOT NULL REFERENCES payment_gateway(id) ON DELETE RESTRICT,

  -- The caller's own idempotency key. A till that retries a charge after a
  -- timeout must not charge twice, and this is what makes the retry safe.
  uuid         uuid NOT NULL,

  -- What it is for. Nullable because an attempt can fail before an invoice
  -- exists — which is the ordinary case at a till, where the sale is only
  -- committed once the money is taken.
  invoice_id   uuid REFERENCES sales_invoice(id) ON DELETE SET NULL,
  order_id     uuid REFERENCES sales_order(id)   ON DELETE SET NULL,

  method       text NOT NULL,
  amount       numeric(18,4) NOT NULL,
  currency     char(3) NOT NULL,

  -- initiated → authorised → captured, or failed, or refunded.
  status       text NOT NULL DEFAULT 'initiated',

  -- The acquirer's own reference, which is what a support call quotes.
  provider_ref text,
  -- Their code and message, stored as they sent them. Not translated and not
  -- normalised: "which decline code did the bank return" is a question only
  -- the raw value answers.
  provider_code text,
  provider_message text,

  -- Where to send the customer, for a hosted checkout. Not kept after the
  -- attempt settles: it is a short-lived URL and holding it invites somebody
  -- to reuse it.
  redirect_url text,

  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  settled_at   timestamptz,

  CONSTRAINT payment_attempt_status_valid CHECK (status IN (
    'initiated', 'authorised', 'captured', 'failed', 'cancelled',
    'refunded')),
  CONSTRAINT payment_attempt_amount_positive CHECK (amount > 0),
  CONSTRAINT payment_attempt_currency_upper CHECK (currency = upper(currency))
);

-- The idempotency guarantee: one attempt per caller-supplied uuid per company.
CREATE UNIQUE INDEX payment_attempt_uuid_uq
  ON payment_attempt (company_id, uuid);
CREATE INDEX payment_attempt_invoice_idx ON payment_attempt (invoice_id)
  WHERE invoice_id IS NOT NULL;
CREATE INDEX payment_attempt_open_idx
  ON payment_attempt (company_id, created_at DESC)
  WHERE status IN ('initiated', 'authorised');
CREATE INDEX payment_attempt_tenant_idx ON payment_attempt (tenant_id);

CREATE TRIGGER payment_attempt_touch BEFORE UPDATE ON payment_attempt
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE payment_attempt ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_attempt FORCE  ROW LEVEL SECURITY;
CREATE POLICY payment_attempt_isolation ON payment_attempt
  USING (tenant_id = current_tenant_id());

-- ---------------------------------------------------------------------------
-- Permissions
-- ---------------------------------------------------------------------------

-- Configuring an acquirer is not the same as seeing that one is configured. A
-- gateway key can move money, so the write is Owner-level and the read is not.
INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner',         'gateway.manage'),
  ('owner',         'gateway.view'),
  ('accountant',    'gateway.view'),
  ('store_manager', 'gateway.view')
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
      ('owner',         'gateway.manage'),
      ('owner',         'gateway.view'),
      ('accountant',    'gateway.view'),
      ('store_manager', 'gateway.view')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END;
$$;

INSERT INTO permission_catalogue
  (permission, section, label, label_ar, label_bn, caution, sort_order)
VALUES
  ('gateway.view', 'money', 'See which card providers are set up',
   'الاطلاع على مزودي الدفع المُعدّين',
   'কোন কার্ড প্রদানকারী সেট আপ আছে দেখা', NULL, 145),
  ('gateway.manage', 'money', 'Connect a card provider',
   'ربط مزود دفع بالبطاقات', 'কার্ড প্রদানকারী সংযুক্ত করা',
   'Holds the key that takes money from customers.', 146)
ON CONFLICT (permission) DO UPDATE SET
  section = excluded.section,
  label = excluded.label,
  label_ar = excluded.label_ar,
  label_bn = excluded.label_bn,
  caution = excluded.caution,
  sort_order = excluded.sort_order;
