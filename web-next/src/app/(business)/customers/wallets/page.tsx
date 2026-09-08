'use client';

// Store credit, and the gift cards that are the same liability in another form.
//
// # This is a liability, not a customer list
//
// Every balance here is money the business has already taken and still owes in
// goods. So the total leads, and customers with nothing on their wallet are not
// listed at all: a page of names with zero beside most of them is not a list of
// what is owed, and the empty rows are the ones nobody is looking for.
//
// The total is added from the rows rather than asked of the server, because the
// rows ARE the answer. A total from a second query could disagree with the list
// above it, which on a liability is the worst kind of disagreement.
//
// # Selling a gift card is not a sale
//
// No revenue and no tax until it is spent, or the month it was sold in is
// overstated and the tax charged twice. The screen keeps the two lists apart
// for that reason: an unspent card is a promise, and a spent one was a sale
// that happened later.

import { Wallet } from 'lucide-react';
import { Suspense, useState } from 'react';

import { Can, RequirePermission } from '@/components/auth/guard';
import { Figure, PageHeader, Panel, Badge } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { Button } from '@/components/ui/button';
import { Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { creditOwed, type WalletRow } from '@/lib/pos/counter-ops';

interface GiftCard {
  id: string;
  code: string;
  face_value: string;
  balance: string;
  currency: string;
  expires_on?: string;
  is_active?: boolean;
  status?: string;
}

function WalletsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const wallets = useApiList<WalletRow>(
    scope ? '/wallets' : null,
    scope ?? undefined,
  );
  const cards = useApiList<GiftCard>(
    scope ? '/gift-cards' : null,
    scope ?? undefined,
  );

  // Finding a card by the number printed on it, which is what a cashier has in
  // their hand. A GET, because checking a balance changes nothing -- the route
  // says so in its own description.
  const [code, setCode] = useState('');
  const [found, setFound] = useState<GiftCard | null>(null);
  const [lookupError, setLookupError] = useState<string | null>(null);
  const [voiding, setVoiding] = useState<GiftCard | null>(null);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string> | null>(null);

  async function lookUp() {
    if (!scope || code.trim() === '') return;
    setBusy(true);
    setLookupError(null);
    setFound(null);
    try {
      const card = await api.get<GiftCard>(
        `/gift-cards/by-code/${encodeURIComponent(code.trim())}?company_id=${scope.company_id}`,
      );
      setFound(card);
    } catch (e) {
      // A number that matches nothing is the ordinary answer to a mistyped
      // card, not a fault, so it reads as one sentence rather than an error
      // panel.
      setLookupError(
        e instanceof ApiError && e.status === 404
          ? t('nx.wal.lookupNone')
          : messageFor(e, t),
      );
    } finally {
      setBusy(false);
    }
  }

  async function voidCard() {
    if (!scope || !voiding) return;
    setBusy(true);
    setActionError(null);
    setFields(null);
    try {
      await api.post(
        `/gift-cards/${voiding.id}/void?company_id=${scope.company_id}`,
        { reason },
      );
      setVoiding(null);
      setReason('');
      setFound(null);
      void cards.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const rows = wallets.data?.data ?? [];
  const cardRows = cards.data?.data ?? [];
  const money = (v: string, c?: string) =>
    formatMoney(v, { currency: c || currency, market });

  const owedInCredit = creditOwed(rows);
  const owedInCards = creditOwed(
    cardRows.map((c) => ({
      customer_id: c.id,
      balance: c.balance,
      currency: c.currency,
    })),
  );

  const walletColumns: Column<WalletRow>[] = [
    {
      key: 'customer',
      header: t('nx.wal.colCustomer'),
      primary: true,
      cell: (w) => w.customer ?? w.customer_id,
    },
    {
      key: 'balance',
      header: t('nx.wal.colBalance'),
      numeric: true,
      width: 'w-40',
      cell: (w) => (
        <span className="num font-medium">{money(w.balance, w.currency)}</span>
      ),
    },
  ];

  const cardColumns: Column<GiftCard>[] = [
    {
      key: 'code',
      header: t('nx.wal.colCode'),
      primary: true,
      cell: (c) => <span className="num">{c.code}</span>,
    },
    {
      key: 'face',
      header: t('nx.wal.colFace'),
      numeric: true,
      secondary: true,
      width: 'w-32',
      cell: (c) => (
        <span className="num text-muted">{money(c.face_value, c.currency)}</span>
      ),
    },
    {
      key: 'balance',
      header: t('nx.wal.colLeft'),
      numeric: true,
      width: 'w-32',
      cell: (c) => (
        <span className="num font-medium">{money(c.balance, c.currency)}</span>
      ),
    },
    {
      key: 'expires',
      header: t('nx.wal.colExpires'),
      secondary: true,
      width: 'w-36',
      cell: (c) =>
        c.expires_on ? (
          <time dateTime={c.expires_on} className="num text-muted">
            {c.expires_on}
          </time>
        ) : (
          <span className="text-muted">{t('nx.wal.noExpiry')}</span>
        ),
    },
    {
      key: 'state',
      header: t('nx.wal.colState'),
      width: 'w-32',
      cell: (c) =>
        c.status ? (
          <Badge tone={c.status === 'active' ? 'positive' : 'neutral'}>
            {c.status}
          </Badge>
        ) : (
          <span className="text-muted">—</span>
        ),
    },
    {
      key: 'actions',
      header: '',
      width: 'w-24',
      // Only a live card can be voided. Offering it on one already voided
      // suggests a second void would do something.
      cell: (c) =>
        c.status === 'active' ? (
          <Can permission="wallet.manage">
            <span className="flex justify-end">
              <Button size="sm" variant="ghost" onClick={() => setVoiding(c)}>
                {t('nx.wal.void')}
              </Button>
            </span>
          </Can>
        ) : null,
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.wal.title')} description={t('nx.wal.subtitle')} />

      <Can permission="wallet.view">
        <Panel
          className="mb-5"
          title={t('nx.wal.lookupTitle')}
          description={t('nx.wal.lookupHint')}
        >
          <div className="flex flex-wrap items-end gap-3">
            <Field name="code" label={t('nx.wal.lookupCode')}>
              <Input
                dir="ltr"
                className="num w-56"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void lookUp();
                }}
              />
            </Field>
            <Button disabled={busy || code.trim() === ''} onClick={() => void lookUp()}>
              {t('nx.wal.lookup')}
            </Button>
          </div>

          {lookupError ? (
            <p className="mt-3 text-body text-muted">{lookupError}</p>
          ) : null}

          {found ? (
            <p className="mt-3 text-body text-fg">
              {t('nx.wal.found', {
                code: found.code,
                balance: money(found.balance, found.currency),
                face: money(found.face_value, found.currency),
              })}
            </p>
          ) : null}
        </Panel>
      </Can>

      {voiding ? (
        <Panel
          className="mb-5"
          title={t('nx.wal.voidTitle', { code: voiding.code })}
        >
          <p className="mb-4 text-body text-muted">{t('nx.wal.voidBody')}</p>
          <Field
            name="reason"
            label={t('nx.wal.voidReason')}
            hint={t('nx.wal.voidReasonHint')}
            error={fields?.reason}
          >
            <Textarea
              rows={2}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              required
            />
          </Field>
          {actionError ? <FormError message={actionError} /> : null}
          <div className="mt-4 flex flex-wrap gap-2">
            <Button variant="destructive" disabled={busy} onClick={() => void voidCard()}>
              {t('nx.wal.voidConfirm')}
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => setVoiding(null)}>
              {t('nx.wal.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {wallets.error ? (
        <ErrorState error={wallets.error} onRetry={() => void wallets.refetch()} />
      ) : null}

      <div className="mb-6 grid gap-4 sm:grid-cols-3">
        <Panel>
          {/* Added from the rows, not asked separately: a total that could
              disagree with the list under it is the worst kind on a
              liability. */}
          <Figure
            label={t('nx.wal.owedCredit')}
            value={money(owedInCredit)}
            caption={t('nx.wal.owedHint')}
          />
        </Panel>
        <Panel>
          <Figure label={t('nx.wal.owedCards')} value={money(owedInCards)} />
        </Panel>
        <Panel>
          <Figure
            label={t('nx.wal.holders')}
            value={String(rows.length)}
            caption={t('nx.wal.holdersHint')}
          />
        </Panel>
      </div>

      <h2 className="mb-3 text-card-title font-semibold text-fg">
        {t('nx.wal.creditTitle')}
      </h2>
      {wallets.isLoading && !wallets.data ? <TableSkeleton columns={2} /> : null}
      {!wallets.isLoading && rows.length === 0 ? (
        <EmptyState
          icon={Wallet}
          title={t('nx.wal.noCreditTitle')}
          description={t('nx.wal.noCreditDesc')}
        />
      ) : null}
      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.wal.creditTitle')}
          columns={walletColumns}
          rows={rows}
          rowKey={(w) => w.customer_id}
        />
      ) : null}

      <h2 className="mt-8 mb-1 text-card-title font-semibold text-fg">
        {t('nx.wal.cardsTitle')}
      </h2>
      <p className="mb-3 max-w-prose text-caption text-muted">
        {t('nx.wal.cardsHint')}
      </p>
      {cards.isLoading && !cards.data ? <TableSkeleton columns={5} /> : null}
      {!cards.isLoading && cardRows.length === 0 ? (
        <EmptyState
          title={t('nx.wal.noCardsTitle')}
          description={t('nx.wal.noCardsDesc')}
        />
      ) : null}
      {cardRows.length > 0 ? (
        <DataTable
          caption={t('nx.wal.cardsTitle')}
          columns={cardColumns}
          rows={cardRows}
          rowKey={(c) => c.id}
        />
      ) : null}
    </>
  );
}

export default function WalletsPage() {
  return (
    <RequirePermission anyOf={['wallet.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <WalletsScreen />
      </Suspense>
    </RequirePermission>
  );
}
