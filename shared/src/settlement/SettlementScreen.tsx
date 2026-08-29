// Card settlement (P15, blueprint C12).
//
// The screen answers one question an owner asks out loud: "the bank paid me
// 985 today — what was that for, and where did the other 15 go?" So it leads
// with what is outstanding, grouped by payment method, because that is how it
// arrives: Mada and an international card settle on different days in different
// batches, and a list ordered only by date makes somebody pick them apart by
// eye, which is where a payment joins the wrong deposit.
//
// The fee is never typed. It is what is left after the deposit is subtracted
// from what was ticked, computed here so it can be read back before anything is
// recorded, and computed again by the server, which is the authority.
//
// Everything reuses what exists — the DetailScreen frame, useRemote and its
// states, the design system's tables, badges and form kit. The arrangement is
// the E-invoicing screen's.

import { useCallback, useMemo, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { money, shortDate, tenderName } from '../ui/format';
import {
  listPendingSettlement,
  recordSettlement,
  type PendingTender,
} from '../api/settlement';
import {
  byMethod,
  canRecord,
  checkDeposit,
  outstandingTotal,
} from './settlement';
import { useLocale, useT } from '../i18n/locale';

export function SettlementScreen({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const mayRecord = can('accounting.create');

  const load = useCallback(
    () => listPendingSettlement(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('settlement.title')}</h1>
          <p className="ds-caption">{t('settle.intro')}</p>
        </div>
      </header>

      <RemoteBody remote={remote} onRetry={reload}>
        {(pending: PendingTender[]) =>
          pending.length === 0 ? (
            <div className="ds-panel">
              <div className="ds-panel__body">
                <EmptyState
                  title={t('settle.allSettledTitle')}
                  body={t('settle.allSettledBody')}
                />
              </div>
            </div>
          ) : (
            <Outstanding
              companyId={companyId}
              pending={pending}
              mayRecord={mayRecord}
              onRecorded={reload}
            />
          )
        }
      </RemoteBody>
    </main>
  );
}

function Outstanding({
  companyId,
  pending,
  mayRecord,
  onRecorded,
}: {
  companyId: string;
  pending: PendingTender[];
  mayRecord: boolean;
  onRecorded: () => void;
}) {
  const t = useT();
  const { locale } = useLocale();
  const { client } = useAuth();

  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [reference, setReference] = useState('');
  const [depositedOn, setDepositedOn] = useState(today());
  const [netAmount, setNetAmount] = useState('');
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);

  const groups = useMemo(() => byMethod(pending), [pending]);
  const check = checkDeposit(pending, selected, netAmount, t);

  const toggle = (id: string) => {
    setSelected((held) => {
      const next = new Set(held);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  // Ticking a whole method at once, because that is how a deposit usually
  // arrives: one acquirer paying out one day's takings.
  const toggleGroup = (tenders: PendingTender[]) => {
    const all = tenders.every((t) => selected.has(t.tender_id));
    setSelected((held) => {
      const next = new Set(held);
      for (const t of tenders) {
        if (all) next.delete(t.tender_id);
        else next.add(t.tender_id);
      }
      return next;
    });
  };

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!canRecord(check) || busy) return;

    setBusy(true);
    setFailed(null);
    try {
      await recordSettlement(client, companyId, {
        // Assigned here, before the request, so a retry after a lost response
        // returns the deposit already recorded rather than clearing the same
        // payments a second time.
        uuid: crypto.randomUUID(),
        reference: reference.trim(),
        deposited_on: depositedOn,
        net_amount: netAmount.trim(),
        tender_ids: [...selected],
      });
      setSelected(new Set());
      setReference('');
      setNetAmount('');
      onRecorded();
    } catch (err) {
      setFailed(err instanceof Error ? err.message : t('settle.depositFailed'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="ds-panel">
        <div className="ds-panel__body">
          <p className="ds-body-sm">
            <span className="settle__outstanding num">
              {money(outstandingTotal(pending))}
            </span>{' '}
            {t('settle.notYetDeposited')}
          </p>
        </div>

        <div className="ds-panel__body ds-scroll-x">
          <table className="ds-table">
            <thead>
              <tr>
                {mayRecord && <th scope="col" />}
                <th scope="col">{t('common.invoice')}</th>
                <th scope="col">{t('settle.taken')}</th>
                <th scope="col" className="num">
                  {t('common.amount')}
                </th>
              </tr>
            </thead>
            {groups.map((group) => (
              <tbody key={group.method}>
                <tr>
                  <th
                    scope="colgroup"
                    colSpan={mayRecord ? 3 : 2}
                    className="detail__strong"
                  >
                    {mayRecord && (
                      <label className="ds-check">
                        <input
                          type="checkbox"
                          aria-label={t('settle.selectEvery', {
                            method: tenderName(group.method, t),
                          })}
                          checked={group.tenders.every((one) =>
                            selected.has(one.tender_id),
                          )}
                          onChange={() => toggleGroup(group.tenders)}
                        />
                      </label>
                    )}{' '}
                    {tenderName(group.method, t)}
                    <span className="ds-caption">
                      {group.count === 1
                        ? t('settle.onePayment')
                        : t('settle.nPayments', { count: group.count })}
                    </span>
                  </th>
                  <td className="num detail__strong">{money(group.total)}</td>
                </tr>

                {/* `tender`, not `t`. The row used to bind `t`, which shadowed
                    the translate function — so the only way to write a label
                    here was an English template literal, and that is what was
                    announced to an Arabic screen reader. */}
                {group.tenders.map((tender) => (
                  <tr key={tender.tender_id}>
                    {mayRecord && (
                      <td>
                        <label className="ds-check">
                          <input
                            type="checkbox"
                            aria-label={t('settle.includeOne', {
                              invoice:
                                tender.invoice_number ||
                                tender.invoice_id.slice(0, 8),
                            })}
                            checked={selected.has(tender.tender_id)}
                            onChange={() => toggle(tender.tender_id)}
                          />
                        </label>
                      </td>
                    )}
                    <td>
                      {tender.invoice_number || tender.invoice_id.slice(0, 8)}
                    </td>
                    <td>{shortDate(tender.issued_at, locale)}</td>
                    <td className="num">{money(tender.amount)}</td>
                  </tr>
                ))}
              </tbody>
            ))}
          </table>
        </div>
      </div>

      {mayRecord && (
        <form className="ds-panel" onSubmit={submit}>
          <div className="ds-panel__body">
            <h2 className="ds-h2">{t('settle.recordTitle')}</h2>
            <p className="ds-caption">{t('settle.recordBlurb')}</p>

            <Field
              label={t('settle.reference')}
              htmlFor="settlement-reference"
              required
              hint={t('settle.referenceHint')}
            >
              <TextInput
                id="settlement-reference"
                value={reference}
                onChange={setReference}
                placeholder="MADA-20260817-001"
              />
            </Field>

            <Field
              label={t('settle.landedOn')}
              htmlFor="settlement-date"
              required
              hint={t('settle.landedOnHint')}
            >
              <TextInput
                id="settlement-date"
                type="date"
                value={depositedOn}
                onChange={setDepositedOn}
              />
            </Field>

            <Field
              label={t('settle.amountDeposited')}
              htmlFor="settlement-net"
              required
            >
              <TextInput
                id="settlement-net"
                value={netAmount}
                onChange={setNetAmount}
                inputMode="decimal"
                placeholder="985.00"
              />
            </Field>

            {/* What is about to be recorded, in words, before it is. The tone
                carries the difference between "this adds up" and "check the
                statement" — colour is never the only signal, so the sentence
                says it too. */}
            <p
              className={
                check.kind === 'exceeds'
                  ? 'field__error'
                  : check.kind === 'no_fee'
                    ? 'ds-caption detail__strong'
                    : 'ds-caption'
              }
              role="status"
            >
              {check.message}
            </p>

            <FormError message={failed} />
            <FormActions
              submitLabel={t('settle.record')}
              busy={busy}
              disabled={!canRecord(check)}
              onCancel={() => {
                setSelected(new Set());
                setReference('');
                setNetAmount('');
                setFailed(null);
              }}
            />
          </div>
        </form>
      )}
    </>
  );
}

/** Today, as the date input wants it. Most deposits are keyed the day they
 *  land, and a date that has to be typed every time is a date that gets typed
 *  wrong. */
function today(): string {
  return new Date().toISOString().slice(0, 10);
}
