// The VAT return, prepared (blueprint E2.3).
//
// # It says, on its face, that it is not a filing
//
// The route is `reports/vat-return` and the payload carries `filed: false`
// permanently. The official form layout is a regulatory value nobody has
// verified, so nothing here is mapped onto numbered boxes — and a screen that
// looked like the form would invite somebody to copy figures into it.
//
// So the heading says "prepared, not filed", and the caveats the server
// attaches travel with the figures rather than being footnotes.
//
// # The reconciliation is the point
//
// A return is trustworthy only if the tax it claims to have charged agrees with
// the Output VAT account, and the tax it claims to have paid agrees with the
// supplier bills. The server works both out independently and reports whether
// they agree. A screen that showed only the totals would be hiding the one
// check that makes them worth anything.

import { useCallback } from 'react';

import { useAuth } from '../auth/session';
import { RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field } from '../ui/Form';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { money } from '../ui/format';
import type { Client } from '../api/client';
import type { Range } from './accounting';

/** A prepared return. Typed here rather than in `api/accounting.ts` because it
 *  belongs to the tax module, and this is the only screen that reads one. */
interface TreatmentLine {
  treatment: string;
  net_amount: string;
  tax_amount: string;
  invoice_count: number;
}

interface VATReturn {
  country: string;
  from: string;
  to: string;
  base_currency: string;
  /** `vat` or `sales_tax`. Not cosmetic: a sales tax has no input side at all,
   *  so a US company's net payable is simply what it collected. */
  model: string;
  supplies: TreatmentLine[];
  total_net: string;
  output_tax_total: string;
  input_tax_total: string | null;
  billed_input_tax: string | null;
  input_difference: string | null;
  net_payable: string | null;
  ledger_output_tax: string;
  difference: string;
  reconciled: boolean;
  /** What the return does NOT include, and why. A figure a business would act
   *  on has to carry its own caveats. */
  outstanding: string[];
  /** Always false. Stated so nobody mistakes a preparation for a submission. */
  filed: boolean;
}

function vatReturn(
  client: Client,
  companyId: string,
  from: string,
  to: string,
): Promise<VATReturn> {
  return client.send<VATReturn>(
    'GET',
    `/api/v1/reports/vat-return?company_id=${companyId}&from=${from}&to=${to}`,
  );
}

export function VATReturnPanel({
  companyId,
  period,
  onPeriod,
}: {
  companyId: string;
  period: Range;
  onPeriod: (r: Range) => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const load = useCallback(
    () => vatReturn(client, companyId, period.from, period.to),
    [client, companyId, period.from, period.to],
  );
  const { remote, reload } = useRemote(load);

  return (
    <>
      <section className="ds-panel acct__controls">
        <div className="ds-panel__body acct__controlrow">
          <Field label={t('common.from')} htmlFor="vat-from" required>
            <input
              id="vat-from"
              type="date"
              className="field__input"
              value={period.from}
              max={period.to}
              onChange={(e) => onPeriod({ ...period, from: e.target.value })}
            />
          </Field>
          <Field label={t('common.to')} htmlFor="vat-to" required>
            <input
              id="vat-to"
              type="date"
              className="field__input"
              value={period.to}
              min={period.from}
              onChange={(e) => onPeriod({ ...period, to: e.target.value })}
            />
          </Field>
        </div>
      </section>

      <section className="ds-panel" aria-label={t('acct.vatReturn')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('acct.vatReturn')}</h2>
          {/* The most important sentence on the screen. */}
          <p className="ds-caption">{t('vat.preparedNotFiled')}</p>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(r: VATReturn) => (
            <>
              <div className="ds-panel__body">
                <p
                  className={
                    r.reconciled ? 'acct__reconciled' : 'acct__outofbalance'
                  }
                  role={r.reconciled ? undefined : 'alert'}
                >
                  {r.reconciled
                    ? t('vat.reconciles')
                    : t('vat.doesNotReconcile', {
                        amount: money(r.difference, {
                          currency: r.base_currency,
                        }),
                      })}
                </p>

                {r.outstanding.length > 0 && (
                  <ul className="acct__caveats">
                    {r.outstanding.map((note) => (
                      // The server's own sentences, which are already written
                      // for a reader. They are not catalogue keys and cannot
                      // be: what a return omits depends on what happened.
                      <li key={note}>{note}</li>
                    ))}
                  </ul>
                )}
              </div>

              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('vat.treatment')}</th>
                      <th scope="col" className="num">
                        {t('vat.invoices')}
                      </th>
                      <th scope="col" className="num">
                        {t('vat.net')}
                      </th>
                      <th scope="col" className="num">
                        {t('vat.tax')}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {r.supplies.map((line) => (
                      <tr key={line.treatment}>
                        <td>{treatmentLabel(line.treatment, t)}</td>
                        <td className="num">{line.invoice_count}</td>
                        <td className="num">
                          {money(line.net_amount, { currency: r.base_currency })}
                        </td>
                        <td className="num">
                          {money(line.tax_amount, { currency: r.base_currency })}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                  <tfoot>
                    <tr>
                      <th scope="row">{t('vat.outputTax')}</th>
                      <td />
                      <td className="num">
                        {money(r.total_net, { currency: r.base_currency })}
                      </td>
                      <td className="num">
                        {money(r.output_tax_total, { currency: r.base_currency })}
                      </td>
                    </tr>
                    {/* A sales tax has no input side, so the row is absent
                        rather than zero. Zero would be a claim that nothing was
                        reclaimable; absence is the truth, which is that there
                        is no such mechanism. */}
                    {r.input_tax_total !== null && (
                      <tr>
                        <th scope="row">{t('vat.inputTax')}</th>
                        <td />
                        <td />
                        <td className="num">
                          {money(r.input_tax_total, {
                            currency: r.base_currency,
                          })}
                        </td>
                      </tr>
                    )}
                    {r.net_payable !== null && (
                      <tr className="acct__total acct__total--strong">
                        <th scope="row">{t('vat.netPayable')}</th>
                        <td />
                        <td />
                        <td className="num">
                          {money(r.net_payable, { currency: r.base_currency })}
                        </td>
                      </tr>
                    )}
                  </tfoot>
                </table>
              </div>
            </>
          )}
        </RemoteBody>
      </section>
    </>
  );
}

/** A tax treatment in words, falling back to its own name — a treatment the
 *  registry allows and the catalogue has not been told about should read as
 *  itself rather than vanish from a tax return. */
function treatmentLabel(treatment: string, t: (k: Key) => string): string {
  const key = `vat.treatment.${treatment}` as Key;
  const named = t(key);
  return named === key ? treatment.replace(/_/g, ' ') : named;
}
