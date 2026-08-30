-- 0094 — Write the platform predicate the way the boundary guard can see it.
--
-- 0093 gave `backup_record` and the two support-ticket tables a policy of
--
--   current_setting('app.platform_admin', true) = 'on'
--
-- which is exactly what `is_platform_admin()` evaluates, and is therefore
-- correct — and invisible. TestPlatformAdminHasNoBusinessDataAccess finds
-- tables that grant Super Admin access by looking for the FUNCTION NAME in the
-- policy expression, so an inlined equivalent passes the guard by not being
-- seen rather than by being justified.
--
-- That is the more dangerous of the two failure modes. A table that wrongly
-- grants platform access and trips the test gets argued about in review; one
-- that grants the same access through a spelling the test does not recognise
-- never comes up at all. The next person to widen Super Admin's reach would
-- have copied this spelling from here.
--
-- So the policies are rewritten to call the function, and the three tables are
-- added to the guard's allowlist with the reason each one belongs there. Both
-- halves are needed: the rewrite makes them visible, the allowlist entry makes
-- them deliberate.
--
-- Nothing about who can read what changes. This is the same predicate, spelled
-- so that it can be audited.

DROP POLICY IF EXISTS backup_record_isolation ON backup_record;
CREATE POLICY backup_record_isolation ON backup_record
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

DROP POLICY IF EXISTS support_ticket_isolation ON support_ticket;
CREATE POLICY support_ticket_isolation ON support_ticket
  USING (tenant_id = current_tenant_id() OR is_platform_admin());

DROP POLICY IF EXISTS support_message_isolation ON support_message;
CREATE POLICY support_message_isolation ON support_message
  USING (tenant_id = current_tenant_id() OR is_platform_admin());
