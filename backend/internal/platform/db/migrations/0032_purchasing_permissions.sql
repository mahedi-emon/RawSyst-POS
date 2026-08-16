-- Permissions for the purchasing module.
--
-- Seeded as the module ships rather than up front, exactly as 0005 describes:
-- "a role never silently grants access to a module that has not been
-- security-reviewed."
--
-- # Why seven verbs and not one
--
-- Because B5.2's control depends on separating them. A three-way match that
-- blocks a bill is worthless if the person who entered the bill can also
-- approve their own discrepancy, and that separation is impossible to configure
-- unless recording and approving are distinct permissions a shop can grant to
-- different people.
--
-- The same reasoning splits issuing an order from creating one. A junior buyer
-- drafting orders is an ordinary arrangement; committing the shop to a supplier
-- is a different act with a different signature on it.

INSERT INTO role_permission (role_id, permission)
SELECT r.id, p.permission
FROM role r
JOIN (VALUES
  -- Owner gets everything, listed explicitly rather than by wildcard so that
  -- adding a module stays a deliberate grant.
  ('owner', 'purchasing.view'),
  ('owner', 'purchasing.manage_suppliers'),
  ('owner', 'purchasing.create_order'),
  ('owner', 'purchasing.issue_order'),
  ('owner', 'purchasing.receive_goods'),
  ('owner', 'purchasing.record_bill'),
  ('owner', 'purchasing.approve_bill'),
  ('owner', 'purchasing.pay_supplier'),

  -- The Purchase Manager runs the buying cycle: suppliers, orders, receipts
  -- and bills. Deliberately WITHOUT approve_bill and pay_supplier — the whole
  -- point of the match is that somebody other than the buyer accepts a
  -- discrepancy, and somebody other than the buyer releases the money.
  ('purchase_manager', 'purchasing.view'),
  ('purchase_manager', 'purchasing.manage_suppliers'),
  ('purchase_manager', 'purchasing.create_order'),
  ('purchase_manager', 'purchasing.issue_order'),
  ('purchase_manager', 'purchasing.receive_goods'),
  ('purchase_manager', 'purchasing.record_bill'),

  -- A store manager receives deliveries at the branch and needs to see what is
  -- on order to know what to expect. They do not raise orders or handle bills.
  ('store_manager', 'purchasing.view'),
  ('store_manager', 'purchasing.receive_goods'),

  -- The accountant is the counterweight: they approve blocked bills and pay
  -- suppliers, and they cannot raise an order or receive goods. That is the
  -- separation of duties B5.2 exists to create.
  ('accountant', 'purchasing.view'),
  ('accountant', 'purchasing.record_bill'),
  ('accountant', 'purchasing.approve_bill'),
  ('accountant', 'purchasing.pay_supplier'),

  -- Read-only, like everything else they see.
  ('auditor', 'purchasing.view')
) AS p(role_key, permission) ON p.role_key = r.key
WHERE r.tenant_id IS NULL
ON CONFLICT DO NOTHING;
