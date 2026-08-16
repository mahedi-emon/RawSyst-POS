-- A buyer has to be able to see what they are buying.
--
-- 0032 gave the Purchase Manager the purchasing verbs and stopped there, which
-- left them unable to raise an order: choosing a line means picking a variant,
-- picking a variant means reading the catalogue, and reading the catalogue is
-- catalog.view. The role could create a purchase order in the API and had no
-- way to fill one in through the product list.
--
-- Granted at view only. A buyer reads the catalogue; changing what the shop
-- sells is a merchandising decision and stays with the roles that already hold
-- catalog.create and catalog.edit.
--
-- Cost price is deliberately NOT granted here either. A purchase order carries
-- the cost the buyer negotiates and types in, which is a different thing from
-- catalog.view_cost_price — that permission reveals what every OTHER item in
-- the shop cost, and nothing about raising an order requires it.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  ('purchase_manager', 'catalog.view'),
  -- A store manager receiving a delivery matches physical goods against the
  -- lines on screen, which needs the same read.
  ('store_manager', 'catalog.view')
) AS p(role_key, permission) ON p.role_key = r.key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;
