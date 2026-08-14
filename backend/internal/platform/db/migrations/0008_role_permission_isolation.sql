-- 0008 — Close a cross-tenant leak on role_permission.
--
-- Migration 0003 enabled row-level security on `role` but not on
-- `role_permission`. The table has no tenant_id of its own, so it was simply
-- unprotected: `SELECT * FROM role_permission` returned every row on the
-- platform, including the permission sets of other tenants' custom roles.
--
-- The severity is moderate rather than severe — permission strings are not
-- customer data — but it is still exactly what QA gate M8 forbids: one tenant
-- learning something about another through a manipulated query. A competitor
-- could see how a rival configures their staff.
--
-- Found while writing the M7 access tests, because seeding a role through the
-- platform plane failed. That failure was correct (role definitions belong to
-- the tenant, per 0006) and following it up surfaced the neighbouring gap.
--
-- The policy derives the tenant through `role` rather than denormalising a
-- tenant_id column. Two reasons: a copied column can drift out of step with its
-- parent, and this table is read once per session behind a cache, so the join
-- is not on a hot path.

ALTER TABLE role_permission ENABLE ROW LEVEL SECURITY;
ALTER TABLE role_permission FORCE  ROW LEVEL SECURITY;

CREATE POLICY role_permission_isolation ON role_permission
  USING (
    EXISTS (
      SELECT 1 FROM role r
      WHERE r.id = role_permission.role_id
        AND (r.tenant_id = current_tenant_id() OR r.tenant_id IS NULL)
    )
  );

-- The lookup above is by primary key on `role`, but the reverse direction —
-- listing a role's permissions — is the query the authorizer actually runs on
-- every cache miss.
CREATE INDEX IF NOT EXISTS role_permission_role_idx ON role_permission (role_id);
