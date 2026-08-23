// Taking a payment from a customer.
//
// The form allocates explicitly, invoice by invoice. The server refuses to guess
// which invoices a payment settles, because a wrong guess becomes a statement
// the customer disputes — and a disputed statement is a week of phone calls. But
// nobody has decided yet at the moment this form opens, so it OFFERS oldest-first
// as a starting point and lets the person move it. Suggesting is not the same as
// assuming.
//
// # The identifier is minted once, when the form opens
//
// Not per attempt. If the network drops after the server committed, pressing
// Save again sends the SAME uuid and the server recognises it rather than taking
// the money twice — which is the whole point of the discipline, and would be
// defeated by generating a fresh one on each submit.

import { useCallback, useMemo, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { money, shortDate } from '../ui/format';
import {
  listOpenInvoices,
  takePayment,
  type Customer,
  type OpenInvoice,
} from '../api/receivables';
import { allocateOldestFirst, checkAllocation, major, minor } from './receivables';
import { Field, FormActions, FormError, SelectInput, TextInput } from '../ui/Form';
import { useT } from '../i18n/locale';

const METHODS = [
  { id: 'cash', label: 'Cash' },
  { id: 'bank_transfer', label: 'Bank transfer' },
  { id: 'mada', label: 'Mada' },
  { id: 'visa', label: 'Visa' },
  { id: 'mastercard', label: 'Mastercard' },
  { id: 'cheque', label: 'Cheque' },
  { id: 'sadad', label: 'SADAD' },
];

export function ReceiptForm({
  companyId,
  customer,
  onSaved,
  onCancel,
}: {
  companyId: string;
  customer: Customer;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const { client } = useAuth();

  // Minted once. See the file comment: a retry must carry the same one.
  const [receiptUUID] = useState(() => crypto.randomUUID());

  const [method, setMethod] = useState('cash');
  const [reference, setReference] = useState('');
  const [received, setReceived] = useState('');
  const [allocations, setAllocations] = useState<Record<string, string>>({});
  const [touched, setTouched] = useState(false);

  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(
    () => listOpenInvoices(client, companyId, customer.id),
    [client, companyId, customer.id],
  );
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(invoices) => (
        <ReceiptBody
          companyId={companyId}
          customer={customer}
          invoices={invoices}
          receiptUUID={receiptUUID}
          method={method}
          setMethod={setMethod}
          reference={reference}
          setReference={setReference}
          received={received}
          setReceived={setReceived}
          allocations={allocations}
          setAllocations={setAllocations}
          touched={touched}
          setTouched={setTouched}
          failure={failure}
          setFailure={setFailure}
          busy={busy}
          setBusy={setBusy}
          onSaved={onSaved}
          onCancel={onCancel}
          send={(body) => takePayment(client, companyId, body)}
        />
      )}
    </RemoteBody>
  );
}

function ReceiptBody({
  customer,
  invoices,
  receiptUUID,
  method,
  setMethod,
  reference,
  setReference,
  received,
  setReceived,
  allocations,
  setAllocations,
  touched,
  setTouched,
  failure,
  setFailure,
  busy,
  setBusy,
  onSaved,
  onCancel,
  send,
}: {
  companyId: string;
  customer: Customer;
  invoices: OpenInvoice[];
  receiptUUID: string;
  method: string;
  setMethod: (v: string) => void;
  reference: string;
  setReference: (v: string) => void;
  received: string;
  setReceived: (v: string) => void;
  allocations: Record<string, string>;
  setAllocations: (v: Record<string, string>) => void;
  touched: boolean;
  setTouched: (v: boolean) => void;
  failure: string | null;
  setFailure: (v: string | null) => void;
  busy: boolean;
  setBusy: (v: boolean) => void;
  onSaved: () => void;
  onCancel: () => void;
  send: (body: {
    uuid: string;
    customer_id: string;
    method: string;
    reference?: string;
    allocations: { invoice_id: string; amount: string }[];
  }) => Promise<unknown>;
}) {
  const t = useT();
  const outcome = useMemo(
    () => checkAllocation(invoices, allocations),
    [invoices, allocations],
  );

  const allocated = outcome.kind === 'ok' ? outcome.total : '0.00';
  // What the person said they received, less what they have placed. Shown so a
  // part-allocated payment is visible rather than being silently short.
  const unplaced = major(minor(received) - minor(allocated));

  /** Spread what was received over the oldest invoices, as a starting point. */
  function suggest(amount: string) {
    setReceived(amount);
    if (!touched) {
      setAllocations(allocateOldestFirst(invoices, amount).allocations);
    }
  }

  function setOne(invoiceId: string, amount: string) {
    setTouched(true);
    const next = { ...allocations };
    if (amount.trim() === '') delete next[invoiceId];
    else next[invoiceId] = amount;
    setAllocations(next);
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (outcome.kind !== 'ok') {
      setFailure(
        outcome.kind === 'nothing'
          ? t('receipt.allocationHint')
          : `Invoice ${outcome.invoice} has ${outcome.outstanding} outstanding, ` +
            `less than the ${outcome.amount} allocated to it.`,
      );
      return;
    }

    setBusy(true);
    setFailure(null);
    try {
      await send({
        uuid: receiptUUID,
        customer_id: customer.id,
        method,
        reference: reference.trim() || undefined,
        allocations: Object.entries(allocations)
          .filter(([, amount]) => minor(amount) > 0n)
          .map(([invoice_id, amount]) => ({ invoice_id, amount })),
      });
      onSaved();
    } catch (err) {
      if (err instanceof Offline) {
        setFailure(
          t('receipt.saveOffline') +
            t('receipt.notTakenTwice'),
        );
      } else if (err instanceof RequestFailed) {
        setFailure(err.message);
      } else {
        setFailure(err instanceof Error ? err.message : 'That did not save.');
      }
    } finally {
      setBusy(false);
    }
  }

  if (invoices.length === 0) {
    return (
      <div className="ds-panel">
        <div className="ds-panel__body">
          <EmptyState
            title={t('common.nothingOutstanding')}
            body={`${customer.name} has settled everything on their account, so there is nothing to allocate a payment against.`}
          />
          <div className="form__actions">
            <button className="ds-btn ds-btn--quiet" type="button" onClick={onCancel}>
              {t('rcpt.backToAccount')}
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <form className="ds-panel form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">Payment from {customer.name}</h2>
      </div>

      <div className="ds-panel__body form__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label={t('rcpt.amountReceived')} htmlFor="rec-amount" required
            hint="Type what the customer handed over and the invoices below fill in oldest first. Change any of them.">
            <TextInput id="rec-amount" value={received} onChange={suggest}
              inputMode="decimal" placeholder="0.00" autoFocus />
          </Field>

          <Field label={t('rcpt.howTheyPaid')} htmlFor="rec-method" required>
            <SelectInput id="rec-method" value={method} onChange={setMethod}
              options={METHODS} label={(o) => o.label} />
          </Field>

          <Field label={t('common.reference')} htmlFor="rec-ref"
            hint="A cheque number or a transfer reference, if there is one.">
            <TextInput id="rec-ref" value={reference} onChange={setReference} />
          </Field>
        </div>

        <div className="ds-scroll-x">
          <table className="ds-table">
            <caption className="ds-caption receipt__caption">
              {t('rcpt.whatItSettles')}
            </caption>
            <thead>
              <tr>
                <th scope="col">{t('common.invoice')}</th>
                <th scope="col">{t('common.due')}</th>
                <th scope="col" className="num">{t('common.outstanding')}</th>
                <th scope="col" className="num">{t('rcpt.allocate')}</th>
              </tr>
            </thead>
            <tbody>
              {invoices.map((invoice) => {
                const label = invoice.human_number || invoice.invoice_id.slice(0, 8);
                const over =
                  minor(allocations[invoice.invoice_id] ?? '0') >
                  minor(invoice.outstanding);
                return (
                  <tr key={invoice.invoice_id}>
                    <td>
                      <span className="detail__strong">{label}</span>
                      {/* Shown because a customer disputing a balance needs the
                          difference between money they paid and goods they
                          brought back. */}
                      {minor(invoice.credited) > 0n && (
                        <span className="ds-caption">
                          {money(invoice.credited)} returned
                        </span>
                      )}
                    </td>
                    <td className="num">{shortDate(invoice.due_date)}</td>
                    <td className="num">{money(invoice.outstanding)}</td>
                    <td className="num">
                      <input
                        className={`input num receipt__alloc${over ? ' input--bad' : ''}`}
                        value={allocations[invoice.invoice_id] ?? ''}
                        inputMode="decimal"
                        aria-label={`Allocate to invoice ${label}`}
                        aria-invalid={over ? true : undefined}
                        onChange={(e) => setOne(invoice.invoice_id, e.target.value)}
                      />
                    </td>
                  </tr>
                );
              })}
            </tbody>
            <tfoot>
              <tr>
                <td colSpan={2}>{t('rcpt.allocated')}</td>
                <td className="num ds-subtle">{money(received || '0.00')} received</td>
                <td className="num">{money(allocated)}</td>
              </tr>
              {minor(unplaced) !== 0n && (
                <tr>
                  <td colSpan={3}>
                    {minor(unplaced) > 0n
                      ? t('receipt.unallocated')
                      : t('receipt.overAllocated')}
                  </td>
                  <td className="num ds-down">{money(unplaced)}</td>
                </tr>
              )}
            </tfoot>
          </table>
        </div>

        <FormActions submitLabel="Record payment" busy={busy} onCancel={onCancel} />
      </div>
    </form>
  );
}
