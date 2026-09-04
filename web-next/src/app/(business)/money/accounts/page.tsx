'use client';

// Every place the business keeps money.
//
// # A till has no IBAN
//
// Five kinds, and only three of them can carry bank detail — the same three the
// schema allows it on. The form follows that rather than showing every field
// for every kind, because a form that asks for a fact that does not exist is a
// form somebody fills in wrongly.
//
// # Unmatched statement lines belong on this list
//
// `unreconciled` is the count of statement lines nobody has tied to a
// transaction. It is on the account rather than buried in a reconciliation
// screen, because it is the thing that makes somebody open that screen.
//
// # Closing an account moves nothing
//
// The balance and every past transaction stay exactly as they are. Saying so is
// the difference between tidying a picker and somebody thinking they have just
// lost a bank account.

import { Landmark } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { LedgerAccount } from '@/lib/money/expenses';
import {
  ACCOUNT_KIND,
  ACCOUNT_KINDS,
  isBankLike,
  type MoneyAccount,
} from '@/lib/money/money-accounts';
import { useUrlFlag, useUrlState } from '@/lib/url-state';

function AccountsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApiList<MoneyAccount>(
    scope ? '/treasury/accounts' : null,
    scope ?? undefined,
  );
  // The chart of accounts, so a new account can say where it posts.
  const ledger = useApiList<LedgerAccount>(
    scope && grants.can('accounting.manage_accounts') ? '/expenses/accounts' : null,
    scope ?? undefined,
  );

  const [editing, setEditing] = useUrlState('edit');
  const [creating, setCreating] = useUrlFlag('new');

  const rows = data?.data ?? [];
  const open = creating ? null : (rows.find((a) => a.id === editing) ?? null);
  const mayManage = grants.can('accounting.manage_accounts');
  const showForm = mayManage && (creating || (editing !== '' && open !== null));

  const columns: Column<MoneyAccount>[] = [
    {
      key: 'name',
      header: t('nx.acc.colName'),
      primary: true,
      cell: (a) => (
        <span className="flex flex-wrap items-center gap-2">
          {a.name}
          {!a.is_active ? <Badge tone="neutral">{t('nx.acc.inactive')}</Badge> : null}
          {/* The thing that makes somebody open the reconciliation screen. */}
          {a.unreconciled ? (
            <Badge tone="caution">
              {t('nx.acc.unreconciled', { count: a.unreconciled })}
            </Badge>
          ) : null}
        </span>
      ),
    },
    {
      key: 'kind',
      header: t('nx.acc.colKind'),
      width: 'w-44',
      cell: (a) => {
        const named = ACCOUNT_KIND[a.kind];
        return named ? t(named.key) : <span className="num">{a.kind}</span>;
      },
    },
    {
      key: 'where',
      header: t('nx.acc.colWhere'),
      secondary: true,
      cell: (a) => <span className="text-muted">{a.store || '—'}</span>,
    },
    {
      key: 'balance',
      header: t('nx.acc.colBalance'),
      numeric: true,
      width: 'w-40',
      cell: (a) => (
        <span className="num font-medium">
          {formatMoney(a.balance, { currency: a.currency || currency, market })}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.acc.title')}
        description={t('nx.acc.subtitle')}
        actions={
          mayManage ? (
            <Button
              variant="primary"
              onClick={() => {
                setEditing('');
                setCreating(true);
              }}
            >
              {t('nx.acc.add')}
            </Button>
          ) : null
        }
      />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && rows.length === 0 ? <TableSkeleton columns={4} /> : null}

      {!isLoading && !error && rows.length === 0 && !showForm ? (
        <EmptyState
          icon={Landmark}
          title={t('nx.acc.emptyTitle')}
          description={t('nx.acc.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 || showForm ? (
        <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_22rem]">
          <div className="min-w-0">
            {rows.length > 0 ? (
              <>
                <DataTable
                  caption={t('nx.acc.caption')}
                  columns={columns}
                  rows={rows}
                  rowKey={(a) => a.id}
                  onOpenRow={
                    mayManage
                      ? (a) => {
                          setCreating(false);
                          setEditing(a.id);
                        }
                      : undefined
                  }
                />
                {rows.some((a) => a.unreconciled) ? (
                  <p className="mt-3 text-caption text-muted">
                    {t('nx.acc.unreconciledHint')}
                  </p>
                ) : null}
              </>
            ) : null}
          </div>

          {showForm ? (
            <AccountForm
              key={open?.id ?? 'new'}
              account={open}
              ledger={ledger.data?.data ?? []}
              defaultCurrency={currency}
              onSaved={() => {
                void refetch();
                setCreating(false);
                setEditing('');
              }}
              onCancel={() => {
                setCreating(false);
                setEditing('');
              }}
            />
          ) : null}
        </div>
      ) : null}
    </>
  );
}

function AccountForm({
  account,
  ledger,
  defaultCurrency,
  onSaved,
  onCancel,
}: {
  account: MoneyAccount | null;
  ledger: LedgerAccount[];
  defaultCurrency: string;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  const [kind, setKind] = useState(account?.kind ?? 'bank');
  const [name, setName] = useState(account?.name ?? '');
  const [nameAr, setNameAr] = useState(account?.name_ar ?? '');
  const [currency, setCurrency] = useState(account?.currency ?? defaultCurrency);
  const [accountId, setAccountId] = useState(account?.account_id ?? '');
  const [bankName, setBankName] = useState(account?.bank_name ?? '');
  const [number, setNumber] = useState(account?.account_number ?? '');
  const [iban, setIban] = useState(account?.iban ?? '');
  const [swift, setSwift] = useState(account?.swift ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const bankLike = isBankLike(kind);

  async function save() {
    if (!scope) return;
    setError(null);
    setFieldErrors({});
    if (name.trim() === '') return setError(t('nx.acc.needName'));
    if (accountId === '') return setError(t('nx.acc.needLedger'));

    setBusy(true);
    try {
      await api.post(`/treasury/accounts?company_id=${scope.company_id}`, {
        kind,
        name,
        name_ar: nameAr,
        currency,
        account_id: accountId,
        // Sent only for the kinds that can carry them, so a till never
        // acquires an empty IBAN it did not ask for.
        bank_name: bankLike ? bankName : '',
        account_number: bankLike ? number : '',
        iban: bankLike ? iban : '',
        swift: bankLike ? swift : '',
      });
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function setActive(active: boolean) {
    if (!scope || !account) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/treasury/accounts/${account.id}/active?company_id=${scope.company_id}`,
        { is_active: active },
      );
      onSaved();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel
      title={account ? t('nx.acc.editTitle', { name: account.name }) : t('nx.acc.newTitle')}
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <FormError message={error} />

        <Field label={t('nx.acc.fKind')} error={fieldErrors.kind} required>
          <Select value={kind} onChange={(e) => setKind(e.target.value)}>
            {ACCOUNT_KINDS.map((k) => (
              <option key={k} value={k}>
                {t(ACCOUNT_KIND[k]!.key)}
              </option>
            ))}
          </Select>
        </Field>

        <Field label={t('nx.acc.fName')} error={fieldErrors.name} required>
          <Input value={name} onChange={(e) => setName(e.target.value)} autoFocus={!account} />
        </Field>

        <Field label={t('nx.acc.fNameAr')}>
          <Input value={nameAr} onChange={(e) => setNameAr(e.target.value)} dir="rtl" lang="ar" />
        </Field>

        <Field label={t('nx.acc.fCurrency')} error={fieldErrors.currency}>
          <Input
            value={currency}
            onChange={(e) => setCurrency(e.target.value.toUpperCase())}
            maxLength={3}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        <Field
          label={t('nx.acc.fLedger')}
          hint={t('nx.acc.fLedgerHint')}
          error={fieldErrors.account_id}
          required
        >
          <Select value={accountId} onChange={(e) => setAccountId(e.target.value)}>
            <option value="">{t('nx.exp.chooseHead')}</option>
            {ledger.map((a) => (
              <option key={a.id} value={a.id}>
                {`${a.code} · ${a.name}`}
              </option>
            ))}
          </Select>
        </Field>

        {bankLike ? (
          <>
            <p className="text-caption text-muted">{t('nx.acc.bankOnly')}</p>
            <Field label={t('nx.acc.fBank')}>
              <Input value={bankName} onChange={(e) => setBankName(e.target.value)} />
            </Field>
            <Field label={t('nx.acc.fNumber')}>
              <Input
                value={number}
                onChange={(e) => setNumber(e.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label={t('nx.acc.fIban')} error={fieldErrors.iban}>
              <Input
                value={iban}
                onChange={(e) => setIban(e.target.value.toUpperCase())}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label={t('nx.acc.fSwift')}>
              <Input
                value={swift}
                onChange={(e) => setSwift(e.target.value.toUpperCase())}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
          </>
        ) : null}

        <div className="flex flex-wrap gap-2">
          <Button type="submit" variant="primary" busy={busy}>
            {account ? t('nx.acc.saveChanges') : t('nx.acc.save')}
          </Button>
          <Button type="button" variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.acc.cancel')}
          </Button>
        </div>

        {account ? (
          <div className="border-t border-line pt-4">
            <p className="text-caption text-muted">{t('nx.acc.closeHint')}</p>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="mt-2"
              disabled={busy}
              onClick={() => void setActive(!account.is_active)}
            >
              {account.is_active ? t('nx.acc.close') : t('nx.acc.reopen')}
            </Button>
          </div>
        ) : null}
      </form>
    </Panel>
  );
}

export default function AccountsPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <AccountsScreen />
      </Suspense>
    </RequirePermission>
  );
}
