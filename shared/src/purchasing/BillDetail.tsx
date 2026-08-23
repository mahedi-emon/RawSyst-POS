// One supplier bill, and the evidence of its three-way match.
//
// B5.2 calls the match "the single most effective control against supplier
// overbilling and internal fraud", and a control is only as good as the account
// it gives of itself. So this screen shows every comparison the server made —
// including the ones that passed — rather than a bare verdict.
//
// # A held bill is not an error
//
// It is the control working. The screen says what was found, in the words a
// buyer would use to a supplier on the phone, and offers the one legitimate way
// forward: somebody with the authority accepts the discrepancy, in writing,
// against their name.

import { useCallback, useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { useAuth } from '../auth/session';
import { DetailScreen, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { money } from '../ui/format';
import { trimQuantity } from '../dashboard/drilldown';
import {
  approveBill,
  paySupplier,
  readBill,
  type Bill,
  type MatchLine,
} from '../api/purchasing';
import { BillStatus } from './PurchasingScreen';
import { payable } from './purchasing';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';

export function BillDetail({
  companyId,
  billId,
  onBack,
}: {
  companyId: string;
  billId: string;
  onBack: () => void;
}) {
  const t = useT();
  const { client, can } = useAuth();
  const load = useCallback(
    () => readBill(client, companyId, billId),
    [client, companyId, billId],
  );
  const { remote, reload, refreshing } = useRemote(load);

  const [reason, setReason] = useState('');
  const [payAmount, setPayAmount] = useState('');
  const [method, setMethod] = useState('bank');
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const mayApprove = can('purchasing.approve_bill');
  const mayPay = can('purchasing.pay_supplier');

  async function approve(bill: Bill) {
    if (!reason.trim()) {
      setNotice(t('bill.reasonPrompt'));
      return;
    }
    setBusy(true);
    setNotice(null);
    try {
      await approveBill(client, companyId, bill.id, reason.trim());
      setReason('');
      setNotice(t('bill.accepted'));
      reload();
    } catch (err) {
      setNotice(explain(err, t('bill.approveFailed'), t));
    } finally {
      setBusy(false);
    }
  }

  async function pay(bill: Bill) {
    const amount = payAmount.trim() || bill.outstanding;
    setBusy(true);
    setNotice(null);
    try {
      const payment = await paySupplier(client, companyId, {
        supplier_id: bill.supplier_id,
        method,
        allocations: [{ bill_id: bill.id, amount }],
      });
      setPayAmount('');
      setNotice(
        payment.already_paid
          ? `That payment was already recorded as ${payment.payment_number}.`
          : `Paid. Recorded as ${payment.payment_number}.`,
      );
      reload();
    } catch (err) {
      setNotice(explain(err, t('bill.paymentFailed'), t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(bill) => (
        <DetailScreen
          title={bill.supplier_ref}
          subtitle={`${bill.supplier} · due ${bill.due_date}`}
          backLabel="Bills"
          onBack={onBack}
          onRefresh={reload}
          refreshing={refreshing}
          actions={<BillStatus bill={bill} />}
        >
          {notice && (
            <p className="ds-panel purchase__notice" role="status" aria-live="polite">
              {notice}
            </p>
          )}

          {bill.status === 'blocked' && <HeldNotice bill={bill} />}

          <section className="detail__summary" aria-label={t('purch.billTotals')}>
            <Figure label={t('common.net')} value={money(bill.subtotal_net, { currency: bill.currency })} />
            <Figure label={t('common.vat')} value={money(bill.tax_total, { currency: bill.currency })} />
            <Figure label={t('common.total')} value={money(bill.total_inclusive, { currency: bill.currency })} strong />
            <Figure label={t('common.outstanding')} value={money(bill.outstanding, { currency: bill.currency })} strong />
          </section>

          <MatchEvidence lines={bill.match ?? []} />

          {bill.status === 'blocked' && mayApprove && (
            <section className="ds-panel">
              <div className="ds-panel__head">
                <h2 className="ds-h3">{t('purch.acceptDiscrepancy')}</h2>
              </div>
              <div className="ds-panel__body purchase__form">
                <label className="purchase__field">
                  <span className="ds-caption">
                    {t('purch.whyAccepting')}
                  </span>
                  <input
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                    placeholder={t('purch.discrepancyExample')}
                  />
                </label>
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy || !reason.trim()}
                  onClick={() => void approve(bill)}
                >
                  {busy ? t('bill.accepting') : t('bill.acceptAndPost')}
                </button>
              </div>
            </section>
          )}

          {bill.status === 'blocked' && !mayApprove && (
            <p className="ds-panel purchase__hint">{t('bill.heldNoRights')}</p>
          )}

          {mayPay && payable(bill) && (
            <section className="ds-panel">
              <div className="ds-panel__head">
                <h2 className="ds-h3">{t('purch.payThisBill')}</h2>
              </div>
              <div className="ds-panel__body purchase__form">
                <label className="purchase__field">
                  <span className="ds-caption">
                    {t('purch.amountOrBlank')}
                  </span>
                  <input
                    inputMode="decimal"
                    value={payAmount}
                    onChange={(e) => setPayAmount(e.target.value)}
                    placeholder={bill.outstanding}
                  />
                </label>
                <label className="purchase__field">
                  <span className="ds-caption">{t('common.paidFrom')}</span>
                  <select value={method} onChange={(e) => setMethod(e.target.value)}>
                    <option value="bank">{t('common.bank')}</option>
                    <option value="cash">{t('common.cash')}</option>
                  </select>
                </label>
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy}
                  onClick={() => void pay(bill)}
                >
                  {busy ? t('bill.paying') : `Pay ${money(payAmount || bill.outstanding)}`}
                </button>
              </div>
            </section>
          )}
        </DetailScreen>
      )}
    </RemoteBody>
  );
}

/** Why the money is held, in the words a buyer would use on the phone.
 *
 * Informational rather than alarming: the control working correctly is not an
 * error, and styling it as one would teach buyers to click past it. */
function HeldNotice({ bill }: { bill: Bill }) {
  const t = useT();
  const breaches = (bill.match ?? []).filter((m) => m.outcome === 'breach');
  return (
    <section className="ds-panel gate" aria-label={t('purch.whyHeld')}>
      <div className="ds-panel__body">
        <h2 className="ds-h3">{t('purch.billHeld')}</h2>
        <p className="gate__body">
          It does not agree with {bill.po_number || 'the order'} and what
          actually arrived. The bill is recorded, but it has deliberately not
          been posted to your accounts and no money can leave against it until
          somebody accepts the difference.
        </p>
        {breaches.length > 0 && (
          <ul className="purchase__breaches">
            {breaches.map((m, i) => (
              <li key={i}>{m.detail || `${m.dimension} does not agree.`}</li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

/** Every comparison the server made, passes included.
 *
 * The passing rows are the point as much as the failing ones: "we checked and
 * it was fine" is the evidence an auditor asks for, and a screen showing only
 * problems cannot demonstrate that a check happened at all. */
function MatchEvidence({ lines }: { lines: MatchLine[] }) {
  const t = useT();
  if (lines.length === 0) return null;

  return (
    <section className="ds-panel" aria-label={t('purch.threeWayMatch')}>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('purch.matchExplain')}</h2>
        <span className="ds-caption">{lines.length} comparison{lines.length === 1 ? '' : 's'}</span>
      </div>
      <div className="ds-panel__body ds-scroll-x">
        <table className="ds-table">
          <thead>
            <tr>
              <th scope="col">{t('common.item')}</th>
              <th scope="col">{t('purch.checked')}</th>
              <th scope="col" className="num">{t('common.ordered')}</th>
              <th scope="col" className="num">{t('common.arrived')}</th>
              <th scope="col" className="num">{t('common.billed')}</th>
              <th scope="col">{t('purch.result')}</th>
            </tr>
          </thead>
          <tbody>
            {lines.map((m, i) => (
              <tr key={i}>
                <td>{m.description || <span className="ds-subtle">{t('purch.wholeBill')}</span>}</td>
                <td>{m.dimension === 'qty' ? 'Quantity' : m.dimension === 'price' ? t('common.unitPrice') : m.dimension}</td>
                <td className="num">{m.ordered ? trimQuantity(m.ordered) : '—'}</td>
                <td className="num">{m.received ? trimQuantity(m.received) : '—'}</td>
                <td className="num">{m.billed ? trimQuantity(m.billed) : '—'}</td>
                <td>
                  <Outcome line={m} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Outcome({ line }: { line: MatchLine }) {
  const t = useT();
  const tone =
    line.outcome === 'breach'
      ? 'ds-badge--danger'
      : line.outcome === 'within_tolerance'
        ? 'ds-badge--warning'
        : 'ds-badge--success';
  const label =
    line.outcome === 'breach'
      ? t('bill.doesNotAgree')
      : line.outcome === 'within_tolerance'
        ? t('bill.withinTolerance')
        : t('bill.agrees');
  return (
    <span className={`ds-badge ${tone}`} title={line.detail}>
      {label}
    </span>
  );
}

function Figure({
  label,
  value,
  strong,
}: {
  label: string;
  value: string;
  strong?: boolean;
}) {
  return (
    <div className={`figure${strong ? ' figure--strong' : ''}`}>
      <span className="ds-caption">{label}</span>
      <span className="figure__value num">{value}</span>
    </div>
  );
}

function explain(err: unknown, fallback: string, t: (key: Key) => string): string {
  if (err instanceof Offline) {
    return t('bill.offlineNoChange');
  }
  if (err instanceof RequestFailed) {
    if (err.status === 403) return t('common.noPermission');
    return err.message;
  }
  return err instanceof Error ? err.message : fallback;
}
