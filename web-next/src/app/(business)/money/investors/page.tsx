'use client';

// Who put money into the business, and what they have taken back out.
//
// # Contributed and withdrawn are both shown, never just the net
//
// A partner who put in a hundred thousand and drew ninety-five is not the same
// as one who put in five and drew nothing, and the net is identical. So both
// sides are columns and the net sits beside them rather than replacing them —
// which is also how a capital account is read on paper.
//
// # This is equity, not a supplier balance
//
// Money an owner puts in is not a liability the business will settle in the
// ordinary course, and money they draw is not an expense. The screen keeps the
// vocabulary of capital and drawings rather than of payable and paid, because
// the two behave differently at year end and calling them the same thing on a
// screen is how they get treated the same in a conversation.
//
// # An inactive investor keeps their history
//
// `is_active` false means nobody expects further movements, not that the person
// was never here. Their contributions are in the ledger and their statement
// still resolves, so the row stays and says which it is.

import { HandCoins } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isNegative } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

interface Investor {
  id: string;
  name: string;
  name_ar?: string;
  kind: string;
  email?: string;
  phone?: string;
  note?: string;
  is_active: boolean;
  contributed: string;
  withdrawn: string;
  net: string;
  currency: string;
}

/** A cash or bank account, as the treasury list answers it. */
interface MoneyAccount {
  id: string;
  name: string;
  kind: string;
  currency: string;
  is_active: boolean;
}

function InvestorsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayManage = grants.can('investor.manage');

  const { data, isLoading, error, refetch } = useApiList<Investor>(
    scope ? '/investors' : null,
    { company_id: scope?.company_id },
  );

  // Capital in and drawings out both move real money, so the movement has to
  // say which cash or bank account it moved through.
  const accounts = useApiList<MoneyAccount>(scope ? '/treasury/accounts' : null, {
    company_id: scope?.company_id,
  });

  const [movement, setMovement] = useState<Investor | null>(null);
  const [direction, setDirection] = useState('contribution');
  const [amount, setAmount] = useState('');
  const [when, setWhen] = useState('');
  const [memo, setMemo] = useState('');
  const [account, setAccount] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  const rows = data?.data ?? [];

  async function record() {
    if (!scope || !movement) return;
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    try {
      await api.post(`/investors/movements?company_id=${scope.company_id}`, {
        investor_id: movement.id,
        direction,
        amount,
        moved_on: when,
        money_account_id: account,
        note: memo,
      });
      setMovement(null);
      setAmount('');
      setWhen('');
      setMemo('');
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Investor>[] = [
    {
      key: 'name',
      header: t('nx.inv.investor'),
      primary: true,
      cell: (x) => (
        <span className="flex flex-col">
          <span className="flex items-center gap-2">
            {x.name}
            {!x.is_active ? (
              <Badge tone="neutral">{t('nx.inv.inactive')}</Badge>
            ) : null}
          </span>
          <span className="text-caption text-muted">{x.kind}</span>
        </span>
      ),
    },
    {
      key: 'contact',
      header: t('nx.inv.contact'),
      secondary: true,
      cell: (x) => (
        <span className="flex flex-col text-caption text-muted">
          {x.email ? <span dir="ltr">{x.email}</span> : null}
          {x.phone ? (
            <span className="num" dir="ltr">
              {x.phone}
            </span>
          ) : null}
          {!x.email && !x.phone ? '—' : null}
        </span>
      ),
    },
    {
      key: 'contributed',
      header: t('nx.inv.contributed'),
      numeric: true,
      width: 'w-36',
      cell: (x) => (
        <span className="num">
          {formatMoney(x.contributed, {
            currency: x.currency || currency,
            market,
            bare: true,
          })}
        </span>
      ),
    },
    {
      key: 'withdrawn',
      header: t('nx.inv.withdrawn'),
      numeric: true,
      width: 'w-36',
      cell: (x) => (
        <span className="num text-muted">
          {formatMoney(x.withdrawn, {
            currency: x.currency || currency,
            market,
            bare: true,
          })}
        </span>
      ),
    },
    {
      key: 'net',
      header: t('nx.inv.net'),
      numeric: true,
      width: 'w-40',
      // A negative capital account means the partner has drawn more than they
      // put in. The sign carries it as well as the colour, because colour alone
      // is not a signal a colour-blind reader gets.
      cell: (x) => (
        <span
          className={`num font-medium ${isNegative(x.net) ? 'text-critical-fg' : ''}`}
        >
          {formatMoney(x.net, { currency: x.currency || currency, market })}
        </span>
      ),
    },
  ];

  if (mayManage) {
    columns.push({
      key: 'record',
      header: t('nx.inv.recordHeader'),
      width: 'w-32',
      cell: (x) => (
        <Button size="sm" variant="ghost" onClick={() => setMovement(x)}>
          {t('nx.inv.record')}
        </Button>
      ),
    });
  }

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader title={t('nx.inv.title')} description={t('nx.inv.subtitle')} />

      <FormError message={actionError} fields={fieldErrors} className="mb-4" />

      {movement ? (
        <Panel
          title={t('nx.inv.movementTitle', { name: movement.name })}
          className="mb-4"
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field name="kind" label={t('nx.inv.direction')}>
              <Select
                value={direction}
                onChange={(e) => setDirection(e.target.value)}
              >
                <option value="contribution">{t('nx.inv.contribution')}</option>
                <option value="withdrawal">{t('nx.inv.withdrawal')}</option>
              </Select>
            </Field>
            <Field name="amount" label={t('nx.inv.amount')}>
              <Input
                inputMode="decimal"
                dir="ltr"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                required
              />
            </Field>
            <Field name="occurred_on" label={t('nx.inv.when')}>
              <Input
                type="date"
                value={when}
                onChange={(e) => setWhen(e.target.value)}
                required
              />
            </Field>
            {/* A money_account id, NOT a chart-account id. The two are both
                uuids on the same row of GET /treasury/accounts — `id` and
                `account_id` — and sending the wrong one 404s with a message
                that names neither. */}
            <Field
              name="money_account_id"
              label={t('nx.inv.account')}
              hint={t('nx.inv.accountHint')}
            >
              <Select
                value={account}
                onChange={(e) => setAccount(e.target.value)}
                required
              >
                <option value="">{t('nx.inv.chooseAccount')}</option>
                {(accounts.data?.data ?? [])
                  .filter((a) => a.is_active)
                  .map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                    </option>
                  ))}
              </Select>
            </Field>
            <Field name="note" label={t('nx.inv.memo')}>
              <Textarea rows={2} value={memo} onChange={(e) => setMemo(e.target.value)} />
            </Field>
          </div>
          <div className="mt-6 flex flex-wrap gap-2">
            <Button
              busy={busy}
              busyLabel={t('nx.inv.saving')}
              disabled={amount.trim() === '' || when === ''}
              onClick={() => void record()}
            >
              {t('nx.inv.save')}
            </Button>
            <Button variant="ghost" onClick={() => setMovement(null)}>
              {t('nx.inv.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {isLoading && !data ? <TableSkeleton columns={6} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={HandCoins}
          title={t('nx.inv.emptyTitle')}
          description={t('nx.inv.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<Investor>
          rows={rows}
          columns={columns}
          rowKey={(x) => x.id}
          caption={t('nx.inv.caption')}
        />
      ) : null}
    </>
  );
}

export default function InvestorsPage() {
  return (
    <RequirePermission anyOf={['investor.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <InvestorsScreen />
      </Suspense>
    </RequirePermission>
  );
}
