'use client';

// The tax return.
//
// # What stands between these figures and a filing comes first
//
// The route reports `outstanding` — a list, in its own words, of what stops
// this being filed. On a Saudi company today it holds three: the input tax on
// bills does not match the Input VAT account, tax held behind the three-way
// match is not included, and *"the official return form layout has not been
// verified against the tax authority, so these totals are not mapped to
// numbered boxes"*.
//
// A screen that showed the totals and left those in a footnote would be
// presenting an unfiled draft as a filing. So they lead, and the figures follow.
//
// # Nothing here decides whether it is ready
//
// `reconciled`, `outstanding` and `filed` are all the server's. Deciding a
// return was ready would be inventing a regulatory confirmation, which is the
// one thing this product must never do.
//
// # The market names its own tax
//
// `model` and `country` come off the payload. Nothing here says "VAT", and no
// rate appears anywhere: the totals are the ledger's, and the ledger got them
// from the register.

import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Input } from '@/components/ui/field';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, isNegative, isZero } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  readyToFile,
  yearToDate,
  type VatReturn,
  type VatSupply,
} from '@/lib/reports/statements';
import { TAX_TREATMENT } from '@/lib/purchasing/orders';
import { useUrlState } from '@/lib/url-state';

function TaxScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const period = yearToDate();
  const [from, setFrom] = useUrlState('from', period.from);
  const [to, setTo] = useUrlState('to', period.to);

  const { data, isLoading, error, refetch } = useApi<VatReturn>(
    scope ? '/reports/vat-return' : null,
    scope ? { ...scope, from, to } : undefined,
  );

  const money = (v: string) =>
    formatMoney(v, { currency: data?.base_currency || currency, market });

  const columns: Column<VatSupply>[] = [
    {
      key: 'treatment',
      header: t('nx.tax.colTreatment'),
      primary: true,
      cell: (s) => {
        const label = TAX_TREATMENT[s.treatment];
        // Named in the product's own words where it knows the treatment, and
        // as the server sent it where it does not -- a market can have one
        // this build has never heard of, and showing the raw word is better
        // than showing nothing.
        return label ? t(label) : s.treatment;
      },
    },
    {
      key: 'count',
      header: t('nx.tax.colCount'),
      numeric: true,
      secondary: true,
      width: 'w-28',
      cell: (s) => <span className="num text-muted">{s.invoice_count}</span>,
    },
    {
      key: 'net',
      header: t('nx.tax.colNet'),
      numeric: true,
      width: 'w-40',
      cell: (s) => <span className="num">{money(s.net_amount)}</span>,
    },
    {
      key: 'tax',
      header: t('nx.tax.colTax'),
      numeric: true,
      width: 'w-40',
      cell: (s) => <span className="num font-medium">{money(s.tax_amount)}</span>,
    },
  ];

  const refundable = data ? isNegative(data.net_payable) : false;

  return (
    <>
      <PageHeader
        title={t('nx.tax.title')}
        description={t('nx.tax.subtitle')}
        actions={
          data?.filed ? <Badge tone="positive">{t('nx.tax.filed')}</Badge> : null
        }
      />

      <div className="mb-5 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted">{t('nx.fin.from')}</span>
          <Input
            type="date"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
            className="w-auto"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-label text-muted">{t('nx.fin.to')}</span>
          <Input
            type="date"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            className="w-auto"
          />
        </label>
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
      {isLoading && !data ? <TableSkeleton columns={4} /> : null}

      {data ? (
        <>
          {/* First, and not in a footnote. */}
          {!readyToFile(data) && !data.filed ? (
            <section
              className="mb-6 rounded-md border border-caution/25 bg-caution-subtle p-4"
              aria-labelledby="not-ready"
            >
              <h2
                id="not-ready"
                className="text-card-title font-semibold text-caution-fg"
              >
                {t('nx.tax.notReadyTitle')}
              </h2>
              <p className="mt-1 max-w-prose text-body text-caution-fg">
                {t('nx.tax.notReadyBody')}
              </p>
              <ul className="mt-3 flex flex-col gap-1.5">
                {/* The server's own sentences, shown as written. Rewording a
                    regulatory refusal is how it stops meaning what it said. */}
                {(data.outstanding ?? []).map((line) => (
                  <li key={line} className="text-body text-caution-fg">
                    — {line}
                  </li>
                ))}
                {!data.reconciled ? (
                  <li className="text-body text-caution-fg">
                    — {t('nx.tax.disagrees', { amount: money(data.difference) })}
                  </li>
                ) : null}
                {!isZero(data.input_difference) ? (
                  <li className="text-body text-caution-fg">
                    —{' '}
                    {t('nx.tax.inputDifference', {
                      amount: money(data.input_difference),
                    })}
                  </li>
                ) : null}
              </ul>
            </section>
          ) : null}

          {readyToFile(data) ? (
            <p className="mb-6">
              <Badge tone="positive">{t('nx.tax.readyTitle')}</Badge>
            </p>
          ) : null}

          <div className="mb-6 grid gap-4 sm:grid-cols-3">
            <Panel>
              <Figure
                label={t('nx.tax.outputTax')}
                value={money(data.output_tax_total)}
              />
            </Panel>
            <Panel>
              <Figure
                label={t('nx.tax.inputTax')}
                value={money(data.input_tax_total)}
              />
            </Panel>
            <Panel>
              {/* Payable or refundable is a different sentence, not a minus
                  sign somebody has to notice. */}
              <Figure
                label={refundable ? t('nx.tax.netRefund') : t('nx.tax.netPayable')}
                value={money(data.net_payable)}
                tone={refundable ? 'positive' : undefined}
              />
            </Panel>
          </div>

          {data.supplies.length === 0 ? (
            <EmptyState
              title={t('nx.tax.nothingTitle')}
              description={t('nx.tax.nothingDesc')}
            />
          ) : (
            <Panel flush title={t('nx.tax.supplies')}>
              <DataTable
                caption={t('nx.tax.supplies')}
                columns={columns}
                rows={data.supplies}
                rowKey={(s) => s.treatment}
                className="rounded-none border-0"
              />
              <div className="flex items-baseline justify-between gap-4 border-t-[3px] border-double border-line-strong px-4 py-3 font-semibold">
                <span>{t('nx.tax.colNet')}</span>
                <span className="num">{money(data.total_net)}</span>
              </div>
            </Panel>
          )}

          <div className="mt-6 rounded-md border border-line bg-surface p-4">
            <p className="text-label font-medium text-fg">
              {t('nx.tax.ledgerCheck')}
            </p>
            <p className="mt-1 max-w-prose text-caption text-muted">
              {data.reconciled
                ? t('nx.tax.agrees')
                : t('nx.tax.disagrees', { amount: money(data.difference) })}
            </p>
          </div>
        </>
      ) : null}
    </>
  );
}

export default function TaxPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <TaxScreen />
      </Suspense>
    </RequirePermission>
  );
}
