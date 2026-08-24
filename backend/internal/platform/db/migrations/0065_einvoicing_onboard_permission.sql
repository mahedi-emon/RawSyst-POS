-- 0065 — The verb for onboarding a till with ZATCA.
--
-- 0043 deliberately left this out, and said so:
--
--   "Nothing here grants the right to assert a CSID. There is no verb for that
--    because there is no such operation: the CSID columns are read-only until
--    onboarding is built against a verified ZATCA source."
--
-- It is built now, against ZATCA's published API and checked against their own
-- validator, so the operation exists and needs a permission.
--
-- # Why this is not folded into einvoicing.manage
--
-- Because they are different sizes of decision.
--
-- einvoicing.manage corrects a till's registration details -- a typo in the
-- industry classification, a branch name. Wrong, and onboarding is refused
-- with a message naming the field.
--
-- Onboarding BINDS the business's tax identity to a certificate, consumes a
-- one-time password the taxpayer had to fetch from their own Fatoora account,
-- and produces the credential every future invoice is reported under. Getting
-- it wrong in production is not a typo; it is a registration at the tax
-- authority.
--
-- So it goes to the Owner alone. Not the accountant, who answers for the VAT
-- return but does not decide how the business registers; not the store
-- manager, who needs to SEE which unit their tills sign under -- 0043's stated
-- reason for their view grant -- and has no business registering one.
--
-- The seeing/doing split 0043 describes is preserved exactly: einvoicing.view
-- still shows onboarding status to everyone who already reads compliance
-- state, including the failure reason ZATCA gave, because that is what makes
-- the problem diagnosable by whoever notices it first.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('owner', 'einvoicing.onboard')
) AS p(role_key, permission) ON r.key = p.role_key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;

-- Existing tenants, one at a time, for the reason 0042 records: `role` has
-- FORCE ROW LEVEL SECURITY and a tenant predicate, so an unqualified backfill
-- would silently do nothing.
-- Joined through cloned_from rather than r.key, matching 0043: a tenant may
-- have renamed their Owner role, and matching on the key would then skip the
-- very role that needs the grant.
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
      ('owner', 'einvoicing.onboard')
    ) AS p(role_key, permission) ON template.key = p.role_key
    WHERE r.tenant_id = t.id
    ON CONFLICT DO NOTHING;
  END LOOP;

  PERFORM set_config('app.tenant_id', '', true);
  PERFORM set_config('app.platform_admin', 'off', true);
END $$;
