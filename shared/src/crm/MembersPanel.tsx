// The membership list, and one member's card (blueprint B16).
//
// # Ordered by what they have spent, not by what they hold
//
// An owner opening this screen is looking for their best customers. Points are
// what those customers have NOT spent yet, and ranking on them would put the
// person who never redeems above the person who comes in every week.
//
// # The segment is on the row
//
// B16 asks for New, Returning, VIP, At-risk. Each is derived from the invoices,
// and putting it beside the name is the whole value: a list of customers is a
// list, and a list that says which of them have stopped coming is a job.

import { useCallback, useState } from 'react';

import {
  adjustPoints,
  expirePoints,
  giveCredit,
  listMembers,
  readCard,
  readWallet,
  type LoyaltyCard,
  type Wallet,
} from '../api/crm';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { money, shortDate } from '../ui/format';

export function MembersPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('loyalty.manage');
  const [open, setOpen] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [expired, setExpired] = useState<number | null>(null);

  const load = useCallback(
    () => listMembers(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  if (open) {
    return (
      <MemberCard
        companyId={companyId}
        customerId={open}
        onBack={() => {
          setOpen(null);
          reload();
        }}
      />
    );
  }

  async function sweep() {
    setBusy(true);
    setFailure(null);
    try {
      const done = await expirePoints(client, companyId);
      setExpired(done.points_expired);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('crm.members')}>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('crm.members')}</h2>
        {mayManage && (
          <button
            className="ds-btn ds-btn--quiet"
            disabled={busy}
            onClick={() => void sweep()}
          >
            {t('crm.expirePoints')}
          </button>
        )}
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />
        {expired !== null && (
          <p className="ds-caption" role="status">
            {expired === 0
              ? t('crm.nothingExpired')
              : t('crm.pointsExpired', { count: String(expired) })}
          </p>
        )}
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: LoyaltyCard[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('crm.noMembersTitle')}
                body={t('crm.noMembersBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('crm.customer')}</th>
                    <th scope="col">{t('crm.segment')}</th>
                    <th scope="col">{t('crm.tier')}</th>
                    <th scope="col" className="num">
                      {t('crm.points')}
                    </th>
                    <th scope="col" className="num">
                      {t('crm.lifetimeSpend')}
                    </th>
                    <th scope="col">{t('crm.lastSeen')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((m) => (
                    <tr key={m.customer_id}>
                      <td>
                        <span className="detail__strong">{m.customer}</span>
                        <span className="ds-caption">
                          {t('crm.visits', { count: String(m.visits) })}
                        </span>
                      </td>
                      <td>
                        <span
                          className={`ds-badge ds-badge--${segmentBadge(m.segment)}`}
                        >
                          {t(`crm.segment.${m.segment}` as Key)}
                        </span>
                      </td>
                      <td>
                        {m.tier ?? '—'}
                        {/* How far off the next rung is. A member given a badge
                            and no next step has been given a badge. */}
                        {m.next_tier && (
                          <span className="ds-caption">
                            {t('crm.toNextTier', {
                              amount: money(m.to_next_tier, {
                                currency: m.currency,
                              }),
                              tier: m.next_tier,
                            })}
                          </span>
                        )}
                      </td>
                      <td className="num">
                        {m.points}
                        <span className="ds-caption">
                          {money(m.worth, { currency: m.currency })}
                        </span>
                        {m.expiring_soon ? (
                          <span className="ds-badge ds-badge--warning">
                            {t('crm.expiringSoon', {
                              count: String(m.expiring_soon),
                            })}
                          </span>
                        ) : null}
                      </td>
                      <td className="num">
                        {money(m.lifetime_spend, { currency: m.currency })}
                      </td>
                      <td>
                        {m.last_purchase
                          ? shortDate(m.last_purchase, locale)
                          : '—'}
                      </td>
                      <td>
                        <button
                          className="ds-btn ds-btn--quiet"
                          onClick={() => setOpen(m.customer_id)}
                        >
                          {t('action.view')}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        }
      </RemoteBody>
    </section>
  );
}

function segmentBadge(segment: string): string {
  switch (segment) {
    case 'vip':
      return 'success';
    case 'at_risk':
      return 'danger';
    case 'new':
      return 'info';
    default:
      return 'neutral';
  }
}

function MemberCard({
  companyId,
  customerId,
  onBack,
}: {
  companyId: string;
  customerId: string;
  onBack: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayAdjust = can('loyalty.manage');
  const mayGiveCredit = can('wallet.manage');

  const load = useCallback(
    () => readCard(client, companyId, customerId),
    [client, companyId, customerId],
  );
  const { remote, reload } = useRemote(load);

  const walletLoad = useCallback(
    () => readWallet(client, companyId, customerId),
    [client, companyId, customerId],
  );
  const wallet = useRemote(walletLoad);

  const [adjusting, setAdjusting] = useState(false);
  const [crediting, setCrediting] = useState(false);

  return (
    <>
      <RemoteBody remote={remote} onRetry={reload}>
        {(card: LoyaltyCard) => (
          <section className="ds-panel">
            <div className="ds-panel__head">
              <div>
                <button className="ds-btn ds-btn--quiet" onClick={onBack}>
                  {t('action.back')}
                </button>
                <h2 className="ds-h3">{card.customer}</h2>
                <p className="ds-caption">
                  {t(`crm.segment.${card.segment}` as Key)}
                  {card.tier ? ` · ${card.tier}` : ''}
                </p>
              </div>
              <div className="crm__actions">
                {mayGiveCredit && (
                  <button
                    className="ds-btn ds-btn--quiet"
                    onClick={() => setCrediting(true)}
                  >
                    {t('crm.giveCredit')}
                  </button>
                )}
                {mayAdjust && (
                  <button
                    className="ds-btn ds-btn--quiet"
                    onClick={() => setAdjusting(true)}
                  >
                    {t('crm.adjustPoints')}
                  </button>
                )}
              </div>
            </div>

            <div className="ds-panel__body">
              <dl className="crm__facts">
                <div>
                  <dt>{t('crm.points')}</dt>
                  <dd>{card.points}</dd>
                </div>
                <div>
                  <dt>{t('crm.worth')}</dt>
                  <dd className="num">
                    {money(card.worth, { currency: card.currency })}
                  </dd>
                </div>
                <div>
                  <dt>{t('crm.lifetimeSpend')}</dt>
                  <dd className="num">
                    {money(card.lifetime_spend, { currency: card.currency })}
                  </dd>
                </div>
                <div>
                  <dt>{t('crm.visits')}</dt>
                  <dd>{card.visits}</dd>
                </div>
                {wallet.remote.state === 'ready' && (
                  <div>
                    <dt>{t('crm.storeCredit')}</dt>
                    <dd className="num">
                      {money(wallet.remote.data.balance, {
                        currency: wallet.remote.data.currency,
                      })}
                    </dd>
                  </div>
                )}
              </dl>
            </div>

            {adjusting && (
              <AdjustForm
                companyId={companyId}
                customerId={customerId}
                onCancel={() => setAdjusting(false)}
                onAdjusted={() => {
                  setAdjusting(false);
                  reload();
                }}
              />
            )}

            {crediting && (
              <CreditForm
                companyId={companyId}
                customerId={customerId}
                onCancel={() => setCrediting(false)}
                onGiven={() => {
                  setCrediting(false);
                  wallet.reload();
                }}
              />
            )}

            <div className="ds-panel__body ds-scroll-x">
              <h3 className="ds-h3">{t('crm.pointsHistory')}</h3>
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('common.when')}</th>
                    <th scope="col">{t('crm.why')}</th>
                    <th scope="col" className="num">
                      {t('crm.points')}
                    </th>
                    <th scope="col">{t('crm.against')}</th>
                  </tr>
                </thead>
                <tbody>
                  {(card.entries ?? []).map((e) => (
                    <tr key={e.id}>
                      <td>{shortDate(e.created_at, locale)}</td>
                      <td>
                        {t(`crm.reason.${e.reason}` as Key)}
                        {e.note && <span className="ds-caption">{e.note}</span>}
                      </td>
                      <td className="num">
                        {e.points > 0 ? `+${e.points}` : e.points}
                      </td>
                      <td>
                        {e.invoice_no ?? '—'}
                        {e.expires_on && (
                          <span className="ds-caption">
                            {t('crm.expiresOn', {
                              date: shortDate(e.expires_on, locale),
                            })}
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        )}
      </RemoteBody>

      <RemoteBody remote={wallet.remote} onRetry={wallet.reload}>
        {(w: Wallet) =>
          (w.entries ?? []).length === 0 ? (
            <></>
          ) : (
            <section className="ds-panel" aria-label={t('crm.storeCredit')}>
              <div className="ds-panel__head">
                <h3 className="ds-h3">{t('crm.creditHistory')}</h3>
              </div>
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('common.when')}</th>
                      <th scope="col">{t('crm.why')}</th>
                      <th scope="col" className="num">
                        {t('common.amount')}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {(w.entries ?? []).map((e) => (
                      <tr key={e.id}>
                        <td>{shortDate(e.created_at, locale)}</td>
                        <td>
                          {t(`crm.creditReason.${e.reason}` as Key)}
                          {e.note && <span className="ds-caption">{e.note}</span>}
                        </td>
                        <td className="num">
                          {money(e.amount, { currency: e.currency, signed: true })}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )
        }
      </RemoteBody>
    </>
  );
}

function AdjustForm({
  companyId,
  customerId,
  onCancel,
  onAdjusted,
}: {
  companyId: string;
  customerId: string;
  onCancel: () => void;
  onAdjusted: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [points, setPoints] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await adjustPoints(client, companyId, customerId, {
        points: Number(points),
        note,
      });
      onAdjusted();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel crm__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field
            label={t('crm.pointsToAdjust')}
            hint={t('crm.pointsToAdjustHint')}
            htmlFor="adj-points"
            required
          >
            <TextInput
              id="adj-points"
              value={points}
              onChange={setPoints}
              inputMode="numeric"
            />
          </Field>
          <Field label={t('crm.whyAdjusting')} htmlFor="adj-note" required>
            <TextInput id="adj-note" value={note} onChange={setNote} />
          </Field>
        </div>
        <FormActions
          submitLabel={t('crm.adjustPoints')}
          busy={busy}
          disabled={points.trim() === '' || note.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

function CreditForm({
  companyId,
  customerId,
  onCancel,
  onGiven,
}: {
  companyId: string;
  customerId: string;
  onCancel: () => void;
  onGiven: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [amount, setAmount] = useState('');
  const [note, setNote] = useState('');
  const [expires, setExpires] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await giveCredit(client, companyId, customerId, {
        amount,
        note,
        expires_on: expires || undefined,
      });
      onGiven();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel crm__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('common.amount')} htmlFor="cr-amount" required>
            <TextInput
              id="cr-amount"
              value={amount}
              onChange={setAmount}
              inputMode="decimal"
            />
          </Field>
          <Field
            label={t('crm.whyCredit')}
            hint={t('crm.whyCreditHint')}
            htmlFor="cr-note"
            required
          >
            <TextInput id="cr-note" value={note} onChange={setNote} />
          </Field>
          <Field label={t('crm.creditExpires')} htmlFor="cr-expires">
            <input
              id="cr-expires"
              type="date"
              className="field__input"
              value={expires}
              onChange={(e) => setExpires(e.target.value)}
            />
          </Field>
        </div>
        <FormActions
          submitLabel={t('crm.giveCredit')}
          busy={busy}
          disabled={amount.trim() === '' || note.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
