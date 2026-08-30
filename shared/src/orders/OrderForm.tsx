// Raising a quotation.
//
// # It is always raised as a quotation
//
// There is no "raise as a confirmed order" here, and the API refuses it too.
// Confirming is the customer's decision, and a form that could skip it would
// put "the customer agreed" in the hands of whoever typed the order.
//
// # No store picker
//
// A sales executive holds `order.manage`, the catalogue and the customer list,
// and nothing else. Asking them which stock location the goods will leave from
// would mean handing that role `stock.view` to fill in a field the warehouse
// decides anyway.

import { useEffect, useState } from 'react';

import { listCustomers, type Customer } from '../api/receivables';
import { raiseOrder } from '../api/orders';
import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { VariantPicker } from '../purchasing/VariantPicker';
import { money } from '../ui/format';
import {
  Field,
  FormActions,
  FormError,
  SelectInput,
  TextInput,
} from '../ui/Form';
import { lineTotal, previewTotals, readyToRaise, type DraftLine } from './orders';

const CHANNELS = ['store', 'wholesale', 'online', 'phone', 'marketplace'] as const;

function blank(): DraftLine {
  return { variantId: '', description: '', qty: '1', unitPrice: '', discount: '' };
}

export function OrderForm({
  companyId,
  onCancel,
  onRaised,
}: {
  companyId: string;
  onCancel: () => void;
  onRaised: (orderId: string) => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [customers, setCustomers] = useState<Customer[]>([]);
  const [customerId, setCustomerId] = useState('');
  const [channel, setChannel] = useState<string>('store');
  const [validUntil, setValidUntil] = useState('');
  const [deliverTo, setDeliverTo] = useState('');
  const [deliverPhone, setDeliverPhone] = useState('');
  const [notes, setNotes] = useState('');
  const [lines, setLines] = useState<DraftLine[]>([blank()]);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  // A quotation for a customer the shop has never met is a real thing — a
  // walk-in asking for a price. So the customer list failing to load does not
  // stop the form; it leaves the picker empty and the quotation unattributed.
  useEffect(() => {
    let cancelled = false;
    listCustomers(client, companyId)
      .then((rows) => {
        if (!cancelled) setCustomers(rows.filter((c) => c.is_active));
      })
      .catch(() => {
        if (!cancelled) setCustomers([]);
      });
    return () => {
      cancelled = true;
    };
  }, [client, companyId]);

  const totals = previewTotals(lines);
  // Every customer carries the COMPANY's base currency, so which one is
  // selected does not change the answer and a quotation for a walk-in is
  // priced in the same money as everybody else's.
  const currency = customers[0]?.currency;

  function setLine(i: number, patch: Partial<DraftLine>) {
    setLines((prev) => prev.map((l, j) => (j === i ? { ...l, ...patch } : l)));
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      const order = await raiseOrder(client, companyId, {
        customer_id: customerId || undefined,
        channel,
        valid_until: validUntil || undefined,
        deliver_to: deliverTo || undefined,
        deliver_phone: deliverPhone || undefined,
        notes: notes || undefined,
        lines: lines
          .filter((l) => l.variantId !== '')
          .map((l) => ({
            variant_id: l.variantId,
            description: l.description || undefined,
            qty: l.qty,
            unit_price: l.unitPrice || '0',
            discount: l.discount || undefined,
          })),
      });
      onRaised(order.id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel orders__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('orders.raise')}</h2>
        <p className="ds-caption">{t('orders.raiseHint')}</p>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field
            label={t('orders.customer')}
            hint={t('orders.customerHint')}
            htmlFor="order-customer"
          >
            <SelectInput
              id="order-customer"
              value={customerId}
              onChange={setCustomerId}
              options={customers}
              label={(c) => `${c.name} (${c.code})`}
              placeholder={t('orders.walkIn')}
            />
          </Field>

          <Field label={t('orders.channel')} htmlFor="order-channel">
            <SelectInput
              id="order-channel"
              value={channel}
              onChange={setChannel}
              options={CHANNELS.map((c) => ({ id: c }))}
              label={(c) => t(`orders.channel.${c.id}` as Key)}
            />
          </Field>

          <Field
            label={t('orders.validUntil')}
            hint={t('orders.validUntilHint')}
            htmlFor="order-valid"
          >
            <input
              id="order-valid"
              type="date"
              className="field__input"
              value={validUntil}
              onChange={(e) => setValidUntil(e.target.value)}
            />
          </Field>

          <Field label={t('orders.deliverTo')} htmlFor="order-deliver">
            <TextInput id="order-deliver" value={deliverTo} onChange={setDeliverTo} />
          </Field>

          <Field label={t('orders.deliverPhone')} htmlFor="order-phone">
            <TextInput
              id="order-phone"
              value={deliverPhone}
              onChange={setDeliverPhone}
              inputMode="tel"
            />
          </Field>

          <Field label={t('orders.notes')} htmlFor="order-notes">
            <TextInput id="order-notes" value={notes} onChange={setNotes} />
          </Field>
        </div>

        <div className="ds-scroll-x">
          <table className="ds-table orders__lines">
            <thead>
              <tr>
                <th scope="col">{t('orders.item')}</th>
                <th scope="col" className="num">
                  {t('orders.qty')}
                </th>
                <th scope="col" className="num">
                  {t('orders.unitPrice')}
                </th>
                <th scope="col" className="num">
                  {t('orders.discount')}
                </th>
                <th scope="col" className="num">
                  {t('orders.lineTotal')}
                </th>
                <th scope="col">
                  <span className="ds-visually-hidden">{t('common.actions')}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {lines.map((l, i) => (
                <tr key={i}>
                  <td>
                    <VariantPicker
                      companyId={companyId}
                      value={l.variantId}
                      description={l.description}
                      onPick={(v) =>
                        setLine(i, { variantId: v.id, description: v.description })
                      }
                    />
                  </td>
                  <td className="num">
                    <TextInput
                      id={`line-qty-${i}`}
                      value={l.qty}
                      onChange={(v) => setLine(i, { qty: v })}
                      inputMode="decimal"
                    />
                  </td>
                  <td className="num">
                    <TextInput
                      id={`line-price-${i}`}
                      value={l.unitPrice}
                      onChange={(v) => setLine(i, { unitPrice: v })}
                      inputMode="decimal"
                    />
                  </td>
                  <td className="num">
                    <TextInput
                      id={`line-discount-${i}`}
                      value={l.discount}
                      onChange={(v) => setLine(i, { discount: v })}
                      inputMode="decimal"
                    />
                  </td>
                  <td className="num">{money(lineTotal(l), { currency })}</td>
                  <td>
                    <button
                      type="button"
                      className="ds-btn ds-btn--quiet"
                      onClick={() =>
                        setLines((prev) =>
                          prev.length === 1
                            ? [blank()]
                            : prev.filter((_, j) => j !== i),
                        )
                      }
                    >
                      {t('action.remove')}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <button
          type="button"
          className="ds-btn ds-btn--quiet"
          onClick={() => setLines((prev) => [...prev, blank()])}
        >
          {t('orders.addLine')}
        </button>

        <dl className="orders__totals">
          <div>
            <dt>{t('orders.subtotal')}</dt>
            <dd className="num">{money(totals.subtotal, { currency })}</dd>
          </div>
          <div>
            <dt>{t('orders.discount')}</dt>
            <dd className="num">{money(totals.discount, { currency })}</dd>
          </div>
          <div className="orders__totals-final">
            <dt>{t('orders.total')}</dt>
            <dd className="num">{money(totals.total, { currency })}</dd>
          </div>
        </dl>

        <FormActions
          submitLabel={t('orders.raise')}
          busy={busy}
          disabled={!readyToRaise(lines)}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
