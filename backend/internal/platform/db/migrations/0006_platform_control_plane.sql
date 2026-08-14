-- 0006 — Platform control plane.
--
-- Migration 0002 enabled FORCE ROW LEVEL SECURITY on every tenant table, which
-- is correct — but it left no way to create a tenant in the first place. A row
-- with no tenant context satisfies no policy, so tenant provisioning was
-- impossible. The isolation tests caught this before any code depended on it.
--
-- The fix is not to weaken isolation. It is to model the thing the blueprint
-- already describes: Super Admin is a SEPARATE plane that sits above tenants,
-- not a tenant with extra rights.
--
-- Blueprint A4 draws the boundary precisely: Super Admin controls the platform
-- level — tenant lifecycle, subscriptions, feature availability, uptime — and
-- "does not interfere in the Owner's day-to-day business data."
--
-- So the platform predicate is added ONLY to tables Super Admin legitimately
-- administers. Business data (sales, inventory, customers, journals) will never
-- carry it, and a Super Admin will be as blind to a tenant's invoices as any
-- outsider.

-- Returns true only when the current transaction was opened by a platform
-- Super Admin. Set exclusively by db.Pool.Tx from a verified token claim —
-- never from anything a request body can influence.
CREATE OR REPLACE FUNCTION is_platform_admin() RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT coalesce(current_setting('app.platform_admin', true), '') = 'on'
$$;

-- ---------------------------------------------------------------------------
-- Tables Super Admin administers (blueprint A4)
-- ---------------------------------------------------------------------------

-- Tenant lifecycle: create, suspend, deactivate, assign plan.
DROP POLICY tenant_isolation ON tenant;
CREATE POLICY tenant_isolation ON tenant
  USING (id = current_tenant_id() OR is_platform_admin());

-- Per-tenant ceilings are a subscription concern.
DROP POLICY tenant_limit_isolation ON tenant_limit;
CREATE POLICY tenant_limit_isolation ON tenant_limit
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- Companies are visible to Super Admin for provisioning and for the
-- platform-wide compliance watch, which surfaces failed ZATCA submissions
-- across all tenants.
DROP POLICY company_isolation ON company;
CREATE POLICY company_isolation ON company
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

DROP POLICY store_isolation ON store;
CREATE POLICY store_isolation ON store
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- Device health and ZATCA onboarding status per terminal appear in the
-- platform health dashboard (blueprint A4, H8).
DROP POLICY device_isolation ON device;
CREATE POLICY device_isolation ON device
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- Super Admin creates the tenant's FIRST Owner login (blueprint A5) and
-- performs assisted password recovery (A4.2). Both require write access here.
-- What it does NOT permit is reading a password: the column holds an
-- irreversible hash, so there is nothing to reveal.
DROP POLICY app_user_isolation ON app_user;
CREATE POLICY app_user_isolation ON app_user
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- Session revocation is a security-incident capability (blueprint H1).
DROP POLICY user_session_isolation ON user_session;
CREATE POLICY user_session_isolation ON user_session
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- The platform-wide audit log records every Super Admin action, including
-- actions taken on an Owner's behalf such as password recovery (A4.2).
DROP POLICY audit_log_isolation ON audit_log;
CREATE POLICY audit_log_isolation ON audit_log
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

-- ---------------------------------------------------------------------------
-- Tables that stay tenant-only, deliberately
-- ---------------------------------------------------------------------------
--
-- role, user_role_assignment and regulatory_rule_override are NOT given the
-- platform predicate. A tenant's own role definitions and any approved
-- regulatory variance belong to that tenant; Super Admin sets platform
-- defaults and role templates, which live as rows with tenant_id IS NULL and
-- are already reachable through the existing template clause.
--
-- Every future business table — sales, invoices, journals, inventory,
-- customers, payroll — follows the same rule: tenant predicate only.
