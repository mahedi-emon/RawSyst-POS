'use client';

// The things the business owns and is writing down.
//
// # Book value is derived, and the screen says both halves
//
// `depreciated` and `book_value` are summed from the depreciation ledger rather
// than stored, for the reason a bank balance is summed from the journal: a
// second copy of a number is a number that can disagree. So the screen shows
// cost and book value together — one is what was paid, the other is what is
// left, and showing only the second hides how far through its life a thing is.
//
// # Months due is the actionable column
//
// `months_due` is how many months are waiting to be charged. A business that
// has not run depreciation for a quarter has a profit figure that is wrong by
// three months of it, and that is not visible anywhere else. So it is a column,
// it is marked when it is not zero, and the run button says how many months it
// posted rather than merely that it worked.
//
// # Disposal is not deletion
//
// An asset that has been sold or scrapped keeps its row: it was on the balance
// sheet for the years it was owned, and the depreciation charged against it is
// in the ledger. `POST /assets/{id}/dispose` records the end; nothing removes
// the asset.

import { Boxes } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

/** A cash or bank account, as the treasury list answers it. */
interface MoneyAccount {
  id: string;
  name: string;
  is_active: boolean;
}

interface Asset {
  id: string;
  asset_no: string;
  name: string;
  name_ar?: string;
  category: string;
  store?: string;
  custodian?: string;
  serial_number?: string;
  warranty_until?: string;
  acquired_on: string;
  cost: string;
  residual_value: string;
  useful_life_months: number;
  currency: string;
  depreciated: string;
  book_value: string;
  monthly_charge: string;
  depreciated_to?: string;
  months_due: number;
  disposed_on?: string;
}

function AssetsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();
  const mayManage = grants.can('asset.manage');

  const { data, isLoading, error, refetch } = useApiList<Asset>(
    scope ? '/assets' : null,
    { company_id: scope?.company_id },
  );

  // Where disposal proceeds land.
  const accounts = useApiList<MoneyAccount>(scope ? '/treasury/accounts' : null, {
    company_id: scope?.company_id,
  });

  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [disposing, setDisposing] = useState<Asset | null>(null);
  const [proceeds, setProceeds] = useState('');
  const [reason, setReason] = useState('');
  const [account, setAccount] = useState('');
  const [disposedOn, setDisposedOn] = useState('');

  const rows = data?.data ?? [];
  const owed = rows.reduce((n, a) => n + (a.months_due > 0 ? 1 : 0), 0);

  async function depreciate() {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      const out = await api.post<{ months_posted?: number; posted?: number }>(
        `/assets/depreciate?company_id=${scope.company_id}`,
        {},
      );
      const n = out.months_posted ?? out.posted ?? 0;
      // Nothing to charge is an outcome. Running it twice in a morning is safe
      // and should read as "already up to date", not as a failure.
      setNote(
        n > 0
          ? t('nx.ast.depreciated', { count: String(n) })
          : t('nx.ast.depreciatedNone'),
      );
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function dispose() {
    if (!scope || !disposing) return;
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await api.post(
        `/assets/${disposing.id}/dispose?company_id=${scope.company_id}`,
        { proceeds, money_account_id: account, disposed_on: disposedOn, note: reason },
      );
      setDisposing(null);
      setProceeds('');
      setReason('');
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Asset>[] = [
    {
      key: 'name',
      header: t('nx.ast.asset'),
      primary: true,
      cell: (a) => (
        <span className="flex flex-col">
          <span className="flex items-center gap-2">
            {a.name}
            {a.disposed_on ? (
              <Badge tone="neutral">{t('nx.ast.disposed')}</Badge>
            ) : null}
          </span>
          <span className="text-caption text-muted">
            <span className="num">{a.asset_no}</span> · {a.category}
            {a.store ? ` · ${a.store}` : ''}
          </span>
        </span>
      ),
    },
    {
      key: 'custodian',
      header: t('nx.ast.custodian'),
      secondary: true,
      cell: (a) => a.custodian ?? <span className="text-muted">—</span>,
    },
    {
      key: 'cost',
      header: t('nx.ast.cost'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      cell: (a) => (
        <span className="num text-muted">
          {formatMoney(a.cost, { currency: a.currency || currency, market })}
        </span>
      ),
    },
    {
      key: 'book',
      header: t('nx.ast.bookValue'),
      numeric: true,
      width: 'w-36',
      cell: (a) => (
        <span className="num font-medium">
          {formatMoney(a.book_value, { currency: a.currency || currency, market })}
        </span>
      ),
    },
    {
      key: 'monthly',
      header: t('nx.ast.monthly'),
      numeric: true,
      secondary: true,
      width: 'w-32',
      // The figure somebody sanity-checks the life against: "forty a month for
      // a laptop" reads wrong in a way "sixty months" does not.
      cell: (a) => (
        <span className="num text-muted">
          {formatMoney(a.monthly_charge, {
            currency: a.currency || currency,
            market,
            bare: true,
          })}
        </span>
      ),
    },
    {
      key: 'due',
      header: t('nx.ast.monthsDue'),
      numeric: true,
      width: 'w-32',
      cell: (a) =>
        a.months_due > 0 ? (
          <Badge tone="caution">
            {t('nx.ast.monthsCount', { count: String(a.months_due) })}
          </Badge>
        ) : (
          <span className="text-subtle">{t('nx.ast.upToDate')}</span>
        ),
    },
  ];

  if (mayManage) {
    columns.push({
      key: 'dispose',
      header: t('nx.ast.disposeHeader'),
      width: 'w-28',
      cell: (a) =>
        a.disposed_on ? (
          <time dateTime={a.disposed_on} className="text-muted">
            {a.disposed_on}
          </time>
        ) : (
          <Button size="sm" variant="ghost" onClick={() => setDisposing(a)}>
            {t('nx.ast.dispose')}
          </Button>
        ),
    });
  }

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader
        title={t('nx.ast.title')}
        description={t('nx.ast.subtitle')}
        actions={
          mayManage ? (
            <Button
              busy={busy}
              busyLabel={t('nx.ast.depreciating')}
              onClick={() => void depreciate()}
            >
              {t('nx.ast.runDepreciation')}
            </Button>
          ) : null
        }
      />

      <FormError message={actionError} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {owed > 0 ? (
        <Panel className="mb-4">
          <p className="text-body text-fg">
            {t('nx.ast.owedWarning', { count: String(owed) })}
          </p>
        </Panel>
      ) : null}

      {disposing ? (
        <Panel title={t('nx.ast.disposeTitle', { name: disposing.name })} className="mb-4">
          <p className="mb-4 text-body text-muted">{t('nx.ast.disposeExplain')}</p>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="proceeds"
              label={t('nx.ast.proceeds')}
              hint={t('nx.ast.proceedsHint')}
            >
              <Input
                inputMode="decimal"
                dir="ltr"
                value={proceeds}
                onChange={(e) => setProceeds(e.target.value)}
              />
            </Field>
            <Field name="disposed_on" label={t('nx.ast.disposedOn')}>
              <Input
                type="date"
                value={disposedOn}
                onChange={(e) => setDisposedOn(e.target.value)}
                required
              />
            </Field>
            {/* Where the proceeds landed. A money_account id, not the chart
                account id sitting beside it on the same treasury row. */}
            <Field
              name="money_account_id"
              label={t('nx.ast.account')}
              hint={t('nx.ast.accountHint')}
            >
              <Select value={account} onChange={(e) => setAccount(e.target.value)}>
                <option value="">{t('nx.ast.chooseAccount')}</option>
                {(accounts.data?.data ?? [])
                  .filter((a) => a.is_active)
                  .map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                    </option>
                  ))}
              </Select>
            </Field>
            <Field name="note" label={t('nx.ast.disposeReason')}>
              <Textarea
                rows={2}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </Field>
          </div>
          <div className="mt-6 flex flex-wrap gap-2">
            <Button busy={busy} busyLabel={t('nx.ast.saving')} onClick={() => void dispose()}>
              {t('nx.ast.confirmDispose')}
            </Button>
            <Button variant="ghost" onClick={() => setDisposing(null)}>
              {t('nx.ast.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {isLoading && !data ? <TableSkeleton columns={6} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={Boxes}
          title={t('nx.ast.emptyTitle')}
          description={t('nx.ast.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<Asset>
          rows={rows}
          columns={columns}
          rowKey={(a) => a.id}
          caption={t('nx.ast.caption')}
        />
      ) : null}
    </>
  );
}

export default function AssetsPage() {
  return (
    <RequirePermission anyOf={['asset.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <AssetsScreen />
      </Suspense>
    </RequirePermission>
  );
}
