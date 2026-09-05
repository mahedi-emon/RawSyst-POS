'use client';

// The business's tax position.
//
// # Not one rate is typed here
//
// The standard rate, the filing deadline and the retention period are all read
// off `GET /compliance`, which reads them from the regulatory register. There is
// no field on this screen that sets a tax rate, and there never will be: a rate
// somebody typed is a rate nobody verified, and every invoice this business
// issues would be computed from it.
//
// The screen's job is to show what is on file, name what is not verified yet,
// and get somebody to the return.
//
// # Exchange rates ARE typed here, and that is a different thing
//
// A rate between two currencies is a market fact a business records, not a
// legal value a government sets. The server refuses to guess one — a pair with
// none makes the document refuse rather than book at par — so somebody has to
// enter it, and this is where.
//
// # What is unverified is said, not hidden
//
// `unverified_rules` and `blocking_rules` come off the same payload. Twelve
// unverified with one blocker is a business that trades normally with one thing
// to chase, and saying "12 problems" would be as wrong as saying none.

import { Suspense, useState } from 'react';
import { useRouter } from 'next/navigation';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import {
  filingUrgency,
  ratePercent,
  type ComplianceReport,
  type ExchangeRate,
} from '@/lib/tax/compliance';

function today() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
    d.getDate(),
  ).padStart(2, '0')}`;
}

function TaxSettingsScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency } = useCompany();
  const grants = useGrants();
  const mayRecordRate = grants.can('accounting.create');

  const compliance = useApi<{ report: ComplianceReport }>(
    scope && grants.can('compliance.view') ? '/compliance' : null,
    scope ?? undefined,
  );
  const rates = useApiList<ExchangeRate>(
    scope && grants.can('accounting.view') ? '/exchange-rates' : null,
    scope ?? undefined,
  );

  const [from, setFrom] = useState('');
  const [to, setTo] = useState(currency);
  const [rate, setRate] = useState('');
  const [asOf, setAsOf] = useState(today);
  const [source, setSource] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const report = compliance.data?.report;
  const vat = report?.vat;
  const urgency = vat ? filingUrgency(vat) : 'unknown';
  const standard = ratePercent(vat?.standard_rate);

  async function recordRate() {
    if (!scope || !from.trim() || !to.trim() || !rate.trim()) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      await api.put(`/exchange-rates?company_id=${scope.company_id}`, {
        from_currency: from.trim().toUpperCase(),
        to_currency: to.trim().toUpperCase(),
        rate: rate.trim(),
        as_of: asOf,
        source: source.trim(),
      });
      setRate('');
      void rates.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const rateColumns: Column<ExchangeRate>[] = [
    {
      key: 'pair',
      header: t('nx.taxs.colPair'),
      primary: true,
      width: 'w-40',
      cell: (r) => (
        <span className="num font-medium">
          {r.from_currency} → {r.to_currency}
        </span>
      ),
    },
    {
      key: 'rate',
      header: t('nx.taxs.colRate'),
      numeric: true,
      width: 'w-36',
      cell: (r) => <span className="num">{r.rate}</span>,
    },
    {
      key: 'asof',
      header: t('nx.taxs.colAsOf'),
      width: 'w-32',
      cell: (r) => (
        <time dateTime={r.as_of} className="num text-muted">
          {r.as_of}
        </time>
      ),
    },
    {
      key: 'source',
      header: t('nx.taxs.colSource'),
      secondary: true,
      cell: (r) => <span className="text-muted">{r.source || '—'}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.taxs.title')}
        description={t('nx.taxs.subtitle')}
        actions={
          <Button onClick={() => router.push('/reports/tax')}>
            {t('nx.taxs.openReturn')}
          </Button>
        }
      />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      {compliance.error ? (
        <ErrorState
          error={compliance.error}
          onRetry={() => void compliance.refetch()}
        />
      ) : null}

      {report ? (
        <>
          {/* What is not verified leads, because it is the thing that decides
              whether these figures can be relied on at all. */}
          {report.blocking_rules > 0 ? (
            <section
              className="mb-6 rounded-md border border-critical/25 bg-critical-subtle p-4"
              aria-labelledby="blocked"
            >
              <h2 id="blocked" className="text-card-title font-semibold text-critical-fg">
                {t('nx.taxs.blockedTitle', { count: String(report.blocking_rules) })}
              </h2>
              <p className="mt-1 max-w-prose text-body text-critical-fg">
                {t('nx.taxs.blockedBody')}
              </p>
            </section>
          ) : null}

          <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Panel>
              <Figure
                label={t('nx.taxs.registered')}
                value={
                  vat?.registered ? t('nx.taxs.yes') : t('nx.taxs.no')
                }
                caption={vat?.vat_number}
              />
            </Panel>
            <Panel>
              {/* From the register. There is no control here that sets it. */}
              <Figure
                label={t('nx.taxs.standardRate')}
                value={standard ?? t('nx.taxs.notOnFile')}
                caption={t('nx.taxs.fromRegister')}
              />
            </Panel>
            <Panel>
              <Figure
                label={t('nx.taxs.nextFiling')}
                value={vat?.next_filing_due ?? t('nx.taxs.notKnown')}
                caption={
                  vat?.days_to_filing !== undefined
                    ? t('nx.taxs.daysAway', { days: String(vat.days_to_filing) })
                    : t('nx.taxs.filingUnverified')
                }
                tone={urgency === 'critical' ? 'critical' : undefined}
              />
            </Panel>
            <Panel>
              <Figure
                label={t('nx.taxs.retention')}
                value={
                  report.records.retention_years > 0
                    ? t('nx.taxs.years', {
                        years: String(report.records.retention_years),
                      })
                    : t('nx.taxs.notOnFile')
                }
                caption={t('nx.taxs.fromRegister')}
              />
            </Panel>
          </div>

          <div className="mb-6 flex flex-wrap items-center gap-3">
            {report.unverified_rules > 0 ? (
              <Badge tone="caution">
                {t('nx.taxs.unverified', {
                  count: String(report.unverified_rules),
                })}
              </Badge>
            ) : (
              <Badge tone="positive">{t('nx.taxs.allVerified')}</Badge>
            )}
            {vat && vat.open_ended_periods > 0 ? (
              <Badge tone="caution">
                {t('nx.taxs.openPeriods', {
                  count: String(vat.open_ended_periods),
                })}
              </Badge>
            ) : null}
          </div>

          <p className="mb-6 max-w-prose text-body text-muted">
            {t('nx.taxs.whereRatesLive')}
          </p>
        </>
      ) : null}

      <Panel
        flush
        title={t('nx.taxs.ratesTitle')}
        description={t('nx.taxs.ratesHint')}
      >
        {mayRecordRate ? (
          <div className="grid gap-4 border-b border-line p-4 sm:grid-cols-2 lg:grid-cols-6 lg:items-end">
            <Field name="from_currency" label={t('nx.taxs.fromCurrency')} required>
              <Input
                value={from}
                onChange={(e) => setFrom(e.target.value.toUpperCase())}
                maxLength={3}
                className="num uppercase"
              />
            </Field>
            <Field name="to_currency" label={t('nx.taxs.toCurrency')} required>
              <Input
                value={to}
                onChange={(e) => setTo(e.target.value.toUpperCase())}
                maxLength={3}
                className="num uppercase"
              />
            </Field>
            <Field
              name="rate"
              label={t('nx.taxs.rate')}
              hint={t('nx.taxs.rateHint')}
              error={fieldErrors.rate}
              required
            >
              <Input
                value={rate}
                onChange={(e) => setRate(e.target.value)}
                inputMode="decimal"
                className="num text-end"
              />
            </Field>
            <Field name="as_of" label={t('nx.taxs.asOf')}>
              <Input
                type="date"
                value={asOf}
                onChange={(e) => setAsOf(e.target.value)}
              />
            </Field>
            <Field name="source" label={t('nx.taxs.source')} hint={t('nx.taxs.sourceHint')}>
              <Input value={source} onChange={(e) => setSource(e.target.value)} />
            </Field>
            <Button
              variant="primary"
              disabled={busy || !from.trim() || !to.trim() || !rate.trim()}
              onClick={() => void recordRate()}
            >
              {t('nx.taxs.recordRate')}
            </Button>
          </div>
        ) : null}

        {(rates.data?.data ?? []).length === 0 ? (
          <div className="p-4">
            <EmptyState
              title={t('nx.taxs.noRatesTitle')}
              description={t('nx.taxs.noRatesDesc')}
            />
          </div>
        ) : (
          <DataTable
            caption={t('nx.taxs.ratesTitle')}
            columns={rateColumns}
            rows={rates.data?.data ?? []}
            rowKey={(r) => r.id}
            className="rounded-none border-0"
          />
        )}
      </Panel>
    </>
  );
}

export default function TaxSettingsPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <TaxSettingsScreen />
      </Suspense>
    </RequirePermission>
  );
}
