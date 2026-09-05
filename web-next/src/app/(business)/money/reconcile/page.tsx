'use client';

// Bank reconciliation.
//
// C11 opens with the sentence the whole module serves: "Proves that what the
// software says is in the bank is actually what the bank says."
//
// # Two columns that look alike and are not
//
// "Bank says" is the closing balance on the statement. "Books say" is what the
// ledger holds for that account on the same date. They are almost never equal,
// and the gap is not an error — it is cheques that have not cleared and charges
// nobody has keyed. What matters is the third figure: what is left once both
// lists of exceptions are taken off. That is `difference`, and it is the only
// one worth reading first.
//
// # Signed off and balancing are different questions
//
// `status` is whether a person put their name to it. `difference` is recomputed
// from today's books every time the row is read, so a statement signed off in
// March can show a difference in April if something on the account has since
// changed. The badge comes from the first and the figure from the second, and
// conflating them would either hide a real change or claim a sign-off that
// never happened.

import { Landmark } from 'lucide-react';
import { useRouter } from 'next/navigation';
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
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { isBankLike, type MoneyAccount } from '@/lib/money/money-accounts';
import {
  addsUp,
  closesAt,
  isSignedOff,
  parseStatement,
  type Statement,
} from '@/lib/money/statement';
import { useUrlFlag } from '@/lib/url-state';

function ReconcileScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const [importing, setImporting] = useUrlFlag('bring');

  const { data, isLoading, error, refetch } = useApiList<Statement>(
    scope ? '/treasury/statements' : null,
    scope ?? undefined,
  );
  const accounts = useApiList<MoneyAccount>(
    scope ? '/treasury/accounts' : null,
    scope ?? undefined,
  );

  const rows = data?.data ?? [];
  const money = (v: string, c?: string) =>
    formatMoney(v, { currency: c || currency, market });

  const columns: Column<Statement>[] = [
    {
      key: 'account',
      header: t('nx.rec.colAccount'),
      primary: true,
      cell: (s) => (
        <span className="flex flex-col gap-0.5">
          <span className="flex items-center gap-2">
            {s.account}
            {isSignedOff(s) ? (
              <Badge tone="positive">{t('nx.rec.statusSigned')}</Badge>
            ) : (
              <Badge>{t('nx.rec.statusDraft')}</Badge>
            )}
          </span>
          {s.reference ? (
            <span className="num text-caption text-muted">{s.reference}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'period',
      header: t('nx.rec.colPeriod'),
      secondary: true,
      width: 'w-52',
      cell: (s) => (
        <span className="num text-muted">
          <time dateTime={s.starts_on}>{s.starts_on}</time>
          {' – '}
          <time dateTime={s.ends_on}>{s.ends_on}</time>
        </span>
      ),
    },
    {
      key: 'closing',
      header: t('nx.rec.colClosing'),
      numeric: true,
      width: 'w-36',
      cell: (s) => <span className="num">{money(s.closing_balance, s.currency)}</span>,
    },
    {
      key: 'books',
      header: t('nx.rec.colBooks'),
      numeric: true,
      secondary: true,
      width: 'w-36',
      cell: (s) => (
        <span className="num text-muted">{money(s.ledger_balance, s.currency)}</span>
      ),
    },
    {
      key: 'difference',
      header: t('nx.rec.colDifference'),
      numeric: true,
      width: 'w-36',
      // The figure the exercise is about. Zero is a word rather than 0.00,
      // because "explained" and "nothing happened" are different readings of
      // the same number and only one of them is right here.
      cell: (s) =>
        isZero(s.difference) ? (
          <span className="text-caption text-positive-fg">{t('nx.rec.explained')}</span>
        ) : (
          <span className="num font-medium text-caution-fg">
            {money(s.difference, s.currency)}
          </span>
        ),
    },
  ];

  // The same three kinds that can carry bank detail are the three that have a
  // statement, and the service says so in its refusal: "Only a bank,
  // card-settlement or gateway account has a statement to reconcile against."
  // One predicate rather than two lists that could drift apart.
  const bankAccounts = (accounts.data?.data ?? []).filter(
    (a) => a.is_active && isBankLike(a.kind),
  );

  return (
    <>
      <PageHeader
        title={t('nx.rec.title')}
        description={t('nx.rec.subtitle')}
        actions={
          <Button variant="primary" onClick={() => setImporting(true)}>
            {t('nx.rec.bring')}
          </Button>
        }
      />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_25rem]">
        <div className="min-w-0">
          {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
          {isLoading && !data ? <TableSkeleton columns={5} /> : null}

          {!isLoading && !error && rows.length === 0 ? (
            <EmptyState
              icon={Landmark}
              title={t('nx.rec.emptyTitle')}
              description={t('nx.rec.emptyDesc')}
            />
          ) : null}

          {rows.length > 0 ? (
            <DataTable
              caption={t('nx.rec.caption')}
              columns={columns}
              rows={rows}
              rowKey={(s) => s.id}
              onOpenRow={(s) => router.push(`/money/reconcile/${s.id}`)}
            />
          ) : null}
        </div>

        {importing ? (
          <ImportForm
            accounts={bankAccounts}
            onImported={(id) => {
              void refetch();
              setImporting(false);
              router.push(`/money/reconcile/${id}`);
            }}
            onCancel={() => setImporting(false)}
          />
        ) : null}
      </div>
    </>
  );
}

function ImportForm({
  accounts,
  onImported,
  onCancel,
}: {
  accounts: readonly MoneyAccount[];
  onImported: (id: string) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const [accountID, setAccountID] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [opening, setOpening] = useState('');
  const [closing, setClosing] = useState('');
  const [reference, setReference] = useState('');
  const [pasted, setPasted] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const { lines, problems } = parseStatement(pasted);
  const account = accounts.find((a) => a.id === accountID);
  const money = (v: string) =>
    formatMoney(v, { currency: account?.currency || currency, market });

  // Checked here as well as at the server because it is the mistake a paste
  // makes -- a row short -- and the refusal names no row. Saying where the
  // lines land, beside the figure they should reach, turns a rejected import
  // into a visible arithmetic problem.
  const lands = closesAt(opening, lines);
  const balances = addsUp(opening, lines, closing);

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      const out = await api.post<Statement>(
        `/treasury/statements?company_id=${scope.company_id}`,
        {
          account_id: accountID,
          starts_on: from,
          ends_on: to,
          opening_balance: opening,
          closing_balance: closing,
          reference,
          lines,
        },
      );
      onImported(out.id);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <Panel title={t('nx.rec.importTitle')}>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <FormError message={error} fields={fieldErrors} />

        <Field
          name="account_id"
          label={t('nx.rec.account')}
          hint={t('nx.rec.accountHint')}
          error={fieldErrors.account_id}
          required
        >
          <Select
            value={accountID}
            onChange={(e) => setAccountID(e.target.value)}
            disabled={accounts.length === 0}
          >
            <option value="">{t('nx.rec.chooseAccount')}</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </Select>
        </Field>

        {accounts.length === 0 ? (
          <p className="text-caption text-caution-fg">{t('nx.rec.noBankAccount')}</p>
        ) : null}

        <div className="grid gap-4 sm:grid-cols-2">
          <Field name="starts_on" label={t('nx.rec.from')} error={fieldErrors.starts_on} required>
            <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
          </Field>
          <Field name="ends_on" label={t('nx.rec.to')} error={fieldErrors.ends_on} required>
            <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
          </Field>
          <Field
            name="opening_balance"
            label={t('nx.rec.opening')}
            error={fieldErrors.opening_balance}
            required
          >
            <Input
              value={opening}
              onChange={(e) => setOpening(e.target.value)}
              inputMode="decimal"
              autoComplete="off"
            />
          </Field>
          <Field
            name="closing_balance"
            label={t('nx.rec.closing')}
            error={fieldErrors.closing_balance}
            required
          >
            <Input
              value={closing}
              onChange={(e) => setClosing(e.target.value)}
              inputMode="decimal"
              autoComplete="off"
            />
          </Field>
        </div>

        <Field label={t('nx.rec.reference')}>
          <Input
            value={reference}
            onChange={(e) => setReference(e.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        <Field label={t('nx.rec.paste')} hint={t('nx.rec.pasteHint')} required>
          <Textarea
            value={pasted}
            onChange={(e) => setPasted(e.target.value)}
            rows={8}
            spellCheck={false}
            className="font-mono text-caption"
          />
        </Field>

        {lines.length > 0 || problems.length > 0 ? (
          <div className="flex flex-col gap-2 rounded-sm border border-line bg-surface-sunken px-3 py-2">
            <p className="text-caption text-muted">
              {t('nx.rec.readRows', { count: lines.length })}
              {problems.length > 0
                ? ` · ${t('nx.rec.rowsUnread', { count: problems.length })}`
                : ''}
            </p>

            {problems.length > 0 ? (
              <ul className="flex flex-col gap-0.5">
                {/* Named row by row. A paste of two hundred lines with one bad
                    date is one correction, not a retype. */}
                {problems.slice(0, 6).map((p) => (
                  <li key={p.row} className="text-caption text-critical-fg">
                    {t(p.key, { row: p.row, text: p.text })}
                  </li>
                ))}
              </ul>
            ) : null}

            {lines.length > 0 ? (
              <p className="text-caption">
                <span className="text-muted">
                  {t('nx.rec.landsAt', { amount: money(lands) })}
                </span>
                {closing.trim() !== '' ? (
                  <span
                    className={
                      balances
                        ? ' block text-positive-fg'
                        : ' block text-caution-fg'
                    }
                  >
                    {balances ? t('nx.rec.addsUp') : t('nx.rec.doesNotAddUp')}
                  </span>
                ) : null}
              </p>
            ) : null}
          </div>
        ) : null}

        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <Button
            type="submit"
            variant="primary"
            busy={busy}
            // Refused by the server anyway; disabled here so the refusal is
            // not the way somebody discovers their paste was short.
            disabled={!balances || accountID === '' || from === '' || to === ''}
          >
            {t('nx.rec.import')}
          </Button>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.rec.cancel')}
          </Button>
        </div>

        <p className="text-caption text-muted">{t('nx.rec.importHint')}</p>
      </form>
    </Panel>
  );
}

export default function ReconcilePage() {
  return (
    <RequirePermission anyOf={['accounting.reconcile']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ReconcileScreen />
      </Suspense>
    </RequirePermission>
  );
}
