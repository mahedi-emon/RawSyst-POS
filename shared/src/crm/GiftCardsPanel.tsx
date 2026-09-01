// Gift cards (blueprint B16).
//
// # Selling one is not a sale
//
// The shop has taken money and owes goods. No revenue and no VAT until it is
// spent, which is why issuing a card asks how it was PAID FOR rather than
// ringing it up — and why a card given away for nothing is a different act with
// a different posting.
//
// # Cancelling writes the balance back
//
// A cancelled card is money the shop no longer owes, and the ledger hears about
// it on the day it stopped owing it. The screen says how much will be written
// back before somebody presses it.

import { useCallback, useState } from 'react';

import {
  issueGiftCard,
  listGiftCards,
  lookUpGiftCard,
  voidGiftCard,
  type GiftCard,
} from '../api/crm';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Field, FormActions, FormError, SelectInput, TextInput } from '../ui/Form';
import { money, shortDate } from '../ui/format';

// Where the money for a card landed. Roles rather than accounts, so a shop that
// renamed its Cash account does not break the posting.
const TENDERS = ['cash', 'bank', 'card_clearing'] as const;

export function GiftCardsPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('wallet.manage');
  const [issuing, setIssuing] = useState(false);
  const [showCancelled, setShowCancelled] = useState(false);
  const [lookup, setLookup] = useState('');
  const [found, setFound] = useState<GiftCard | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [voiding, setVoiding] = useState<GiftCard | null>(null);

  const load = useCallback(
    () => listGiftCards(client, companyId, showCancelled),
    [client, companyId, showCancelled],
  );
  const { remote, reload } = useRemote(load);

  async function find(e: React.FormEvent) {
    e.preventDefault();
    setFailure(null);
    setFound(null);
    try {
      setFound(await lookUpGiftCard(client, companyId, lookup));
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    }
  }

  return (
    <>
      {/* The counter's question, at the top: a customer is holding a card and
          wants to know what is on it. */}
      <form className="ds-panel crm__lookup" onSubmit={(e) => void find(e)}>
        <div className="ds-panel__body crm__lookuprow">
          <Field label={t('crm.checkACard')} htmlFor="gc-lookup">
            <TextInput
              id="gc-lookup"
              value={lookup}
              onChange={setLookup}
              placeholder={t('crm.cardNumber')}
            />
          </Field>
          <button className="ds-btn ds-btn--primary" type="submit">
            {t('crm.checkBalance')}
          </button>
          {found && (
            <p className="crm__found" role="status">
              {t('crm.cardHolds', {
                code: found.code,
                amount: money(found.balance, { currency: found.currency }),
              })}
              {found.is_void ? ` · ${t('crm.cardCancelled')}` : ''}
              {found.expired ? ` · ${t('crm.cardExpired')}` : ''}
            </p>
          )}
        </div>
        <div className="ds-panel__body">
          <FormError message={failure} />
        </div>
      </form>

      {issuing && (
        <IssueForm
          companyId={companyId}
          onCancel={() => setIssuing(false)}
          onIssued={() => {
            setIssuing(false);
            reload();
          }}
        />
      )}

      {voiding && (
        <VoidForm
          companyId={companyId}
          card={voiding}
          onCancel={() => setVoiding(null)}
          onVoided={() => {
            setVoiding(null);
            reload();
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('crm.giftCards')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('crm.giftCards')}</h2>
          <div className="crm__actions">
            <label className="crm__check">
              <input
                type="checkbox"
                checked={showCancelled}
                onChange={(e) => setShowCancelled(e.target.checked)}
              />
              <span>{t('crm.showCancelled')}</span>
            </label>
            {mayManage && !issuing && (
              <button
                className="ds-btn ds-btn--primary"
                onClick={() => setIssuing(true)}
              >
                {t('crm.issueCard')}
              </button>
            )}
          </div>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: GiftCard[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('crm.noCardsTitle')}
                  body={t('crm.noCardsBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('crm.cardNumber')}</th>
                      <th scope="col" className="num">
                        {t('crm.faceValue')}
                      </th>
                      <th scope="col" className="num">
                        {t('crm.balance')}
                      </th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">{t('crm.issued')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((c) => (
                      <tr
                        key={c.id}
                        className={c.is_void ? 'detail__row--aside' : undefined}
                      >
                        <td>
                          <span className="crm__code">{c.code}</span>
                          {c.customer && (
                            <span className="ds-caption">{c.customer}</span>
                          )}
                        </td>
                        <td className="num">
                          {money(c.face_value, { currency: c.currency })}
                        </td>
                        <td className="num">
                          {money(c.balance, { currency: c.currency })}
                        </td>
                        <td>
                          {c.is_void ? (
                            <span className="ds-badge ds-badge--neutral">
                              {t('crm.cardCancelled')}
                            </span>
                          ) : c.expired ? (
                            <span className="ds-badge ds-badge--warning">
                              {t('crm.cardExpired')}
                            </span>
                          ) : (
                            <span className="ds-badge ds-badge--success">
                              {t('crm.cardLive')}
                            </span>
                          )}
                          {c.expires_on && !c.expired && (
                            <span className="ds-caption">
                              {t('crm.expiresOn', {
                                date: shortDate(c.expires_on, locale),
                              })}
                            </span>
                          )}
                        </td>
                        <td>
                          {shortDate(c.issued_at, locale)}
                          {c.issued_by && (
                            <span className="ds-caption">{c.issued_by}</span>
                          )}
                        </td>
                        <td>
                          {mayManage && !c.is_void && (
                            <button
                              className="ds-btn ds-btn--quiet"
                              onClick={() => setVoiding(c)}
                            >
                              {t('crm.cancelCard')}
                            </button>
                          )}
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
    </>
  );
}

function IssueForm({
  companyId,
  onCancel,
  onIssued,
}: {
  companyId: string;
  onCancel: () => void;
  onIssued: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [value, setValue] = useState('');
  const [code, setCode] = useState('');
  const [expires, setExpires] = useState('');
  const [paid, setPaid] = useState(true);
  const [tender, setTender] = useState<string>('cash');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [minted, setMinted] = useState<GiftCard | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      const card = await issueGiftCard(client, companyId, {
        face_value: value,
        code: code || undefined,
        expires_on: expires || undefined,
        proceeds: paid ? [{ role: tender, amount: value }] : undefined,
      });
      setMinted(card);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  // The number, once, large enough to write on a card.
  if (minted) {
    return (
      <section className="ds-panel crm__minted">
        <div className="ds-panel__body">
          <p className="ds-caption">{t('crm.cardIssued')}</p>
          <p className="crm__mintedcode">{minted.code}</p>
          <p className="ds-caption">
            {money(minted.face_value, { currency: minted.currency })}
          </p>
          <button
            className="ds-btn ds-btn--primary"
            onClick={() => {
              setMinted(null);
              onIssued();
            }}
          >
            {t('common.done')}
          </button>
        </div>
      </section>
    );
  }

  return (
    <form className="ds-panel crm__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('crm.issueCard')}</h2>
        <p className="ds-caption">{t('crm.issueCardHint')}</p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('crm.faceValue')} htmlFor="gc-value" required>
            <TextInput
              id="gc-value"
              value={value}
              onChange={setValue}
              inputMode="decimal"
            />
          </Field>
          <Field
            label={t('crm.cardNumber')}
            hint={t('crm.cardNumberHint')}
            htmlFor="gc-code"
          >
            <TextInput id="gc-code" value={code} onChange={setCode} />
          </Field>
          <Field label={t('crm.cardExpires')} htmlFor="gc-expires">
            <input
              id="gc-expires"
              type="date"
              className="field__input"
              value={expires}
              onChange={(e) => setExpires(e.target.value)}
            />
          </Field>
          {paid && (
            <Field label={t('crm.paidBy')} htmlFor="gc-tender" required>
              <SelectInput
                id="gc-tender"
                value={tender}
                onChange={setTender}
                options={TENDERS.map((x) => ({ id: x }))}
                label={(x) => t(`crm.tender.${x.id}` as Key)}
              />
            </Field>
          )}
        </div>

        <label className="crm__check">
          <input
            type="checkbox"
            checked={!paid}
            onChange={(e) => setPaid(!e.target.checked)}
          />
          {/* A card given away costs the shop rather than earning it, and it
              posts differently. Worth one checkbox to get right. */}
          <span>{t('crm.givenFree')}</span>
        </label>

        <FormActions
          submitLabel={t('crm.issueCard')}
          busy={busy}
          disabled={value.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}

function VoidForm({
  companyId,
  card,
  onCancel,
  onVoided,
}: {
  companyId: string;
  card: GiftCard;
  onCancel: () => void;
  onVoided: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await voidGiftCard(client, companyId, card.id, reason);
      onVoided();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel crm__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('crm.cancelCard')}</h2>
        {/* Said before it happens, because it is a movement in the books. */}
        <p className="ds-caption">
          {t('crm.cancelCardHint', {
            code: card.code,
            amount: money(card.balance, { currency: card.currency }),
          })}
        </p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('crm.whyCancelling')} htmlFor="gc-reason" required>
            <TextInput id="gc-reason" value={reason} onChange={setReason} />
          </Field>
        </div>
        <FormActions
          submitLabel={t('crm.cancelCard')}
          busy={busy}
          disabled={reason.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
