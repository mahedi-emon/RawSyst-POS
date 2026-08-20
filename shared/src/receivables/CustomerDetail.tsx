// One customer's account.
//
// Three things a person needs about a customer who owes money, on one screen
// because they are asked together: how much and how overdue, the history behind
// that figure, and a way to take a payment. Splitting them would make the
// commonest journey — customer rings up, you check what they owe, they pay —
// a three-screen trip.
//
// # The credit limit sits with the balance, not in the edit form
//
// It is the one number here that is a decision rather than a record, and it
// carries its own permission (`customers.set_credit_limit`). Putting it beside
// the balance means whoever raises it is looking at what is already owed while
// they do it.

import { useCallback, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { money, shortDate } from '../ui/format';
import {
  readLedger,
  reversePayment,
  setCreditLimit,
  type Customer,
  type LedgerRow,
} from '../api/receivables';
import { canReversePayment, creditStanding } from './receivables';
import { ReceiptForm } from './ReceiptForm';

export function CustomerDetail({
  companyId,
  customerId,
  onBack,
  onEdit,
}: {
  companyId: string;
  customerId: string;
  onBack: () => void;
  onEdit: (customer: Customer) => void;
}) {
  const { client, can } = useAuth();
  const [taking, setTaking] = useState(false);
  const [confirming, setConfirming] = useState<{ id: string; uuid: string } | null>(null);
  const [reverseBusy, setReverseBusy] = useState(false);
  const [reverseFailure, setReverseFailure] = useState<string | null>(null);

  const load = useCallback(
    () => readLedger(client, companyId, customerId),
    [client, companyId, customerId],
  );
  const { remote, reload } = useRemote(load);

  const mayTakePayment = can('sales.receive_payment');
  const mayManage = can('customers.manage');

  return (
    <main className="detail">
      <header className="detail__head">
        <button className="detail__back" onClick={onBack}>
          <span aria-hidden="true" className="detail__backarrow">
            ←
          </span>
          Customers
        </button>
      </header>

      <RemoteBody remote={remote} onRetry={reload}>
        {(ledger) => {
          const customer = ledger.customer;
          const owes = Number(customer.balance) > 0;

          if (taking) {
            return (
              <ReceiptForm
                companyId={companyId}
                customer={customer}
                onSaved={() => {
                  setTaking(false);
                  reload();
                }}
                onCancel={() => setTaking(false)}
              />
            );
          }

          return (
            <>
              <div className="detail__head detail__head--flat">
                <div className="detail__titles">
                  <h1 className="ds-h1">{customer.name}</h1>
                  <p className="ds-caption">
                    {customer.code} · {typeLabel(customer.customer_type)}
                    {customer.payment_terms_days === 0
                      ? ' · paid at the till'
                      : ` · ${customer.payment_terms_days} day terms`}
                    {!customer.is_active && ' · retired'}
                  </p>
                </div>
                <div className="detail__actions">
                  {mayTakePayment && owes && (
                    <button
                      className="ds-btn ds-btn--primary"
                      onClick={() => setTaking(true)}
                    >
                      Record a payment
                    </button>
                  )}
                  {mayManage && (
                    <button className="ds-btn ds-btn--quiet" onClick={() => onEdit(customer)}>
                      Edit details
                    </button>
                  )}
                </div>
              </div>

              <AccountStanding
                companyId={companyId}
                customer={customer}
                currency={ledger.base_currency}
                onChanged={reload}
              />

              <div className="ds-panel">
                <div className="ds-panel__head">
                  <h2 className="ds-h3">Account history</h2>
                  <span className="ds-caption">
                    closing {money(ledger.closing, { currency: ledger.base_currency })}
                  </span>
                </div>
                <div className="ds-panel__body ds-scroll-x">
                  {reverseFailure && (
                    <p className="form__error" role="alert">
                      {reverseFailure}
                    </p>
                  )}
                  {ledger.rows.length === 0 ? (
                    <EmptyState
                      title="Nothing on this account yet"
                      body="Sales put on account appear here, along with every payment received against them."
                    />
                  ) : (
                    <table className="ds-table">
                      <thead>
                        <tr>
                          <th scope="col">Date</th>
                          <th scope="col">Reference</th>
                          <th scope="col">Due</th>
                          <th scope="col" className="num">
                            Charged
                          </th>
                          <th scope="col" className="num">
                            Received
                          </th>
                          <th scope="col" className="num">
                            Balance
                          </th>
                          <th scope="col">
                            <span className="ds-visually-hidden">Actions</span>
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {ledger.rows.map((row, i) => (
                          <LedgerLine
                            key={`${row.kind}-${row.reference}-${i}`}
                            row={row}
                            currency={ledger.base_currency}
                            confirming={confirming?.id === row.source_id}
                            busy={reverseBusy}
                            mayReverse={mayTakePayment && canReversePayment(row)}
                            onAsk={() => {
                              if (!row.source_id) return;
                              setReverseFailure(null);
                              setConfirming({
                                id: row.source_id,
                                uuid: crypto.randomUUID(),
                              });
                            }}
                            onCancel={() => setConfirming(null)}
                            onConfirm={() => {
                              if (!row.source_id || !confirming) return;
                              setReverseBusy(true);
                              setReverseFailure(null);
                              void reversePayment(
                                client,
                                companyId,
                                row.source_id,
                                confirming.uuid,
                              )
                                .then(() => {
                                  setConfirming(null);
                                  reload();
                                })
                                .catch((err: unknown) => {
                                  if (err instanceof Offline) {
                                    setReverseFailure(
                                      'This device cannot reach the server, so the payment was not reversed.',
                                    );
                                  } else if (err instanceof RequestFailed) {
                                    setReverseFailure(err.message);
                                  } else {
                                    setReverseFailure(
                                      err instanceof Error
                                        ? err.message
                                        : 'That payment could not be reversed.',
                                    );
                                  }
                                })
                                .finally(() => setReverseBusy(false));
                            }}
                          />
                        ))}
                      </tbody>
                      <tfoot>
                        <tr>
                          <td colSpan={5}>Owed now</td>
                          <td className="num">
                            {money(ledger.closing, { currency: ledger.base_currency })}
                          </td>
                          <td />
                        </tr>
                      </tfoot>
                    </table>
                  )}
                </div>
              </div>
            </>
          );
        }}
      </RemoteBody>
    </main>
  );
}

/** What is owed, against what they are allowed to owe. */
function AccountStanding({
  companyId,
  customer,
  currency,
  onChanged,
}: {
  companyId: string;
  customer: Customer;
  currency: string;
  onChanged: () => void;
}) {
  const { client, can } = useAuth();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(customer.credit_limit ?? '');
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const standing = creditStanding(customer);
  const maySetLimit = can('customers.set_credit_limit');

  async function save() {
    setBusy(true);
    setFailure(null);
    try {
      await setCreditLimit(client, companyId, customer.id, draft.trim());
      setEditing(false);
      onChanged();
    } catch (err) {
      if (err instanceof Offline) {
        setFailure('This device cannot reach the server, so the limit was not changed.');
      } else if (err instanceof RequestFailed) {
        setFailure(err.message);
      } else {
        setFailure(err instanceof Error ? err.message : 'That did not save.');
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="ds-panel">
      <div className="ds-panel__body customer__standing">
        <div className="customer__figure">
          <span className="ds-caption">Owed now</span>
          <strong className="customer__amount num">
            {money(customer.balance, { currency })}
          </strong>
        </div>

        <div className="customer__figure">
          <span className="ds-caption">Credit limit</span>
          {customer.credit_limit ? (
            <strong className="customer__amount num">
              {money(customer.credit_limit, { currency })}
            </strong>
          ) : (
            <strong className="customer__amount ds-subtle">None</strong>
          )}
        </div>

        {/* The figure somebody about to sell on account actually needs, rather
            than making them subtract two others in their head at the counter. */}
        <div className="customer__figure">
          <span className="ds-caption">Available</span>
          {standing.kind === 'none' || standing.kind === 'at_limit' ? (
            <strong className="customer__amount ds-subtle">—</strong>
          ) : (
            <strong className="customer__amount num">
              {money(standing.available, { currency })}
            </strong>
          )}
        </div>

        <p
          className={`customer__standingnote${
            standing.kind === 'clear' ? '' : ' customer__standingnote--flag'
          }`}
        >
          {standing.message}
        </p>

        {maySetLimit && !editing && (
          <button className="ds-btn ds-btn--quiet" onClick={() => setEditing(true)}>
            {customer.credit_limit ? 'Change limit' : 'Set a limit'}
          </button>
        )}
      </div>

      {maySetLimit && editing && (
        <div className="ds-panel__body customer__limitform">
          <label className="field__label" htmlFor="cust-limit-edit">
            Credit limit
          </label>
          <p className="field__hint" id="cust-limit-hint">
            The most {customer.name} may owe at once. Empty removes the account, so
            every sale must then be paid at the till. Lowering it below what is
            already owed is allowed — it stops anything further going on.
          </p>
          <div className="customer__limitrow">
            <input
              id="cust-limit-edit"
              className="input num"
              value={draft}
              inputMode="decimal"
              aria-describedby="cust-limit-hint"
              placeholder="Empty for no credit"
              onChange={(e) => setDraft(e.target.value)}
            />
            <button
              className="ds-btn ds-btn--primary"
              disabled={busy}
              onClick={() => void save()}
            >
              {busy ? 'Saving…' : 'Save limit'}
            </button>
            <button
              className="ds-btn ds-btn--quiet"
              disabled={busy}
              onClick={() => {
                setEditing(false);
                setDraft(customer.credit_limit ?? '');
                setFailure(null);
              }}
            >
              Cancel
            </button>
          </div>
          {failure && (
            <p className="form__error" role="alert">
              {failure}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function LedgerLine({
  row,
  currency,
  confirming,
  busy,
  mayReverse,
  onAsk,
  onCancel,
  onConfirm,
}: {
  row: LedgerRow;
  currency: string;
  confirming: boolean;
  busy: boolean;
  mayReverse: boolean;
  onAsk: () => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <tr>
      <td className="num">{shortDate(row.date)}</td>
      <td>
        <span className="detail__strong">{row.reference}</span>
        <span className="ds-caption">{kindLabel(row.kind)}</span>
      </td>
      <td className="num">
        {row.due_date ? shortDate(row.due_date) : <span className="ds-subtle">—</span>}
      </td>
      <td className="num">
        {row.charged ? money(row.charged, { currency }) : <span className="ds-subtle">—</span>}
      </td>
      <td className="num">
        {row.received ? money(row.received, { currency }) : <span className="ds-subtle">—</span>}
      </td>
      <td className="num">{money(row.balance, { currency })}</td>
      <td>
        {mayReverse && !confirming && (
          <button className="ds-btn ds-btn--quiet" disabled={busy} onClick={onAsk}>
            Reverse
          </button>
        )}
        {mayReverse && confirming && (
          <>
            <button className="ds-btn ds-btn--primary" disabled={busy} onClick={onConfirm}>
              {busy ? 'Reversing…' : 'Confirm reverse'}
            </button>
            <button className="ds-btn ds-btn--quiet" disabled={busy} onClick={onCancel}>
              Cancel
            </button>
          </>
        )}
      </td>
    </tr>
  );
}

function kindLabel(kind: LedgerRow['kind']): string {
  switch (kind) {
    case 'sale':
      return 'Sold on account';
    // Named for what happened rather than for the document, because a customer
    // reading a statement needs to see this was goods coming back and not a
    // payment they never made.
    case 'credit':
      return 'Returned — credited';
    case 'reversal':
      return 'Payment reversed';
    default:
      return 'Payment received';
  }
}

function typeLabel(type: Customer['customer_type']): string {
  switch (type) {
    case 'wholesale':
      return 'Wholesale';
    case 'vip':
      return 'VIP';
    default:
      return 'Retail';
  }
}
