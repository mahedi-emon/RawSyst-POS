-- 0039 — The enrolment lookup has to run before a tenant is known.
--
-- Redeeming a code is the one call in the system made by a caller with no
-- credential at all: a terminal being paired has nothing to identify itself
-- with, which is the entire problem being solved. So the lookup runs on the
-- platform plane, and 0037 gave device_enrolment only the ordinary tenant
-- policy — which matches nothing there, so every enrolment was refused.
--
-- The same escape `device` itself has carried since 0006, and for a related
-- reason: A4 already gives Super Admin device health across tenants, and an
-- enrolment row holds a HASH and an expiry, never a usable code.
DROP POLICY device_enrolment_isolation ON device_enrolment;
CREATE POLICY device_enrolment_isolation ON device_enrolment
  USING (tenant_id = current_tenant_id() OR is_platform_admin());
