'use client';

// Money moved between the business's own accounts.
//
// # A transfer is neither income nor a cost
//
// Cash taken to the bank has not been earned and has not been spent; it has
// moved. The screen says so, because a list of amounts with dates looks exactly
// like a list of takings, and somebody reading it quickly will add them to the
// month.
//
// # The form is beside the list
//
// There is no route for one transfer, and no reason to leave the page to record
// one: somebody with a paying-in slip in their hand wants to type it and see it
// appear.

import { ArrowLeftRight } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import type { MoneyAccount, Transfer } from '@/lib/money/money-accounts';

function TransfersScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApiList<Transfer>(
    scope ? '/treasury/transfers' : null,
    scope ? { ...scope, limit: 100 } : undefined,
  );
  const accounts = useApiList<MoneyAccount>(
    scope ? '/treasury/accounts' : null,
    scope ?? undefined,
  );

  const [fromId, setFromId] = useState('');
  const [toId, setToId] = useState('');
  const [amount, setAmount] = useState('');
  const [movedOn, setMovedOn] = useState(() => new Date().toISOString().slice(0, 10));
  const [reference, setReference] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  // Required by the route, and it is money: a retry after a lost response
  // must return the original rather than banking the takings twice. Replaced
  // once a transfer has actually been recorded.
  const [docUUID, setDocUUID] = useState(() => crypto.randomUUID());

  const rows = data?.data ?? [];
  const places = (accounts.data?.data ?? []).filter((a) => a.is_active);
  const sameBothEnds = fromId !== '' && fromId === toId;
  const mayMove = grants.can('accounting.create');

  async function record() {
    if (!scope) return;
    setFormError(null);
    setFieldErrors({});
    if (!fromId || !toId) return setFormError(t('nx.tfr.needAccounts'));
    if (sameBothEnds) return setFormError(t('nx.tfr.sameWarning'));
    if (amount.trim() === '') return setFormError(t('nx.tfr.needAmount'));

    setBusy(true);
    try {
      await api.post(`/treasury/transfers?company_id=${scope.company_id}`, {
        uuid: docUUID,
        from_account_id: fromId,
        to_account_id: toId,
        amount,
        moved_on: movedOn,
        reference,
        note,
      });
      setAmount('');
      setReference('');
      setNote('');
      setDocUUID(crypto.randomUUID());
      await refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setFormError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Transfer>[] = [
    {
      key: 'when',
      header: t('nx.tfr.colWhen'),
      width: 'w-32',
      secondary: true,
      cell: (x) => (
        <time dateTime={x.moved_on} className="num text-muted">
          {x.moved_on}
        </time>
      ),
    },
    { key: 'from', header: t('nx.tfr.colFrom'), primary: true, cell: (x) => x.from_account },
    { key: 'to', header: t('nx.tfr.colTo'), cell: (x) => x.to_account },
    {
      key: 'reference',
      header: t('nx.tfr.colReference'),
      secondary: true,
      cell: (x) => <span className="num text-muted">{x.reference || '—'}</span>,
    },
    {
      key: 'amount',
      header: t('nx.tfr.colAmount'),
      numeric: true,
      width: 'w-36',
      cell: (x) => (
        <span className="num font-medium">
          {formatMoney(x.amount, { currency: x.currency || currency, market })}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.tfr.title')} description={t('nx.tfr.subtitle')} />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="min-w-0">
          {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
          {isLoading && rows.length === 0 ? <TableSkeleton columns={5} /> : null}

          {!isLoading && !error && rows.length === 0 ? (
            <EmptyState
              icon={ArrowLeftRight}
              title={t('nx.tfr.emptyTitle')}
              description={t('nx.tfr.emptyDesc')}
            />
          ) : null}

          {rows.length > 0 ? (
            <DataTable
              caption={t('nx.tfr.caption')}
              columns={columns}
              rows={rows}
              rowKey={(x) => x.id}
            />
          ) : null}
        </div>

        {mayMove ? (
          <Panel title={t('nx.tfr.move')}>
            <FormError message={formError} className="mb-3" />
            <div className="flex flex-col gap-4">
              <Field label={t('nx.tfr.from')} error={fieldErrors.from_account_id} required>
                <Select value={fromId} onChange={(e) => setFromId(e.target.value)}>
                  <option value="">{t('nx.exp.choosePaidFrom')}</option>
                  {places.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field
                label={t('nx.tfr.to')}
                error={sameBothEnds ? t('nx.tfr.sameWarning') : fieldErrors.to_account_id}
                required
              >
                <Select value={toId} onChange={(e) => setToId(e.target.value)}>
                  <option value="">{t('nx.exp.choosePaidFrom')}</option>
                  {places.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label={t('nx.tfr.amount')} error={fieldErrors.amount} required>
                <Input
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                  inputMode="decimal"
                  autoComplete="off"
                />
              </Field>
              <Field label={t('nx.tfr.when')} error={fieldErrors.moved_on}>
                <Input
                  type="date"
                  value={movedOn}
                  onChange={(e) => setMovedOn(e.target.value)}
                />
              </Field>
              <Field
                label={t('nx.tfr.reference')}
                hint={t('nx.tfr.referenceHint')}
                error={fieldErrors.reference}
              >
                <Input
                  value={reference}
                  onChange={(e) => setReference(e.target.value)}
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              <Field label={t('nx.tfr.note')}>
                <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={2} />
              </Field>
              <Button
                variant="primary"
                busy={busy}
                disabled={sameBothEnds}
                onClick={() => void record()}
              >
                {t('nx.tfr.record')}
              </Button>
            </div>
          </Panel>
        ) : null}
      </div>
    </>
  );
}

export default function TreasuryTransfersPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <TransfersScreen />
      </Suspense>
    </RequirePermission>
  );
}
