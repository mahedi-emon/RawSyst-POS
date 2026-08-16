// Buying.
//
// Four views on one screen, because they are four stages of one thing and a
// buyer moves between them constantly: what is on order, what has arrived, what
// is owed, and who is owed it. Splitting them across four navigation items
// would make the commonest journey — order arrives, bill it, pay it — a
// three-screen trip.
//
// Everything here reuses what already exists: the DetailScreen frame, the
// useRemote hook and its five states, the design system's tables and badges.
// Nothing new was invented for this module.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { useRemote } from '../dashboard/useRemote';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { money } from '../ui/format';
import {
  fetchAgeing,
  listBills,
  listOrders,
  listSuppliers,
  type Bill,
  type Order,
  type Supplier,
} from '../api/purchasing';
import { OrderDetail } from './OrderDetail';
import { BillDetail } from './BillDetail';

type Tab = 'orders' | 'bills' | 'suppliers' | 'ageing';

export function PurchasingScreen({ companyId }: { companyId: string }) {
  const [tab, setTab] = useState<Tab>('orders');
  const [openOrder, setOpenOrder] = useState<string | null>(null);
  const [openBill, setOpenBill] = useState<string | null>(null);

  if (openOrder) {
    return (
      <OrderDetail
        companyId={companyId}
        poId={openOrder}
        onBack={() => setOpenOrder(null)}
      />
    );
  }
  if (openBill) {
    return (
      <BillDetail
        companyId={companyId}
        billId={openBill}
        onBack={() => setOpenBill(null)}
      />
    );
  }

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">Buying</h1>
          <p className="ds-caption">Orders, deliveries, bills and suppliers</p>
        </div>

        <div className="detail__actions">
          <div className="segmented" role="group" aria-label="What to show">
            {(
              [
                ['orders', 'Orders'],
                ['bills', 'Bills'],
                ['suppliers', 'Suppliers'],
                ['ageing', 'What we owe'],
              ] as const
            ).map(([key, label]) => (
              <button
                key={key}
                className={`segmented__btn${tab === key ? ' segmented__btn--on' : ''}`}
                aria-pressed={tab === key}
                onClick={() => setTab(key)}
              >
                {label}
              </button>
            ))}
          </div>
        </div>
      </header>

      {tab === 'orders' && <Orders companyId={companyId} onOpen={setOpenOrder} />}
      {tab === 'bills' && <Bills companyId={companyId} onOpen={setOpenBill} />}
      {tab === 'suppliers' && <Suppliers companyId={companyId} />}
      {tab === 'ageing' && <AgeingView companyId={companyId} />}
    </main>
  );
}

/** The order book. */
function Orders({
  companyId,
  onOpen,
}: {
  companyId: string;
  onOpen: (id: string) => void;
}) {
  const { client } = useAuth();
  const load = useCallback(() => listOrders(client, companyId), [client, companyId]);
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(orders) => (
        <div className="ds-panel">
          <div className="ds-panel__body ds-scroll-x">
            {orders.length === 0 ? (
              <EmptyState
                title="No purchase orders yet"
                body="An order records what you have asked a supplier for. Nothing goes into stock until the goods actually arrive and you receive them."
              />
            ) : (
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">Order</th>
                    <th scope="col">Supplier</th>
                    <th scope="col">Ordered</th>
                    <th scope="col">Status</th>
                    <th scope="col" className="num">Value</th>
                  </tr>
                </thead>
                <tbody>
                  {orders.map((o) => (
                    <tr key={o.id}>
                      <td>
                        <button className="detail__rowbtn" onClick={() => onOpen(o.id)}>
                          <span className="detail__strong">{o.po_number}</span>
                        </button>
                      </td>
                      <td>{o.supplier}</td>
                      <td className="num">{o.ordered_on}</td>
                      <td>
                        <OrderStatus status={o.status} />
                      </td>
                      <td className="num">
                        {money(o.total_inclusive, { currency: o.currency })}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}
    </RemoteBody>
  );
}

/** The payables ledger. */
function Bills({
  companyId,
  onOpen,
}: {
  companyId: string;
  onOpen: (id: string) => void;
}) {
  const { client } = useAuth();
  const load = useCallback(() => listBills(client, companyId), [client, companyId]);
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(bills) => (
        <div className="ds-panel">
          <div className="ds-panel__body ds-scroll-x">
            {bills.length === 0 ? (
              <EmptyState
                title="No supplier bills yet"
                body="Bills appear here as you record them. Each is checked against its order and what actually arrived before it can be paid."
              />
            ) : (
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">Invoice</th>
                    <th scope="col">Supplier</th>
                    <th scope="col">Due</th>
                    <th scope="col">Status</th>
                    <th scope="col" className="num">Outstanding</th>
                  </tr>
                </thead>
                <tbody>
                  {bills.map((b) => (
                    <tr key={b.id}>
                      <td>
                        <button className="detail__rowbtn" onClick={() => onOpen(b.id)}>
                          <span className="detail__strong">{b.supplier_ref}</span>
                          {b.po_number && <span className="ds-caption">{b.po_number}</span>}
                        </button>
                      </td>
                      <td>{b.supplier}</td>
                      <td className="num">{b.due_date}</td>
                      <td>
                        <BillStatus bill={b} />
                      </td>
                      <td className="num">
                        {money(b.outstanding, { currency: b.currency })}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}
    </RemoteBody>
  );
}

function Suppliers({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const load = useCallback(() => listSuppliers(client, companyId), [client, companyId]);
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(suppliers) => (
        <div className="ds-panel">
          <div className="ds-panel__body ds-scroll-x">
            {suppliers.length === 0 ? (
              <EmptyState
                title="No suppliers yet"
                body="Add the businesses you buy from. Their payment terms set when each bill falls due."
              />
            ) : (
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">Supplier</th>
                    <th scope="col">Terms</th>
                    <th scope="col">VAT number</th>
                    <th scope="col" className="num">Owed</th>
                  </tr>
                </thead>
                <tbody>
                  {suppliers.map((s) => (
                    <SupplierRow key={s.id} supplier={s} />
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}
    </RemoteBody>
  );
}

function SupplierRow({ supplier }: { supplier: Supplier }) {
  const owes = Number(supplier.outstanding) > 0;
  return (
    <tr>
      <td>
        <span className="detail__strong">{supplier.legal_name}</span>
        <span className="ds-caption">{supplier.code}</span>
      </td>
      <td>
        {supplier.payment_terms_days === 0
          ? 'On delivery'
          : `${supplier.payment_terms_days} days`}
      </td>
      <td className="num">
        {supplier.vat_number || <span className="ds-subtle">—</span>}
      </td>
      <td className={`num${owes ? '' : ' ds-subtle'}`}>
        {money(supplier.outstanding)}
      </td>
    </tr>
  );
}

/** What is owed, and how overdue.
 *
 * Aged from the DUE date, not the bill date — a 60-day invoice raised 45 days
 * ago is not late, and ageing it from issue would send a buyer to chase a
 * supplier who is owed nothing yet. */
function AgeingView({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const load = useCallback(() => fetchAgeing(client, companyId), [client, companyId]);
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(ageing) => (
        <div className="ds-panel">
          <div className="ds-panel__head">
            <h2 className="ds-h3">What we owe</h2>
            <span className="ds-caption">as at {ageing.as_of}</span>
          </div>
          <div className="ds-panel__body ds-scroll-x">
            {ageing.rows.length === 0 ? (
              <EmptyState
                title="Nothing outstanding"
                body="Every supplier bill has been settled."
              />
            ) : (
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">Supplier</th>
                    <th scope="col" className="num">Not due</th>
                    <th scope="col" className="num">1–30</th>
                    <th scope="col" className="num">31–60</th>
                    <th scope="col" className="num">61–90</th>
                    <th scope="col" className="num">90+</th>
                    <th scope="col" className="num">Total</th>
                  </tr>
                </thead>
                <tbody>
                  {ageing.rows.map((r) => (
                    <tr key={r.supplier_id}>
                      <td>{r.supplier}</td>
                      <td className="num ds-muted">{money(r.not_due)}</td>
                      <td className="num">{money(r.days_0_30)}</td>
                      <td className="num">{money(r.days_31_60)}</td>
                      {/* Past 60 days is where a supplier relationship starts
                          to be at risk, so it is emphasised rather than left
                          as one more column of grey figures. */}
                      <td className="num ds-down">{money(r.days_61_90)}</td>
                      <td className="num ds-down">{money(r.days_90_plus)}</td>
                      <td className="num">{money(r.total)}</td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr>
                    <td>Total owed</td>
                    <td colSpan={5} />
                    <td className="num">
                      {money(ageing.total, { currency: ageing.base_currency })}
                    </td>
                  </tr>
                </tfoot>
              </table>
            )}
          </div>
        </div>
      )}
    </RemoteBody>
  );
}

const ORDER_STATES: Record<string, { label: string; tone: string }> = {
  draft: { label: 'Draft', tone: 'ds-badge--neutral' },
  issued: { label: 'Sent to supplier', tone: 'ds-badge--info' },
  receiving: { label: 'Part delivered', tone: 'ds-badge--warning' },
  received: { label: 'Fully delivered', tone: 'ds-badge--success' },
  closed: { label: 'Closed', tone: 'ds-badge--neutral' },
  cancelled: { label: 'Cancelled', tone: 'ds-badge--neutral' },
};

export function OrderStatus({ status }: { status: string }) {
  const known = ORDER_STATES[status];
  return (
    <span className={`ds-badge ${known?.tone ?? 'ds-badge--neutral'}`}>
      {known?.label ?? status.replace(/_/g, ' ')}
    </span>
  );
}

/** A bill's state, in words a buyer reads rather than a status enum. */
export function BillStatus({ bill }: { bill: Bill }) {
  const states: Record<string, { label: string; tone: string }> = {
    draft: { label: 'Draft', tone: 'ds-badge--neutral' },
    matched: { label: 'Checked', tone: 'ds-badge--success' },
    // The one that matters. Named for what it means to the reader — the money
    // is held — rather than for the internal state.
    blocked: { label: 'Held — needs approval', tone: 'ds-badge--danger' },
    approved: { label: 'Approved', tone: 'ds-badge--warning' },
    paid: { label: 'Paid', tone: 'ds-badge--success' },
    cancelled: { label: 'Cancelled', tone: 'ds-badge--neutral' },
  };
  const known = states[bill.status];
  return (
    <span className={`ds-badge ${known?.tone ?? 'ds-badge--neutral'}`}>
      {known?.label ?? bill.status}
    </span>
  );
}

export type { Order };
