'use client';

// One customer's points and their store credit, and the two things a shop
// actually does to them.
//
// # Why this had to be built
//
// `loyalty.manage` and `wallet.manage` were both grantable permissions with
// nothing to exercise them. The loyalty screen listed every member and could
// expire points across all of them; the wallets screen showed what the business
// owed in total. Neither could touch ONE customer, and the four routes that can
// — read a card, adjust its points, read a wallet, put credit on it — had no
// caller anywhere.
//
// What that cost, concretely: a customer complains that a purchase did not earn
// its points, or is owed a goodwill credit after a bad delivery. Both are
// ordinary shop-counter events, both were impossible, and the workaround —
// ringing up a fake sale — puts revenue and tax on a month that did not earn
// them.
//
// # Here, rather than on the loyalty and wallet screens
//
// Those two are liability registers: they answer "what do we owe, in total".
// Adjusting one person's points is a fact about that person, and this is the
// page somebody is already on when the customer is in front of them. It also
// avoids a customer picker over a list that can run to thousands.
//
// # Points are adjusted with a reason, and the reason is required
//
// An adjustment moves a liability. Six months later "why does this customer
// have 4,000 points" has to have an answer, and "somebody typed it" is not one.
// The server takes a note; this makes it mandatory before the button works.
//
// # Nothing here is shown when there is no scheme
//
// A shop with no loyalty programme is not a shop whose customers have zero
// points, and offering to adjust a balance that cannot exist is how somebody
// concludes the feature is broken. The card read answers 404 in that case and
// the section stays away.

import { Gift, Sparkles } from 'lucide-react';
import { useState } from 'react';

import { Can } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useCompany } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

interface Card {
  customer_id: string;
  customer: string;
  points: number;
  worth: string;
  tier?: string;
  currency: string;
  segment?: string;
  expiring_soon?: number;
}

interface Wallet {
  customer_id: string;
  customer?: string;
  balance: string;
  currency: string;
}

export function RewardsPanel({
  customerId,
  companyId,
}: {
  customerId: string;
  companyId: string;
}) {
  const t = useT();
  const { currency, market } = useCompany();
  const scope = { company_id: companyId };

  const card = useApi<Card>(`/loyalty/members/${customerId}`, scope);
  const wallet = useApi<Wallet>(`/wallets/${customerId}`, scope);

  const [points, setPoints] = useState('');
  const [pointsNote, setPointsNote] = useState('');
  const [amount, setAmount] = useState('');
  const [expires, setExpires] = useState('');
  const [creditNote, setCreditNote] = useState('');

  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState<string | null>(null);

  const q = `?company_id=${companyId}`;
  const money = (v: string, c?: string) =>
    formatMoney(v, { currency: c || currency, market });

  async function run(what: string, fn: () => Promise<string>) {
    setBusy(what);
    setError(null);
    setFieldErrors({});
    setDone(null);
    try {
      setDone(await fn());
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  // A whole number, and not zero. `Number('')` is 0 and an adjustment of zero
  // is a record of nothing that still names somebody as having made it.
  const pointsValue = Number(points.trim());
  const pointsUsable =
    points.trim() !== '' &&
    Number.isInteger(pointsValue) &&
    pointsValue !== 0 &&
    pointsNote.trim() !== '';

  // 404 is the ordinary answer for a shop with no scheme, or a customer with
  // no wallet yet. Neither is a fault and neither renders as one.
  const noScheme = card.error instanceof ApiError && card.error.status === 404;
  const noWallet = wallet.error instanceof ApiError && wallet.error.status === 404;

  return (
    <>
      <FormError message={error} fields={fieldErrors} className="mb-4" />
      {done ? <p className="mb-4 text-body text-positive-fg">{done}</p> : null}

      {!noScheme ? (
        <Panel
          className="mb-6"
          title={t('nx.cust.pointsTitle')}
          description={t('nx.cust.pointsHint')}
        >
          <div className="mb-4 flex flex-wrap items-end gap-6">
            <Figure
              label={t('nx.cust.pointsHeld')}
              value={String(card.data?.points ?? 0)}
            />
            <Figure
              label={t('nx.cust.pointsWorth')}
              value={money(card.data?.worth ?? '0', card.data?.currency)}
            />
            {card.data?.tier ? <Badge tone="info">{card.data.tier}</Badge> : null}
            {card.data?.expiring_soon ? (
              // Points about to be lost are the reason a customer rings up.
              <Badge tone="caution">
                {t('nx.cust.pointsExpiring', {
                  n: String(card.data.expiring_soon),
                })}
              </Badge>
            ) : null}
          </div>

          <Can permission="loyalty.manage">
            <div className="grid items-start gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <Field
                name="points"
                label={t('nx.cust.pointsAdjust')}
                hint={t('nx.cust.pointsAdjustHint')}
                error={fieldErrors.points}
              >
                <Input
                  className="num"
                  inputMode="numeric"
                  value={points}
                  onChange={(e) => setPoints(e.target.value)}
                />
              </Field>
              <Field
                name="note"
                label={t('nx.cust.pointsWhy')}
                hint={t('nx.cust.pointsWhyHint')}
                error={fieldErrors.note}
                required
              >
                <Textarea
                  rows={2}
                  value={pointsNote}
                  onChange={(e) => setPointsNote(e.target.value)}
                />
              </Field>
              <div className="flex items-end sm:col-span-2 lg:col-span-1 lg:h-full">
                <Button
                  disabled={busy !== null || !pointsUsable}
                  onClick={() =>
                    void run('points', async () => {
                      await api.post(`/loyalty/members/${customerId}/adjust${q}`, {
                        points: pointsValue,
                        note: pointsNote.trim(),
                      });
                      setPoints('');
                      setPointsNote('');
                      void card.refetch();
                      return t('nx.cust.pointsAdjusted');
                    })
                  }
                >
                  <Sparkles aria-hidden="true" className="size-4" />
                  {busy === 'points' ? t('nx.cust.saving') : t('nx.cust.pointsApply')}
                </Button>
              </div>
            </div>
          </Can>
        </Panel>
      ) : null}

      <Panel
        className="mb-6"
        title={t('nx.cust.creditTitle')}
        description={t('nx.cust.creditHint')}
      >
        <div className="mb-4">
          <Figure
            label={t('nx.cust.creditHeld')}
            value={
              noWallet
                ? money('0')
                : money(wallet.data?.balance ?? '0', wallet.data?.currency)
            }
          />
        </div>

        <Can permission="wallet.manage">
          <div className="grid items-start gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Field
              name="amount"
              label={t('nx.cust.creditAmount')}
              hint={t('nx.cust.creditAmountHint')}
              error={fieldErrors.amount}
              required
            >
              <Input
                className="num"
                inputMode="decimal"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
              />
            </Field>
            <Field
              name="expires_on"
              label={t('nx.cust.creditExpires')}
              hint={t('nx.cust.creditExpiresHint')}
              error={fieldErrors.expires_on}
            >
              <Input
                type="date"
                value={expires}
                onChange={(e) => setExpires(e.target.value)}
              />
            </Field>
            <Field
              name="note"
              label={t('nx.cust.creditWhy')}
              hint={t('nx.cust.creditWhyHint')}
              error={fieldErrors.note}
              required
            >
              <Textarea
                rows={2}
                value={creditNote}
                onChange={(e) => setCreditNote(e.target.value)}
              />
            </Field>
            <div className="flex items-end sm:col-span-2 lg:col-span-1 lg:h-full">
              <Button
                disabled={
                  busy !== null ||
                  amount.trim() === '' ||
                  Number.isNaN(Number(amount)) ||
                  Number(amount) <= 0 ||
                  creditNote.trim() === ''
                }
                onClick={() =>
                  void run('credit', async () => {
                    await api.post(`/wallets/${customerId}/credit${q}`, {
                      amount: amount.trim(),
                      expires_on: expires,
                      note: creditNote.trim(),
                    });
                    setAmount('');
                    setExpires('');
                    setCreditNote('');
                    void wallet.refetch();
                    return t('nx.cust.creditGiven');
                  })
                }
              >
                <Gift aria-hidden="true" className="size-4" />
                {busy === 'credit' ? t('nx.cust.saving') : t('nx.cust.creditApply')}
              </Button>
            </div>
          </div>
        </Can>
      </Panel>
    </>
  );
}
