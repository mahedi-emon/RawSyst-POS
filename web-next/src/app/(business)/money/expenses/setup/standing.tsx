'use client';

// Costs a shop agrees once and then owes every month.
//
// # A schedule posts nothing
//
// It describes an expense and says when the next one is due. Booking it calls
// the same `Record` path a person typing one takes, so the tax treatment, the
// posting rules, the numbering and the audit record are the ones expenses
// already have. That is also why booking sits behind `expense.record` rather
// than the permission that configures this screen: somebody who may not record
// an expense must not be able to make a schedule do it for them.
//
// # Quarterly is monthly, three at a time
//
// The API allows `weekly`, `monthly` and `yearly`, and says what to do with
// everything else in its own refusal: "Use interval_count for anything else:
// monthly every 3 is quarterly." Nobody signs a lease "monthly, every three",
// so the form offers the cadences a business actually agrees to and sends the
// pair each one means.
//
// # Running it twice must not pay the rent twice
//
// The guard is a unique index on (schedule, due date) in the database, not a
// check the client performs — so the button is safe to press again, and says
// so rather than being disabled out of caution.

import { CalendarClock } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  PRESETS,
  describeCadence,
  isDue,
  type Department,
  type ExpenseHead,
  type GenerateResult,
  type Recurring,
} from '@/lib/money/expenses';
import { useUrlFlag } from '@/lib/url-state';

interface Supplier {
  id: string;
  legal_name: string;
  is_active: boolean;
}

function today(): string {
  return new Date().toISOString().slice(0, 10);
}

export function Standing() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const [creating, setCreating] = useUrlFlag('newStanding');

  const { data, isLoading, error, refetch } = useApiList<Recurring>(
    scope ? '/expenses/recurring' : null,
    scope ? { ...scope, include_inactive: 'true' } : undefined,
  );

  const heads = useApiList<ExpenseHead>(
    scope ? '/expenses/heads' : null,
    scope ?? undefined,
  );
  const departments = useApiList<Department>(
    scope ? '/expenses/departments' : null,
    scope ?? undefined,
  );
  // Only asked for by somebody who can read suppliers. A schedule is perfectly
  // valid without one, and a request that is going to answer 403 is a refused
  // request in the audit log rather than a missing field.
  const suppliers = useApiList<Supplier>(
    scope && grants.can('purchasing.view') ? '/purchasing/suppliers' : null,
    scope ? { ...scope, limit: 200 } : undefined,
  );

  const [busyID, setBusyID] = useState<string | null>(null);
  const [rowError, setRowError] = useState<string | null>(null);
  const [generating, setGenerating] = useState(false);
  const [result, setResult] = useState<GenerateResult | null>(null);

  const rows = data?.data ?? [];
  const now = today();
  const dueCount = rows.filter((r) => isDue(r, now)).length;

  async function setActive(row: Recurring, active: boolean) {
    if (!scope) return;
    setBusyID(row.id);
    setRowError(null);
    try {
      await api.post(
        `/expenses/recurring/${row.id}/active?company_id=${scope.company_id}`,
        { active },
      );
      await refetch();
    } catch (e) {
      setRowError(messageFor(e, t));
    } finally {
      setBusyID(null);
    }
  }

  async function generate() {
    if (!scope) return;
    setGenerating(true);
    setRowError(null);
    setResult(null);
    try {
      const out = await api.post<GenerateResult>(
        `/expenses/recurring/generate?company_id=${scope.company_id}`,
        {},
      );
      setResult(out);
      await refetch();
    } catch (e) {
      setRowError(messageFor(e, t));
    } finally {
      setGenerating(false);
    }
  }

  const columns: Column<Recurring>[] = [
    {
      key: 'name',
      header: t('nx.expcfg.colWhat'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="flex items-center gap-2">
            {r.name}
            {!r.is_active ? <Badge>{t('nx.expcfg.paused')}</Badge> : null}
          </span>
          {r.head ? <span className="text-caption text-muted">{r.head}</span> : null}
        </span>
      ),
    },
    {
      key: 'every',
      header: t('nx.expcfg.colEvery'),
      secondary: true,
      width: 'w-40',
      cell: (r) => <span className="text-muted">{describeCadence(r, t)}</span>,
    },
    {
      key: 'amount',
      header: t('nx.expcfg.colAmount'),
      numeric: true,
      width: 'w-36',
      cell: (r) => (
        <span className="num font-medium">
          {formatMoney(r.amount, { currency: r.currency || currency, market })}
        </span>
      ),
    },
    {
      key: 'due',
      header: t('nx.expcfg.colNextDue'),
      width: 'w-40',
      cell: (r) => (
        <span className="flex items-center gap-2">
          <time dateTime={r.next_due_on} className="num text-muted">
            {r.next_due_on}
          </time>
          {/* The state that decides whether pressing the button does
              anything. Said on the row rather than only in a total. */}
          {isDue(r, now) ? (
            <Badge tone="caution">{t('nx.expcfg.dueNow')}</Badge>
          ) : null}
        </span>
      ),
    },
    {
      key: 'actions',
      header: <span className="sr-only">{t('nx.expcfg.actions')}</span>,
      width: 'w-28',
      cell: (r) => (
        <Button
          variant="ghost"
          size="sm"
          busy={busyID === r.id}
          onClick={() => void setActive(r, !r.is_active)}
        >
          {r.is_active ? t('nx.expcfg.pause') : t('nx.expcfg.resume')}
        </Button>
      ),
    },
  ];

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_23rem]">
      <div className="min-w-0">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2">
            {grants.can('expense.record') ? (
              <Button
                busy={generating}
                disabled={dueCount === 0}
                onClick={() => void generate()}
              >
                {t('nx.expcfg.generate')}
              </Button>
            ) : null}
          </div>
          <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
            {t('nx.expcfg.newStanding')}
          </Button>
        </div>

        <FormError message={rowError} className="mb-4" />

        {/* Announced, because somebody who has just pressed the button is
            looking at it and not at the table underneath. */}
        {result ? (
          <p
            role="status"
            className="mb-4 rounded-sm border border-line bg-surface-sunken px-3 py-2 text-body"
          >
            {t('nx.expcfg.generated', {
              created: result.created,
              skipped: result.skipped,
            })}
            {result.failed?.length ? (
              <span className="mt-1 block text-caption text-caution-fg">
                {t('nx.expcfg.generateFailed', { list: result.failed.join('; ') })}
              </span>
            ) : null}
          </p>
        ) : null}

        {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
        {isLoading && !data ? <TableSkeleton columns={5} /> : null}

        {!isLoading && !error && rows.length === 0 ? (
          <EmptyState
            icon={CalendarClock}
            title={t('nx.expcfg.standingEmptyTitle')}
            description={t('nx.expcfg.standingEmptyDesc')}
          />
        ) : null}

        {rows.length > 0 ? (
          <DataTable
            caption={t('nx.expcfg.standingCaption')}
            columns={columns}
            rows={rows}
            rowKey={(r) => r.id}
          />
        ) : null}

        <p className="mt-3 max-w-prose text-caption text-muted">
          {grants.can('expense.record')
            ? t('nx.expcfg.generateHint')
            : t('nx.expcfg.postsNothing')}
        </p>
      </div>

      {creating ? (
        <StandingForm
          heads={(heads.data?.data ?? []).filter((h) => h.is_active)}
          departments={(departments.data?.data ?? []).filter((d) => d.is_active)}
          suppliers={(suppliers.data?.data ?? []).filter((s) => s.is_active)}
          onSaved={() => {
            void refetch();
            setCreating(false);
          }}
          onCancel={() => setCreating(false)}
        />
      ) : null}
    </div>
  );
}

function StandingForm({
  heads,
  departments,
  suppliers,
  onSaved,
  onCancel,
}: {
  heads: readonly ExpenseHead[];
  departments: readonly Department[];
  suppliers: readonly Supplier[];
  onSaved: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  const [name, setName] = useState('');
  const [headID, setHeadID] = useState('');
  const [departmentID, setDepartmentID] = useState('');
  const [supplierID, setSupplierID] = useState('');
  const [amount, setAmount] = useState('');
  const [paidFrom, setPaidFrom] = useState('');
  const [preset, setPreset] = useState('monthly');
  const [startsOn, setStartsOn] = useState(today);
  const [endsOn, setEndsOn] = useState('');
  const [description, setDescription] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const head = heads.find((h) => h.id === headID);

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    const cadence = PRESETS.find((p) => p.id === preset)?.cadence;
    try {
      await api.post(`/expenses/recurring?company_id=${scope.company_id}`, {
        name,
        head_id: headID,
        department_id: departmentID || undefined,
        supplier_id: supplierID || undefined,
        // A decimal STRING, straight from the field. Nothing here parses it.
        amount,
        paid_from: paidFrom,
        description,
        frequency: cadence?.frequency,
        interval_count: cadence?.interval_count,
        starts_on: startsOn,
        ends_on: endsOn || undefined,
      });
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <Panel title={t('nx.expcfg.newStanding')}>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <FormError message={error} />

        <Field
          label={t('nx.expcfg.standingName')}
          hint={t('nx.expcfg.standingNameHint')}
          error={fieldErrors.name}
          required
        >
          <Input value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </Field>

        <Field label={t('nx.exp.head')} error={fieldErrors.head_id} required>
          <Select value={headID} onChange={(e) => setHeadID(e.target.value)}>
            <option value="">{t('nx.exp.chooseHead')}</option>
            {heads.map((h) => (
              <option key={h.id} value={h.id}>
                {h.name}
              </option>
            ))}
          </Select>
        </Field>

        {/* The same warning the recording screen gives, at the moment the
            category is chosen rather than on the VAT return. */}
        {head && !head.input_vat_recoverable ? (
          <p>
            <Badge tone="caution">{t('nx.exp.notReclaimable')}</Badge>
          </p>
        ) : null}

        <Field label={t('nx.expcfg.amount')} error={fieldErrors.amount} required>
          <Input
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            inputMode="decimal"
            autoComplete="off"
          />
        </Field>

        <Field
          label={t('nx.exp.paidFrom')}
          hint={t('nx.exp.paidFromHint')}
          error={fieldErrors.paid_from}
          required
        >
          <Select value={paidFrom} onChange={(e) => setPaidFrom(e.target.value)}>
            <option value="">{t('nx.exp.choosePaidFrom')}</option>
            <option value="cash">{t('nx.exp.fromCash')}</option>
            <option value="bank">{t('nx.exp.fromBank')}</option>
          </Select>
        </Field>

        <Field label={t('nx.expcfg.every')} error={fieldErrors.frequency} required>
          <Select value={preset} onChange={(e) => setPreset(e.target.value)}>
            {PRESETS.map((p) => (
              <option key={p.id} value={p.id}>
                {t(p.labelKey)}
              </option>
            ))}
          </Select>
        </Field>

        <Field
          label={t('nx.expcfg.startsOn')}
          hint={t('nx.expcfg.startsOnHint')}
          error={fieldErrors.starts_on}
          required
        >
          <Input
            type="date"
            value={startsOn}
            onChange={(e) => setStartsOn(e.target.value)}
          />
        </Field>

        <Field
          label={t('nx.expcfg.endsOn')}
          hint={t('nx.expcfg.endsOnHint')}
          error={fieldErrors.ends_on}
        >
          <Input
            type="date"
            value={endsOn}
            onChange={(e) => setEndsOn(e.target.value)}
          />
        </Field>

        {departments.length > 0 ? (
          <Field label={t('nx.exp.department')}>
            <Select
              value={departmentID}
              onChange={(e) => setDepartmentID(e.target.value)}
            >
              <option value="">{t('nx.exp.noDepartment')}</option>
              {departments.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.name}
                </option>
              ))}
            </Select>
          </Field>
        ) : null}

        {suppliers.length > 0 ? (
          <Field label={t('nx.expcfg.supplier')}>
            <Select
              value={supplierID}
              onChange={(e) => setSupplierID(e.target.value)}
            >
              <option value="">{t('nx.expcfg.noSupplier')}</option>
              {suppliers.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.legal_name}
                </option>
              ))}
            </Select>
          </Field>
        ) : null}

        <Field label={t('nx.exp.note')}>
          <Textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={2}
          />
        </Field>

        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <Button type="submit" variant="primary" busy={busy}>
            {t('nx.expcfg.save')}
          </Button>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.expcfg.cancel')}
          </Button>
        </div>

        <p className="text-caption text-muted">{t('nx.expcfg.postsNothing')}</p>
      </form>
    </Panel>
  );
}
