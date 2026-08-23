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
  setSupplierActive,
  type Bill,
  type Order,
  type Supplier,
} from '../api/purchasing';
import { OrderDetail } from './OrderDetail';
import { BillDetail } from './BillDetail';
import { SupplierForm } from './SupplierForm';
import { OrderForm } from './OrderForm';
import { BillForm } from './BillForm';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';

type Tab = 'orders' | 'bills' | 'suppliers' | 'ageing';

export function PurchasingScreen({ companyId }: { companyId: string }) {
  const t = useT();
  const { can } = useAuth();
  const [tab, setTab] = useState<Tab>('orders');
  const [openOrder, setOpenOrder] = useState<string | null>(null);
  const [openBill, setOpenBill] = useState<string | null>(null);

  // Which form is showing, if any. A form takes over the screen rather than
  // opening in a modal: an order has a line table that grows, and a dialog
  // that scrolls internally on a laptop is a worse place to do real work than
  // the page itself.
  const [creating, setCreating] = useState<null | 'supplier' | 'order' | 'bill'>(null);
  // The supplier being corrected, if any. Reuses the form that added them.
  const [editingSupplier, setEditingSupplier] = useState<Supplier | null>(null);

  // Bumped after a save so the list underneath refetches. Cheaper and harder
  // to get wrong than threading a refresh callback through every list.
  const [saved, setSaved] = useState(0);

  const mayAddSupplier = can('purchasing.manage_suppliers');
  const mayCreateOrder = can('purchasing.create_order');
  const mayRecordBill = can('purchasing.record_bill');

  if (creating === 'supplier' || editingSupplier) {
    // One form for both, because they are the same fields — a separate edit
    // screen would be the same inputs twice and the two would drift.
    const done = () => {
      setCreating(null);
      setEditingSupplier(null);
    };
    return (
      <FormPage title={t('common.suppliers')} onBack={done}>
        <SupplierForm
          companyId={companyId}
          existing={editingSupplier ?? undefined}
          onSaved={() => {
            done();
            setTab('suppliers');
            setSaved((n) => n + 1);
          }}
          onCancel={done}
        />
      </FormPage>
    );
  }

  if (creating === 'order') {
    return (
      <FormPage title={t('common.orders')} onBack={() => setCreating(null)}>
        <OrderForm
          companyId={companyId}
          onSaved={(order) => {
            setCreating(null);
            setSaved((n) => n + 1);
            // Straight to the order just raised, because the next thing a
            // buyer does is send it to the supplier.
            setOpenOrder(order.id);
          }}
          onCancel={() => setCreating(null)}
        />
      </FormPage>
    );
  }

  if (creating === 'bill') {
    return (
      <FormPage title={t('common.bills')} onBack={() => setCreating(null)}>
        <BillForm
          companyId={companyId}
          onSaved={(bill) => {
            setCreating(null);
            setSaved((n) => n + 1);
            // Straight to the bill, because the match result is the thing the
            // buyer needs to see and it is only known once it is recorded.
            setOpenBill(bill.id);
          }}
          onCancel={() => setCreating(null)}
        />
      </FormPage>
    );
  }

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
          <h1 className="ds-h1">{t('purch.buying')}</h1>
          <p className="ds-caption">{t('purch.overview')}</p>
        </div>

        <div className="detail__actions">
          {/* The primary action sits beside the tab it acts on, so it always
              means the thing the reader is currently looking at. Shown only
              when the permission is held — the server refuses it either way,
              but a button that always refuses teaches people to distrust the
              rest of them. */}
          {tab === 'suppliers' && mayAddSupplier && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setCreating('supplier')}
            >
              {t('purch.addSupplier')}
            </button>
          )}
          {tab === 'bills' && mayRecordBill && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setCreating('bill')}
            >
              {t('purch.recordBill')}
            </button>
          )}
          {tab === 'orders' && mayCreateOrder && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setCreating('order')}
            >
              {t('purch.newOrder')}
            </button>
          )}

          <div className="segmented" role="group" aria-label={t('common.whatToShow')}>
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

      {tab === 'orders' && <Orders key={saved} companyId={companyId} onOpen={setOpenOrder} />}
      {tab === 'bills' && <Bills key={saved} companyId={companyId} onOpen={setOpenBill} />}
      {tab === 'suppliers' && (
        <Suppliers
          key={saved}
          companyId={companyId}
          onEdit={setEditingSupplier}
          onChanged={() => setSaved((n) => n + 1)}
        />
      )}
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
  const t = useT();
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
                title={t('purch.noOrders')}
                body="An order records what you have asked a supplier for. Nothing goes into stock until the goods actually arrive and you receive them."
              />
            ) : (
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('common.order')}</th>
                    <th scope="col">{t('common.supplier')}</th>
                    <th scope="col">{t('common.ordered')}</th>
                    <th scope="col">{t('common.status')}</th>
                    <th scope="col" className="num">{t('common.value')}</th>
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
  const t = useT();
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
                title={t('purch.noBills')}
                body="Bills appear here as you record them. Each is checked against its order and what actually arrived before it can be paid."
              />
            ) : (
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('common.invoice')}</th>
                    <th scope="col">{t('common.supplier')}</th>
                    <th scope="col">{t('common.due')}</th>
                    <th scope="col">{t('common.status')}</th>
                    <th scope="col" className="num">{t('common.outstanding')}</th>
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

function Suppliers({
  companyId,
  onEdit,
  onChanged,
}: {
  companyId: string;
  onEdit: (supplier: Supplier) => void;
  onChanged: () => void;
}) {
  const t = useT();
  const { client, can } = useAuth();
  // Retired suppliers are shown behind a toggle rather than hidden outright.
  // They are referenced by orders and bills, so somebody looking one up needs
  // to be able to find them — but they must not clutter the list a buyer picks
  // from every day.
  const [showRetired, setShowRetired] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(
    () => listSuppliers(client, companyId, '', showRetired),
    [client, companyId, showRetired],
  );
  const { remote, reload } = useRemote(load);

  const mayManage = can('purchasing.manage_suppliers');

  async function setActive(supplier: Supplier, active: boolean) {
    setNotice(null);
    try {
      await setSupplierActive(client, companyId, supplier.id, active);
      reload();
      onChanged();
    } catch (err) {
      // The server refuses to retire a supplier who is still owed money, and
      // the refusal names the amount. Shown as-is: it says what to do.
      setNotice(err instanceof Error ? err.message : t('common.didNotWork'));
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(suppliers) => (
        <>
          {notice && (
            <p className="ds-panel purchase__notice" role="alert">
              {notice}
            </p>
          )}

          <div className="ds-panel">
            <div className="ds-panel__head">
              <h2 className="ds-h3">{t('common.suppliers')}</h2>
              <label className="supplier__toggle">
                <input
                  type="checkbox"
                  checked={showRetired}
                  onChange={(e) => setShowRetired(e.target.checked)}
                />
                <span className="ds-caption">{t('common.includeRetired')}</span>
              </label>
            </div>

            <div className="ds-panel__body ds-scroll-x">
              {suppliers.length === 0 ? (
                <EmptyState
                  title={t('purch.noSuppliers')}
                  body="Add the businesses you buy from. Their payment terms set when each bill falls due."
                />
              ) : (
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('common.supplier')}</th>
                      <th scope="col">{t('common.terms')}</th>
                      <th scope="col">{t('common.vatNumber')}</th>
                      <th scope="col" className="num">{t('common.owed')}</th>
                      {mayManage && <th scope="col" />}
                    </tr>
                  </thead>
                  <tbody>
                    {suppliers.map((s) => (
                      <SupplierRow
                        key={s.id}
                        supplier={s}
                        mayManage={mayManage}
                        onEdit={() => onEdit(s)}
                        onSetActive={(active) => void setActive(s, active)}
                      />
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        </>
      )}
    </RemoteBody>
  );
}
function SupplierRow({
  supplier,
  mayManage,
  onEdit,
  onSetActive,
}: {
  supplier: Supplier;
  mayManage: boolean;
  onEdit: () => void;
  onSetActive: (active: boolean) => void;
}) {
  const t = useT();
  const owes = Number(supplier.outstanding) > 0;
  return (
    <tr className={supplier.is_active ? undefined : 'detail__row--aside'}>
      <td>
        <span className="detail__strong">{supplier.legal_name}</span>
        <span className="ds-caption">
          {supplier.code}
          {!supplier.is_active && ' · retired'}
        </span>
      </td>
      <td>
        {supplier.payment_terms_days === 0
          ? t('order.onDelivery')
          : `${supplier.payment_terms_days} days`}
      </td>
      <td className="num">
        {supplier.vat_number || <span className="ds-subtle">—</span>}
      </td>
      <td className={`num${owes ? '' : ' ds-subtle'}`}>
        {money(supplier.outstanding)}
      </td>
      {mayManage && (
        <td>
          <div className="supplier__actions">
            <button className="ds-btn ds-btn--quiet" onClick={onEdit}>
              {t('action.edit')}
            </button>
            {/* Retiring is refused by the server while money is owed, so the
                control is hidden rather than offering something that would be
                turned down. Never a delete: orders and bills refer to them. */}
            {supplier.is_active ? (
              !owes && (
                <button
                  className="ds-btn ds-btn--quiet"
                  onClick={() => onSetActive(false)}
                >
                  {t('common.retire')}
                </button>
              )
            ) : (
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => onSetActive(true)}
              >
                {t('common.bringBack')}
              </button>
            )}
          </div>
        </td>
      )}
    </tr>
  );
}
/** What is owed, and how overdue.
 *
 * Aged from the DUE date, not the bill date — a 60-day invoice raised 45 days
 * ago is not late, and ageing it from issue would send a buyer to chase a
 * supplier who is owed nothing yet. */
function AgeingView({ companyId }: { companyId: string }) {
  const t = useT();
  const { client } = useAuth();
  const load = useCallback(() => fetchAgeing(client, companyId), [client, companyId]);
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(ageing) => (
        <div className="ds-panel">
          <div className="ds-panel__head">
            <h2 className="ds-h3">{t('purch.whatWeOwe')}</h2>
            <span className="ds-caption">as at {ageing.as_of}</span>
          </div>
          <div className="ds-panel__body ds-scroll-x">
            {ageing.rows.length === 0 ? (
              <EmptyState
                title={t('common.nothingOutstanding')}
                body="Every supplier bill has been settled."
              />
            ) : (
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('common.supplier')}</th>
                    <th scope="col" className="num">{t('common.notDue')}</th>
                    <th scope="col" className="num">1–30</th>
                    <th scope="col" className="num">31–60</th>
                    <th scope="col" className="num">61–90</th>
                    <th scope="col" className="num">90+</th>
                    <th scope="col" className="num">{t('common.total')}</th>
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
                    <td>{t('common.totalOwed')}</td>
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

// A function of the translator: a module constant is built once at import,
// before a locale exists, so it would freeze the first language loaded.
function orderStates(
  t: (key: Key) => string,
): Record<string, { label: string; tone: string }> {
  return {
    draft: { label: t('common.draft'), tone: 'ds-badge--neutral' },
    issued: { label: t('order.sentToSupplier'), tone: 'ds-badge--info' },
    receiving: { label: t('order.partDelivered'), tone: 'ds-badge--warning' },
    received: { label: t('order.fullyDelivered'), tone: 'ds-badge--success' },
    closed: { label: t('common.closed'), tone: 'ds-badge--neutral' },
    cancelled: { label: t('common.cancelled'), tone: 'ds-badge--neutral' },
  };
}

export function OrderStatus({ status }: { status: string }) {
  const t = useT();
  const known = orderStates(t)[status];
  return (
    <span className={`ds-badge ${known?.tone ?? 'ds-badge--neutral'}`}>
      {known?.label ?? status.replace(/_/g, ' ')}
    </span>
  );
}

/** A bill's state, in words a buyer reads rather than a status enum. */
export function BillStatus({ bill }: { bill: Bill }) {
  const t = useT();
  const states: Record<string, { label: string; tone: string }> = {
    draft: { label: t('common.draft'), tone: 'ds-badge--neutral' },
    matched: { label: t('common.checked'), tone: 'ds-badge--success' },
    // The one that matters. Named for what it means to the reader — the money
    // is held — rather than for the internal state.
    blocked: { label: t('bill.heldNeedsApproval'), tone: 'ds-badge--danger' },
    approved: { label: t('bill.approved'), tone: 'ds-badge--warning' },
    paid: { label: t('common.paid'), tone: 'ds-badge--success' },
    cancelled: { label: t('common.cancelled'), tone: 'ds-badge--neutral' },
  };
  const known = states[bill.status];
  return (
    <span className={`ds-badge ${known?.tone ?? 'ds-badge--neutral'}`}>
      {known?.label ?? bill.status}
    </span>
  );
}

export type { Order };

/** A form filling the page, with one click back to the list it came from.
 *
 * The same shape as a drill-through, because it is the same journey: you went
 * somewhere from a list and you need to get back. Reusing the pattern rather
 * than inventing a second one means the back control is where a reader already
 * expects it.
 */
function FormPage({
  title,
  onBack,
  children,
}: {
  title: string;
  onBack: () => void;
  children: React.ReactNode;
}) {
  return (
    <main className="detail">
      <header className="detail__head">
        <button className="detail__back" onClick={onBack}>
          <span aria-hidden="true" className="detail__backarrow">
            ←
          </span>
          {title}
        </button>
      </header>
      {children}
    </main>
  );
}