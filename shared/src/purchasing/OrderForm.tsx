// Raising a purchase order.
//
// The one screen in the module a buyer spends real time on, so the whole design
// is about the line table: adding a line, correcting a quantity, seeing the
// running total change. Everything above it — supplier, destination, expected
// date — is set once and then ignored.
//
// # It saves as a draft, and issuing is a separate press
//
// A draft commits nothing and can be corrected freely; issuing is what the
// supplier can hold the shop to, and it happens from the order screen with the
// finished order in front of you rather than as a side effect of pressing Save
// on a form. That split exists in the API for the same reason and carries its
// own permission.
//
// # The total shown here is arithmetic for the reader, not for the books
//
// The server recomputes every figure from the lines and is the authority — a
// client that could state its own total could authorise an amount different
// from what its lines add up to. This preview exists so a buyer knows roughly
// what they are committing to before they press Save, and it is computed in
// minor units rather than through a float, because a preview that disagreed
// with the saved order by a hallala would be worse than no preview.

import { useCallback, useEffect, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import {
  createOrder,
  listSuppliers,
  updateOrder,
  type Order,
  type Supplier,
  type Warehouse,
  listWarehouses,
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
import { VariantPicker } from './VariantPicker';
import { lineTotals, orderTotals, type DraftLine } from './draft';
import { useT } from '../i18n/locale';

export function OrderForm({
  companyId,
  existing,
  onSaved,
  onCancel,
}: {
  companyId: string;
  /** An existing DRAFT being corrected. Absent when raising a new one. */
  existing?: Order;
  onSaved: (order: Order) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [suppliers, setSuppliers] = useState<Supplier[] | null>(null);
  const [warehouses, setWarehouses] = useState<Warehouse[] | null>(null);

  const [supplierId, setSupplierId] = useState(existing?.supplier_id ?? '');
  const [warehouseId, setWarehouseId] = useState(existing?.warehouse_id ?? '');
  const [expectedOn, setExpectedOn] = useState(existing?.expected_on ?? '');
  const [notes, setNotes] = useState(existing?.notes ?? '');

  const [lines, setLines] = useState<DraftLine[]>(() =>
    (existing?.lines ?? []).map((l) => ({
      variantId: l.variant_id,
      description: l.description,
      qty: trim(l.qty_ordered),
      unitCost: trim(l.unit_cost),
      taxRate: '0.15',
    })),
  );

  const [fields, setFields] = useState<FieldErrors>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Both lists are needed before the form is usable, so they load together and
  // the form shows a skeleton until they arrive rather than rendering two empty
  // dropdowns a buyer would try to use.
  useEffect(() => {
    let cancelled = false;
    Promise.all([
      listSuppliers(client, companyId),
      listWarehouses(client, companyId),
    ])
      .then(([s, w]) => {
        if (cancelled) return;
        setSuppliers(s);
        setWarehouses(w);
        // A single warehouse is not a choice, so it is made for them.
        if (w.length === 1 && !existing) setWarehouseId(w[0]!.id);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setSuppliers([]);
        setWarehouses([]);
        setFailure(
          err instanceof Offline
            ? 'This device cannot reach the server, so suppliers and warehouses could not be loaded.'
            : 'Suppliers and warehouses could not be loaded.',
        );
      });
    return () => {
      cancelled = true;
    };
  }, [client, companyId, existing]);

  const setLine = useCallback((i: number, patch: Partial<DraftLine>) => {
    setLines((prev) => prev.map((l, j) => (j === i ? { ...l, ...patch } : l)));
  }, []);

  const totals = orderTotals(lines);
  const currency = existing?.currency;

  async function submit(e: React.FormEvent) {
    e.preventDefault();

    const local: FieldErrors = {};
    if (!supplierId) local.supplier_id = 'Choose who you are buying from.';
    if (!warehouseId) local.warehouse_id = 'Choose where the goods will be delivered.';
    const usable = lines.filter((l) => l.variantId && Number(l.qty) > 0);
    if (usable.length === 0) {
      local.lines = 'Add at least one item with a quantity.';
    }
    if (Object.keys(local).length > 0) {
      setFields(local);
      setFailure(null);
      return;
    }

    setBusy(true);
    setFields({});
    setFailure(null);

    const body = {
      supplier_id: supplierId,
      warehouse_id: warehouseId,
      expected_on: expectedOn || undefined,
      notes: notes.trim() || undefined,
      lines: usable.map((l) => ({
        variant_id: l.variantId,
        description: l.description,
        qty: l.qty,
        unit_cost: l.unitCost || '0',
        tax_rate: l.taxRate,
      })),
    };

    try {
      onSaved(
        existing
          ? await updateOrder(client, companyId, existing.id, body)
          : await createOrder(client, companyId, body),
      );
    } catch (err) {
      if (err instanceof Offline) {
        setFailure(
          'This device cannot reach the server, so the order was not saved. ' +
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

  if (suppliers === null || warehouses === null) {
    return (
      <div className="ds-panel" aria-busy="true">
        <div className="ds-panel__body">
          <div className="ds-skeleton" style={{ blockSize: 240 }} />
        </div>
      </div>
    );
  }

  if (suppliers.length === 0) {
    // Ordering from nobody is not possible, so the form says what to do first
    // rather than presenting an empty dropdown.
    return (
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">{t('purch.noSuppliers')}</p>
          <p className="ds-state__body">
            An order is raised against a supplier. Add one first, and their
            payment terms will set when the bill falls due.
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
        <h2 className="ds-h3">
          {existing ? `Correcting ${existing.po_number}` : 'New purchase order'}
        </h2>
        {existing && <span className="ds-caption">{t('purch.draftNotCommitted')}</span>}
      </div>

      <div className="ds-panel__body form__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label={t('common.supplier')} htmlFor="po-supplier" required error={fields.supplier_id}>
            <SelectInput
              id="po-supplier"
              value={supplierId}
              onChange={setSupplierId}
              options={suppliers}
              label={(s) => `${s.legal_name} (${s.code})`}
              placeholder={t('purch.chooseSupplier')}
              error={fields.supplier_id}
            />
          </Field>

          <Field label={t('purch.deliverTo')} htmlFor="po-warehouse" required
            error={fields.warehouse_id}
            hint="Stock arrives here when the delivery is recorded.">
            <SelectInput
              id="po-warehouse"
              value={warehouseId}
              onChange={setWarehouseId}
              options={warehouses}
              label={(w) => (w.store ? `${w.name} — ${w.store}` : w.name)}
              placeholder={t('purch.chooseWarehouse')}
              error={fields.warehouse_id}
            />
          </Field>

          <Field label={t('common.expected')} htmlFor="po-expected" error={fields.expected_on}
            hint="When the supplier says it will arrive.">
            <TextInput id="po-expected" value={expectedOn} onChange={setExpectedOn}
              type="date" error={fields.expected_on} />
          </Field>

          <Field label={t('common.notes')} htmlFor="po-notes">
            <TextInput id="po-notes" value={notes} onChange={setNotes}
              placeholder={t('purch.supplierShouldKnow')} />
          </Field>
        </div>

        <section className="form__lines" aria-label={t('purch.items')}>
          <div className="form__lineshead">
            <h3 className="ds-h3">{t('purch.items')}</h3>
            {fields.lines && (
              <span className="field__error" role="alert">{fields.lines}</span>
            )}
          </div>

          <div className="ds-scroll-x">
            <table className="ds-table">
              <thead>
                <tr>
                  <th scope="col">{t('common.item')}</th>
                  <th scope="col">{t('common.quantity')}</th>
                  <th scope="col">{t('common.unitCost')}</th>
                  <th scope="col" className="num">{t('common.lineTotal')}</th>
                  <th scope="col" />
                </tr>
              </thead>
              <tbody>
                {lines.map((line, i) => (
                  <tr key={i}>
                    <td>
                      <VariantPicker
                        companyId={companyId}
                        value={line.variantId}
                        description={line.description}
                        onPick={(v) =>
                          setLine(i, { variantId: v.id, description: v.description })
                        }
                      />
                    </td>
                    <td>
                      <input
                        className="input input--narrow"
                        inputMode="decimal"
                        value={line.qty}
                        aria-label={`Quantity for line ${i + 1}`}
                        onChange={(e) => setLine(i, { qty: e.target.value })}
                      />
                    </td>
                    <td>
                      <input
                        className="input input--narrow"
                        inputMode="decimal"
                        value={line.unitCost}
                        aria-label={`Unit cost for line ${i + 1}`}
                        onChange={(e) => setLine(i, { unitCost: e.target.value })}
                      />
                    </td>
                    <td className="num">{money(lineTotals(line).gross, { currency })}</td>
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

                {lines.length === 0 && (
                  <tr>
                    <td colSpan={5} className="form__empty">
                      {t('purch.noItemsYet')}
                    </td>
                  </tr>
                )}
              </tbody>

              {lines.length > 0 && (
                <tfoot>
                  <tr>
                    <td colSpan={3}>{t('common.net')}</td>
                    <td className="num">{money(totals.net, { currency })}</td>
                    <td />
                  </tr>
                  <tr>
                    <td colSpan={3}>{t('common.vat')}</td>
                    <td className="num">{money(totals.tax, { currency })}</td>
                    <td />
                  </tr>
                  <tr>
                    <td colSpan={3}>{t('purch.orderTotal')}</td>
                    <td className="num">{money(totals.gross, { currency })}</td>
                    <td />
                  </tr>
                </tfoot>
              )}
            </table>
          </div>

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
            {t('purch.addItem')}
          </button>

          <p className="ds-caption form__aside">
            The figures above are a guide. The server prices the order from these
            lines when it is saved and is the authority on the total.
          </p>
        </section>

        <FormActions
          submitLabel={existing ? 'Save changes' : 'Save as draft'}
          busy={busy}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

/** Trims the scale a numeric column returns, so an editable field shows "10"
 *  rather than "10.0000". */
function trim(raw: string): string {
  if (!raw.includes('.')) return raw;
  const t = raw.replace(/0+$/, '').replace(/\.$/, '');
  return t === '' || t === '-' ? '0' : t;
}
