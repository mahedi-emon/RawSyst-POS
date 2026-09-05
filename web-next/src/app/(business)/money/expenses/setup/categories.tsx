'use client';

// Expense categories, and the one field on them that is a tax position.
//
// # `input_vat_recoverable` is the reason this screen is not a list of labels
//
// Every other field here names something. That one decides, for every expense
// booked under the category from now on, whether the VAT goes to Input VAT
// Receivable or is absorbed into the cost. E2.3 restricts recovery BY CATEGORY
// — entertainment, some vehicles, fuel — all of it or none of it, never
// apportioned within one.
//
// So it is asked as a question with two full answers rather than offered as a
// checkbox. The API refuses a request that omits it, and says why: "Defaulting
// either way is wrong: false silently stops a shop reclaiming VAT it is
// entitled to, true silently claims VAT on entertainment." A checkbox has a
// default. A select with an empty first option does not.
//
// # Nothing is deleted
//
// A category that has been spent against is part of the history of every
// expense booked to it. `POST .../active` retires it, and a retired category
// simply stops being offered when somebody records an expense.

import { Tags } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { ExpenseHead, LedgerAccount } from '@/lib/money/expenses';
import { useUrlFlag, useUrlState } from '@/lib/url-state';

export function Categories() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const [retired, setRetired] = useUrlFlag('retired');
  const [editing, setEditing] = useUrlState('head');
  const [creating, setCreating] = useUrlFlag('newHead');

  const { data, isLoading, error, refetch } = useApiList<ExpenseHead>(
    scope ? '/expenses/heads' : null,
    scope ? { ...scope, include_retired: retired ? 'true' : undefined } : undefined,
  );
  // Behind `expense.manage_heads`, the same permission as this whole screen,
  // so it is fetched here rather than in the form: opening the editor should
  // not be the first moment the chart of accounts is asked for.
  const accounts = useApiList<LedgerAccount>(
    scope ? '/expenses/accounts' : null,
    scope ?? undefined,
  );

  const rows = data?.data ?? [];
  const open = creating ? null : (rows.find((h) => h.id === editing) ?? null);
  const showForm = creating || (editing !== '' && open !== null);

  function close() {
    setCreating(false);
    setEditing('');
  }

  const columns: Column<ExpenseHead>[] = [
    {
      key: 'code',
      header: t('nx.expcfg.colCode'),
      width: 'w-28',
      cell: (h) => <span className="num text-muted">{h.code}</span>,
    },
    {
      key: 'name',
      header: t('nx.expcfg.colName'),
      primary: true,
      cell: (h) => (
        <span className="flex items-center gap-2">
          {h.name}
          {!h.is_active ? <Badge>{t('nx.expcfg.retired')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'account',
      header: t('nx.expcfg.colAccount'),
      secondary: true,
      cell: (h) => (
        <span className="text-muted">
          <span className="num">{h.account_code}</span> {h.account_name}
        </span>
      ),
    },
    {
      key: 'vat',
      header: t('nx.expcfg.colVat'),
      width: 'w-40',
      // The column the screen exists for. A word, not a tick: "reclaimable"
      // and an empty cell look the same at a glance, and the difference
      // between them is what the VAT return claims.
      cell: (h) =>
        h.input_vat_recoverable ? (
          <span className="text-muted">{t('nx.expcfg.vatYes')}</span>
        ) : (
          <Badge tone="caution">{t('nx.expcfg.vatNo')}</Badge>
        ),
    },
    {
      key: 'spent',
      header: t('nx.expcfg.colSpent'),
      numeric: true,
      width: 'w-36',
      cell: (h) =>
        isZero(h.spent) ? (
          <span className="text-subtle">—</span>
        ) : (
          <span className="num">
            {formatMoney(h.spent, { currency: h.currency || currency, market })}
          </span>
        ),
    },
  ];

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_23rem]">
      <div className="min-w-0">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <Checkbox
            label={t('nx.expcfg.showRetired')}
            checked={retired}
            onChange={(e) => setRetired(e.target.checked)}
          />
          <Button
            variant="primary"
            size="sm"
            onClick={() => {
              setEditing('');
              setCreating(true);
            }}
          >
            {t('nx.expcfg.newHead')}
          </Button>
        </div>

        {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
        {isLoading && !data ? <TableSkeleton columns={5} /> : null}

        {!isLoading && !error && rows.length === 0 ? (
          <EmptyState
            icon={Tags}
            title={t('nx.expcfg.headsEmptyTitle')}
            description={t('nx.expcfg.headsEmptyDesc')}
          />
        ) : null}

        {rows.length > 0 ? (
          <DataTable
            caption={t('nx.expcfg.headsCaption')}
            columns={columns}
            rows={rows}
            rowKey={(h) => h.id}
            isSelected={(h) => h.id === open?.id}
            onOpenRow={(h) => {
              setCreating(false);
              setEditing(h.id);
            }}
          />
        ) : null}

        <p className="mt-3 max-w-prose text-caption text-muted">
          {t('nx.expcfg.noDelete')}
        </p>
      </div>

      {showForm ? (
        <CategoryForm
          key={open?.id ?? 'new'}
          head={open}
          accounts={accounts.data?.data ?? []}
          onSaved={() => {
            void refetch();
            close();
          }}
          onRetired={() => {
            void refetch();
            close();
          }}
          onCancel={close}
        />
      ) : null}
    </div>
  );
}

function CategoryForm({
  head,
  accounts,
  onSaved,
  onRetired,
  onCancel,
}: {
  head: ExpenseHead | null;
  accounts: readonly LedgerAccount[];
  onSaved: () => void;
  onRetired: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  const [code, setCode] = useState(head?.code ?? '');
  const [name, setName] = useState(head?.name ?? '');
  const [nameAr, setNameAr] = useState(head?.name_ar ?? '');
  const [accountID, setAccountID] = useState(head?.account_id ?? '');
  // A string with three states, not a boolean with two. Empty is "nobody has
  // decided", which is exactly what the API refuses — and a checkbox cannot
  // hold it.
  const [recoverable, setRecoverable] = useState(
    head ? String(head.input_vat_recoverable) : '',
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    const body = {
      code,
      name,
      name_ar: nameAr,
      account_id: accountID,
      // Sent only once decided. Sending `false` for "not yet answered" is the
      // silent wrong answer the API exists to refuse.
      input_vat_recoverable: recoverable === '' ? undefined : recoverable === 'true',
    };
    try {
      if (head) {
        await api.put(
          `/expenses/heads/${head.id}?company_id=${scope.company_id}`,
          body,
        );
      } else {
        await api.post(`/expenses/heads?company_id=${scope.company_id}`, body);
      }
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  async function setActive(active: boolean) {
    if (!scope || !head) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/expenses/heads/${head.id}/active?company_id=${scope.company_id}`,
        { active },
      );
      onRetired();
    } catch (e) {
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <Panel title={head ? t('nx.expcfg.editHead') : t('nx.expcfg.newHead')}>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <FormError message={error} />

        <Field
          label={t('nx.expcfg.headCode')}
          hint={t('nx.expcfg.headCodeHint')}
          error={fieldErrors.code}
          required={!head}
        >
          <Input
            value={code}
            onChange={(e) => setCode(e.target.value)}
            // The update statement does not touch the code. Disabled rather
            // than accepted and ignored, which reads as a save that did not
            // stick.
            disabled={Boolean(head)}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        <Field label={t('nx.expcfg.headName')} error={fieldErrors.name} required>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus={!head}
          />
        </Field>

        <Field label={t('nx.expcfg.nameAr')} hint={t('nx.expcfg.nameArHint')}>
          <Input
            value={nameAr}
            onChange={(e) => setNameAr(e.target.value)}
            dir="rtl"
            lang="ar"
          />
        </Field>

        <Field
          label={t('nx.expcfg.account')}
          hint={t('nx.expcfg.accountHint')}
          error={fieldErrors.account_id}
          required
        >
          <Select value={accountID} onChange={(e) => setAccountID(e.target.value)}>
            <option value="">{t('nx.expcfg.chooseAccount')}</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.code} — {a.name}
              </option>
            ))}
          </Select>
        </Field>

        <Field
          label={t('nx.expcfg.vatQuestion')}
          hint={head ? t('nx.expcfg.vatChangeHint') : t('nx.expcfg.vatHint')}
          error={fieldErrors.input_vat_recoverable}
          required
        >
          <Select
            value={recoverable}
            onChange={(e) => setRecoverable(e.target.value)}
          >
            <option value="">{t('nx.expcfg.vatChoose')}</option>
            <option value="true">{t('nx.expcfg.vatOptionYes')}</option>
            <option value="false">{t('nx.expcfg.vatOptionNo')}</option>
          </Select>
        </Field>

        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <Button type="submit" variant="primary" busy={busy}>
            {t('nx.expcfg.save')}
          </Button>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.expcfg.cancel')}
          </Button>
          {head ? (
            <Button
              variant="ghost"
              className="ms-auto"
              disabled={busy}
              onClick={() => void setActive(!head.is_active)}
            >
              {head.is_active ? t('nx.expcfg.retire') : t('nx.expcfg.restore')}
            </Button>
          ) : null}
        </div>
      </form>
    </Panel>
  );
}
