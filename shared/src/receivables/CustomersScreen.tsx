// Customers.
//
// Two views on one screen, because they answer two questions a shop asks
// constantly and asks together: who are our customers and what does each owe,
// and — across all of them — what is overdue and how badly. The mirror of the
// Buying screen, deliberately: somebody who has used one should not have to
// learn the other.
//
// Everything here reuses what already exists: the DetailScreen frame, the
// useRemote hook and its five states, the design system's tables and badges.
// Nothing new was invented for this module.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { money } from '../ui/format';
import {
  listCustomers,
  readAgeing,
  setCustomerActive,
  type AgeingRow,
  type Customer,
} from '../api/receivables';
import { ageingTone, ageingTotals, creditStanding, worstBucket } from './receivables';
import { CustomerForm } from './CustomerForm';
import { CustomerDetail } from './CustomerDetail';
import { useT } from '../i18n/locale';

type Tab = 'customers' | 'ageing';

export function CustomersScreen({ companyId }: { companyId: string }) {
  const t = useT();
  const { can } = useAuth();
  const [tab, setTab] = useState<Tab>('customers');
  const [open, setOpen] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<Customer | null>(null);

  // Bumped after a save so the list underneath refetches. Cheaper and harder to
  // get wrong than threading a refresh callback through every list.
  const [saved, setSaved] = useState(0);

  const mayAdd = can('customers.manage');

  if (creating || editing) {
    const done = () => {
      setCreating(false);
      setEditing(null);
    };
    return (
      <FormPage title={t('common.customers')} onBack={done}>
        <CustomerForm
          companyId={companyId}
          existing={editing ?? undefined}
          onSaved={(customer) => {
            done();
            setSaved((n) => n + 1);
            // Straight to the account just opened, because the next thing
            // somebody does is look at it or put a sale on it.
            setOpen(customer.id);
          }}
          onCancel={done}
        />
      </FormPage>
    );
  }

  if (open) {
    return (
      <CustomerDetail
        companyId={companyId}
        customerId={open}
        onBack={() => {
          setOpen(null);
          setSaved((n) => n + 1);
        }}
        onEdit={setEditing}
      />
    );
  }

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('common.customers')}</h1>
          <p className="ds-caption">{t('cust.overview')}</p>
        </div>

        <div className="detail__actions">
          {/* Shown only when the permission is held — the server refuses it
              either way, but a button that always refuses teaches people to
              distrust the rest of them. */}
          {tab === 'customers' && mayAdd && (
            <button className="ds-btn ds-btn--primary" onClick={() => setCreating(true)}>
              {t('cust.addCustomer')}
            </button>
          )}

          <div className="segmented" role="group" aria-label={t('common.whatToShow')}>
            {(
              [
                ['customers', 'Customers'],
                ['ageing', "What we're owed"],
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

      {tab === 'customers' && (
        <Customers
          key={saved}
          companyId={companyId}
          onOpen={setOpen}
          onEdit={setEditing}
          onChanged={() => setSaved((n) => n + 1)}
        />
      )}
      {tab === 'ageing' && <AgeingView companyId={companyId} onOpen={setOpen} />}
    </main>
  );
}

function Customers({
  companyId,
  onOpen,
  onEdit,
  onChanged,
}: {
  companyId: string;
  onOpen: (id: string) => void;
  onEdit: (customer: Customer) => void;
  onChanged: () => void;
}) {
  const t = useT();
  const { client, can } = useAuth();
  // Retired customers sit behind a toggle rather than being hidden outright.
  // They are referenced by invoices already issued, so somebody looking one up
  // must be able to find them — but they must not clutter the list a cashier
  // picks from every day.
  const [showRetired, setShowRetired] = useState(false);
  const [search, setSearch] = useState('');
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(
    () => listCustomers(client, companyId, search, showRetired),
    [client, companyId, search, showRetired],
  );
  const { remote, reload } = useRemote(load);

  const mayManage = can('customers.manage');

  async function setActive(customer: Customer, active: boolean) {
    setNotice(null);
    try {
      await setCustomerActive(client, companyId, customer.id, active);
      reload();
      onChanged();
    } catch (err) {
      // The server refuses to retire a customer who still owes money, and the
      // refusal names the amount. Shown as-is: it says what to do.
      setNotice(err instanceof Error ? err.message : 'That did not work.');
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(customers) => (
        <>
          {notice && (
            <p className="ds-panel purchase__notice" role="alert">
              {notice}
            </p>
          )}

          <div className="ds-panel">
            <div className="ds-panel__head">
              <h2 className="ds-h3">{t('common.customers')}</h2>
              <div className="customer__filters">
                <label className="ds-visually-hidden" htmlFor="cust-search">
                  {t('cust.searchCustomers')}
                </label>
                <input
                  id="cust-search"
                  className="input customer__search"
                  value={search}
                  placeholder={t('cust.searchBy')}
                  onChange={(e) => setSearch(e.target.value)}
                />
                <label className="supplier__toggle">
                  <input
                    type="checkbox"
                    checked={showRetired}
                    onChange={(e) => setShowRetired(e.target.checked)}
                  />
                  <span className="ds-caption">{t('common.includeRetired')}</span>
                </label>
              </div>
            </div>

            <div className="ds-panel__body ds-scroll-x">
              {customers.length === 0 ? (
                <EmptyState
                  title={search ? 'Nobody matches that' : 'No customers yet'}
                  body={
                    search
                      ? 'Try a shorter search, or clear it to see everyone.'
                      : 'Add the people and businesses you sell to. Their payment terms set when each invoice falls due, and their credit limit caps what they may owe at once.'
                  }
                />
              ) : (
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('common.customer')}</th>
                      <th scope="col">{t('common.terms')}</th>
                      <th scope="col" className="num">
                        {t('common.owed')}
                      </th>
                      <th scope="col" className="num">
                        {t('common.limit')}
                      </th>
                      <th scope="col">{t('common.account')}</th>
                      {mayManage && <th scope="col" />}
                    </tr>
                  </thead>
                  <tbody>
                    {customers.map((c) => (
                      <CustomerRow
                        key={c.id}
                        customer={c}
                        mayManage={mayManage}
                        onOpen={() => onOpen(c.id)}
                        onEdit={() => onEdit(c)}
                        onSetActive={(active) => void setActive(c, active)}
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

function CustomerRow({
  customer,
  mayManage,
  onOpen,
  onEdit,
  onSetActive,
}: {
  customer: Customer;
  mayManage: boolean;
  onOpen: () => void;
  onEdit: () => void;
  onSetActive: (active: boolean) => void;
}) {
  const t = useT();
  const owes = Number(customer.balance) > 0;
  const standing = creditStanding(customer);

  return (
    <tr className={customer.is_active ? undefined : 'detail__row--aside'}>
      <td>
        <button className="detail__rowbtn" onClick={onOpen}>
          <span className="detail__strong">{customer.name}</span>
          <span className="ds-caption">
            {customer.code}
            {customer.customer_type !== 'retail' && ` · ${customer.customer_type}`}
            {!customer.is_active && ' · retired'}
          </span>
        </button>
      </td>
      <td>
        {customer.payment_terms_days === 0
          ? 'At the till'
          : `${customer.payment_terms_days} days`}
      </td>
      <td className={`num${owes ? '' : ' ds-subtle'}`}>{money(customer.balance)}</td>
      <td className="num">
        {customer.credit_limit ? (
          money(customer.credit_limit)
        ) : (
          <span className="ds-subtle">—</span>
        )}
      </td>
      <td>
        <AccountBadge kind={standing.kind} />
      </td>
      {mayManage && (
        <td>
          <div className="supplier__actions">
            <button className="ds-btn ds-btn--quiet" onClick={onEdit}>
              {t('action.edit')}
            </button>
            {/* Retiring is refused by the server while money is owed, so the
                control is hidden rather than offering something that would be
                turned down. Never a delete: invoices refer to them. */}
            {customer.is_active ? (
              !owes && (
                <button className="ds-btn ds-btn--quiet" onClick={() => onSetActive(false)}>
                  {t('common.retire')}
                </button>
              )
            ) : (
              <button className="ds-btn ds-btn--quiet" onClick={() => onSetActive(true)}>
                {t('common.bringBack')}
              </button>
            )}
          </div>
        </td>
      )}
    </tr>
  );
}

/** What state a customer's credit account is in, in words rather than a number. */
function AccountBadge({ kind }: { kind: ReturnType<typeof creditStanding>['kind'] }) {
  const states: Record<
    ReturnType<typeof creditStanding>['kind'],
    { label: string; tone: string }
  > = {
    none: { label: 'Pays at the till', tone: 'ds-badge--neutral' },
    clear: { label: 'Credit available', tone: 'ds-badge--success' },
    near_limit: { label: 'Near limit', tone: 'ds-badge--warning' },
    // The one that matters at a counter. Named for what it means to a cashier —
    // nothing further can go on — rather than for the internal state.
    at_limit: { label: 'At limit', tone: 'ds-badge--danger' },
  };
  const known = states[kind];
  return <span className={`ds-badge ${known.tone}`}>{known.label}</span>;
}

/** What is owed to the shop, and how overdue.
 *
 * Aged from the DUE date, not the invoice date — a 30-day invoice raised 20 days
 * ago is not late, and ageing it from issue would send somebody to chase a
 * customer who owes nothing yet. */
function AgeingView({
  companyId,
  onOpen,
}: {
  companyId: string;
  onOpen: (id: string) => void;
}) {
  const t = useT();
  const { client } = useAuth();
  const load = useCallback(() => readAgeing(client, companyId), [client, companyId]);
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(ageing) => {
        const totals = ageingTotals(ageing.rows);
        return (
          <div className="ds-panel">
            <div className="ds-panel__head">
              <h2 className="ds-h3">{t('cust.whatWereOwed')}</h2>
              <span className="ds-caption">as at {ageing.as_of}</span>
            </div>
            <div className="ds-panel__body ds-scroll-x">
              {ageing.rows.length === 0 ? (
                <EmptyState
                  title={t('common.nothingOutstanding')}
                  body="Every customer invoice has been settled. Sales put on account will appear here until they are paid."
                />
              ) : (
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('common.customer')}</th>
                      <th scope="col" className="num">
                        {t('common.notDue')}
                      </th>
                      <th scope="col" className="num">
                        1–30
                      </th>
                      <th scope="col" className="num">
                        31–60
                      </th>
                      <th scope="col" className="num">
                        61–90
                      </th>
                      <th scope="col" className="num">
                        90+
                      </th>
                      <th scope="col" className="num">
                        {t('common.total')}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {ageing.rows.map((r) => (
                      <AgeingLine key={r.customer_id} row={r} onOpen={() => onOpen(r.customer_id)} />
                    ))}
                  </tbody>
                  <tfoot>
                    <tr>
                      <td>{t('common.totalOwed')}</td>
                      <td className="num ds-muted">{money(totals.not_due)}</td>
                      <td className="num">{money(totals.days_0_30)}</td>
                      <td className="num">{money(totals.days_31_60)}</td>
                      <td className="num ds-down">{money(totals.days_61_90)}</td>
                      <td className="num ds-down">{money(totals.days_90_plus)}</td>
                      <td className="num">
                        {money(ageing.total, { currency: ageing.base_currency })}
                      </td>
                    </tr>
                  </tfoot>
                </table>
              )}
            </div>
          </div>
        );
      }}
    </RemoteBody>
  );
}

function AgeingLine({ row, onOpen }: { row: AgeingRow; onOpen: () => void }) {
  const tone = ageingTone(worstBucket(row));
  return (
    <tr>
      <td>
        <button className="detail__rowbtn" onClick={onOpen}>
          <span className="detail__strong">{row.customer}</span>
          {/* Only said when it is worth acting on. A label on every row would
              stop meaning anything. */}
          {tone === 'danger' && <span className="ds-caption ds-down">overdue 60+ days</span>}
        </button>
      </td>
      <td className="num ds-muted">{money(row.not_due)}</td>
      <td className="num">{money(row.days_0_30)}</td>
      <td className="num">{money(row.days_31_60)}</td>
      {/* Past 60 days is where money starts not to arrive at all, so it is
          emphasised rather than left as one more column of grey figures. */}
      <td className="num ds-down">{money(row.days_61_90)}</td>
      <td className="num ds-down">{money(row.days_90_plus)}</td>
      <td className="num">{money(row.total)}</td>
    </tr>
  );
}

/** A form filling the page, with one click back to the list it came from.
 *
 * The same shape as the Buying screen's, because it is the same journey. */
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
