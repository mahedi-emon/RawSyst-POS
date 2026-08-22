// Recording what a supplier has invoiced.
//
// This is the step that creates the payable and runs the three-way match, so
// the form's job is to capture what the SUPPLIER SAID rather than what the shop
// expected. Those two agreeing is the good case; them disagreeing is the entire
// reason the match exists, and a form that quietly corrected the difference
// would destroy the control it feeds.
//
// # It prefills from the order, and that is not the same as assuming
//
// Choosing a purchase order fills the lines with what is outstanding on it at
// the agreed cost, because a supplier billing exactly what they delivered is
// the common case by a wide margin and retyping it invites transcription
// errors. Every figure stays editable, and the buyer is expected to change
// whatever the invoice in their hand actually says.
//
// # A bill with no order is legitimate
//
// Rent, a utility bill, a consultant. The purchase order field is optional and
// the match records "nothing to match against" rather than silently passing —
// a false assurance is worse than an absent one.

import { useCallback, useEffect, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import {
  listOrders,
  listSuppliers,
  readOrder,
  recordBill,
  type Bill,
  type Order,
  type Supplier,
} from '../api/purchasing';
import { useAuth } from '../auth/session';
import { money } from '../ui/format';
import {
  Field,
  FormActions,
  FormError,
  SelectInput,
  TextInput,
  type FieldErrors,
} from '../ui/Form';
import { billableQty, orderTotals, type DraftLine } from './draft';
import { useT } from '../i18n/locale';

/** A bill line being typed. Carries the PO line it answers, when there is one:
 *  that link is what the quantity and price comparisons are made against. */
interface BillDraftLine extends DraftLine {
  poLineId?: string;
}

export function BillForm({
  companyId,
  onSaved,
  onCancel,
}: {
  companyId: string;
  onSaved: (bill: Bill) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [suppliers, setSuppliers] = useState<Supplier[] | null>(null);
  const [orders, setOrders] = useState<Order[]>([]);

  const [supplierId, setSupplierId] = useState('');
  const [poId, setPoId] = useState('');
  const [supplierRef, setSupplierRef] = useState('');
  const [billDate, setBillDate] = useState(today());
  const [lines, setLines] = useState<BillDraftLine[]>([]);

  const [fields, setFields] = useState<FieldErrors>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [loadingOrder, setLoadingOrder] = useState(false);

  useEffect(() => {
    let cancelled = false;
    Promise.all([listSuppliers(client, companyId), listOrders(client, companyId)])
      .then(([s, o]) => {
        if (cancelled) return;
        setSuppliers(s);
        // Only orders that could plausibly be invoiced. A draft has not been
        // sent to anybody, so nothing can have been billed against it.
        setOrders(
          o.filter((x) =>
            ['issued', 'receiving', 'received', 'closed'].includes(x.status),
          ),
        );
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setSuppliers([]);
        setFailure(
          err instanceof Offline
            ? 'This device cannot reach the server, so suppliers could not be loaded.'
            : 'Suppliers could not be loaded.',
        );
      });
    return () => {
      cancelled = true;
    };
  }, [client, companyId]);

  // Choosing an order fills the lines with what is still outstanding on it.
  const prefillFrom = useCallback(
    async (chosen: string) => {
      if (!chosen) {
        setLines([]);
        return;
      }
      setLoadingOrder(true);
      setFailure(null);
      try {
        const order = await readOrder(client, companyId, chosen);
        // The supplier follows the order rather than being chosen twice. A
        // bill against one supplier's order and another supplier's account is
        // not a thing that should be possible to type.
        setSupplierId(order.supplier_id);
        setLines(
          (order.lines ?? [])
            // Lines with nothing left to bill are dropped rather than listed
            // at zero: a row a buyer must not touch is noise on a form whose
            // whole job is comparing numbers.
            .filter((l) => Number(billableQty(l)) > 0)
            .map((l) => ({
              poLineId: l.id,
              variantId: l.variant_id,
              description: l.description,
              // What is left to bill, not what was ordered. Billing a
              // part-delivered order for the whole thing is the discrepancy
              // the match exists to catch, and pre-filling it would be the
              // form manufacturing one.
              qty: billableQty(l),
              unitCost: trim(l.unit_cost),
              taxRate: '0.15',
            })),
        );
      } catch (err) {
        setFailure(
          err instanceof Error ? err.message : 'That order could not be read.',
        );
      } finally {
        setLoadingOrder(false);
      }
    },
    [client, companyId],
  );

  const setLine = (i: number, patch: Partial<BillDraftLine>) =>
    setLines((prev) => prev.map((l, j) => (j === i ? { ...l, ...patch } : l)));

  const totals = orderTotals(lines);

  async function submit(e: React.FormEvent) {
    e.preventDefault();

    const local: FieldErrors = {};
    if (!supplierId) local.supplier_id = 'Choose whose invoice this is.';
    if (!supplierRef.trim()) {
      local.supplier_ref =
        "Enter the supplier's own invoice number, so the same document cannot be paid twice.";
    }
    const usable = lines.filter((l) => Number(l.qty) > 0);
    if (usable.length === 0) local.lines = 'A bill needs at least one line.';

    if (Object.keys(local).length > 0) {
      setFields(local);
      setFailure(null);
      return;
    }

    setBusy(true);
    setFields({});
    setFailure(null);
    try {
      onSaved(
        await recordBill(client, companyId, {
          supplier_id: supplierId,
          po_id: poId || undefined,
          supplier_ref: supplierRef.trim(),
          bill_date: billDate || undefined,
          lines: usable.map((l) => ({
            po_line_id: l.poLineId,
            description: l.description || 'Item',
            qty: l.qty,
            unit_cost: l.unitCost || '0',
            tax_rate: l.taxRate,
          })),
        }),
      );
    } catch (err) {
      if (err instanceof Offline) {
        setFailure(
          'This device cannot reach the server, so the bill was not recorded. ' +
            'Nothing has been lost.',
        );
      } else if (err instanceof RequestFailed) {
        if (err.fields) setFields(err.fields);
        setFailure(err.fields ? null : err.message);
      } else {
        setFailure(err instanceof Error ? err.message : 'That did not save.');
      }
    } finally {
      setBusy(false);
    }
  }

  if (suppliers === null) {
    return (
      <div className="ds-panel" aria-busy="true">
        <div className="ds-panel__body">
          <div className="ds-skeleton" style={{ blockSize: 240 }} />
        </div>
      </div>
    );
  }

  if (suppliers.length === 0) {
    return (
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">{t('purch.noSuppliers')}</p>
          <p className="ds-state__body">
            A bill is recorded against a supplier, and their payment terms set
            when it falls due. Add one first.
          </p>
          <button className="ds-btn ds-btn--secondary" onClick={onCancel}>
            {t('common.back')}
          </button>
        </div>
      </div>
    );
  }

  return (
    <form className="ds-panel form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('purch.recordSupplierBill')}</h2>
      </div>

      <div className="ds-panel__body form__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label={t('purch.againstWhichOrder')} htmlFor="bill-po"
            hint="Choosing one fills the lines in and lets the bill be checked against what arrived. Leave blank for rent, utilities and the like.">
            <SelectInput
              id="bill-po"
              value={poId}
              onChange={(v) => {
                setPoId(v);
                void prefillFrom(v);
              }}
              options={orders}
              label={(o) => `${o.po_number} — ${o.supplier}`}
              placeholder={t('purch.noOrderDirect')}
            />
          </Field>

          <Field label={t('common.supplier')} htmlFor="bill-supplier" required
            error={fields.supplier_id}>
            <SelectInput
              id="bill-supplier"
              value={supplierId}
              onChange={setSupplierId}
              options={suppliers}
              label={(s) => `${s.legal_name} (${s.code})`}
              placeholder={t('purch.chooseSupplier')}
              error={fields.supplier_id}
            />
          </Field>

          <Field label={t('purch.theirInvoiceNumber')} htmlFor="bill-ref" required
            error={fields.supplier_ref}
            hint="Exactly as it appears on their document. The same number cannot be entered twice for one supplier.">
            <TextInput id="bill-ref" value={supplierRef} onChange={setSupplierRef}
              placeholder="INV-10023" error={fields.supplier_ref} />
          </Field>

          <Field label={t('purch.invoiceDate')} htmlFor="bill-date" required
            error={fields.bill_date}
            hint="Their date, not today's. The due date follows from it and the supplier's terms.">
            <TextInput id="bill-date" value={billDate} onChange={setBillDate}
              type="date" error={fields.bill_date} />
          </Field>
        </div>

        <section className="form__lines" aria-label={t('purch.billedLines')}>
          <div className="form__lineshead">
            <h3 className="ds-h3">{t('purch.whatCharging')}</h3>
            {fields.lines && (
              <span className="field__error" role="alert">{fields.lines}</span>
            )}
          </div>

          {loadingOrder ? (
            <div className="ds-skeleton" style={{ blockSize: 120, inlineSize: '100%' }} />
          ) : lines.length === 0 ? (
            <p className="form__empty">
              {poId
                ? 'That order has nothing left to bill.'
                : 'Choose an order above to fill these in, or add a line by hand.'}
            </p>
          ) : (
            <div className="ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('common.item')}</th>
                    <th scope="col">{t('purch.quantityBilled')}</th>
                    <th scope="col">{t('purch.unitCostBilled')}</th>
                    <th scope="col" className="num">{t('common.lineTotal')}</th>
                    <th scope="col" />
                  </tr>
                </thead>
                <tbody>
                  {lines.map((line, i) => (
                    <tr key={i}>
                      <td>
                        <span className="detail__strong">
                          {line.description || 'Item'}
                        </span>
                        {!line.poLineId && (
                          <span className="ds-caption">{t('purch.notOnOrder')}</span>
                        )}
                      </td>
                      <td>
                        <input
                          className="input input--narrow"
                          inputMode="decimal"
                          value={line.qty}
                          aria-label={`Quantity billed on line ${i + 1}`}
                          onChange={(e) => setLine(i, { qty: e.target.value })}
                        />
                      </td>
                      <td>
                        <input
                          className="input input--narrow"
                          inputMode="decimal"
                          value={line.unitCost}
                          aria-label={`Unit cost billed on line ${i + 1}`}
                          onChange={(e) => setLine(i, { unitCost: e.target.value })}
                        />
                      </td>
                      <td className="num">{money(lineGross(line))}</td>
                      <td>
                        <button
                          type="button"
                          className="ds-btn ds-btn--quiet"
                          onClick={() => setLines((p) => p.filter((_, j) => j !== i))}
                          aria-label={`Remove line ${i + 1}`}
                        >
                          {t('common.remove')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr>
                    <td colSpan={3}>{t('common.net')}</td>
                    <td className="num">{money(totals.net)}</td>
                    <td />
                  </tr>
                  <tr>
                    <td colSpan={3}>{t('common.vat')}</td>
                    <td className="num">{money(totals.tax)}</td>
                    <td />
                  </tr>
                  <tr>
                    <td colSpan={3}>{t('purch.invoiceTotal')}</td>
                    <td className="num">{money(totals.gross)}</td>
                    <td />
                  </tr>
                </tfoot>
              </table>
            </div>
          )}

          <button
            type="button"
            className="ds-btn ds-btn--secondary"
            onClick={() =>
              setLines((p) => [
                ...p,
                { variantId: '', description: '', qty: '1', unitCost: '', taxRate: '0.15' },
              ])
            }
          >
            {t('purch.addLine')}
          </button>

          <p className="ds-caption form__aside">
            Enter what the supplier has actually charged, even where it differs
            from the order. The difference is what the check is for, and a bill
            that does not agree is held rather than refused.
          </p>
        </section>

        <FormActions submitLabel="Record bill" busy={busy} onCancel={onCancel} />
      </div>
    </form>
  );
}

function lineGross(line: BillDraftLine): string {
  return orderTotals([line]).gross;
}

function trim(raw: string): string {
  if (!raw.includes('.')) return raw;
  const t = raw.replace(/0+$/, '').replace(/\.$/, '');
  return t === '' || t === '-' ? '0' : t;
}

function today(): string {
  return new Date().toISOString().slice(0, 10);
}
