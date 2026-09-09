'use client';

// Who works here, and which of their documents is about to lapse.
//
// # E6, and the line it does not cross
//
// The route answers a count, a split and a percentage, and deliberately no
// Nitaqat band: the band depends on the establishment's activity, its size
// bracket and a schedule the ministry publishes and revises. Asserting one from
// a head count would be inventing a regulatory classification, so the screen
// says where the band actually comes from instead of guessing at it.
//
// # No date picker, because there is no date
//
// This is who is employed right now — `left_on IS NULL` — not a figure for a
// month. A period control over a route that ignores periods is worse than none:
// somebody would set it and believe the answer.
//
// # The nationality split appears where it means something
//
// `is_saudi` is a fact the employee record carries in every market, and the
// RATIO is a Saudi regulatory measure. A shop in Dhaka reading "40% Saudi"
// learns nothing and may act on it, so the ratio and its explanation are shown
// for a Saudi company and the head count is shown for everybody.
//
// # Expiry leads when something has expired
//
// A lapsed residence permit is a person who cannot legally be on shift today. A
// permit lapsing in six weeks is a diary entry. The screen puts the first at the
// top and never adds the two together.

import { Users } from 'lucide-react';
import Link from 'next/link';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Badge, Figure, PageHeader, Panel, type Tone } from '@/components/ui/panel';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  departmentsAddUp,
  expiryPressure,
  otherThanSaudi,
  rankedDepartments,
  saudiFraction,
  type ExpiryPressure,
  type Workforce,
  type WorkforceLine,
} from '@/lib/reports/workforce';
import { cn } from '@/lib/utils';

const PRESSURE_TONE: Record<ExpiryPressure, Tone> = {
  expired: 'critical',
  due_soon: 'caution',
  clear: 'positive',
};

const PRESSURE_LABEL: Record<ExpiryPressure, Key> = {
  expired: 'nx.wf.someExpired',
  due_soon: 'nx.wf.someExpiring',
  clear: 'nx.wf.allCurrent',
};

function WorkforceScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { market } = useCompany();

  const { data, isLoading, error, refetch } = useApi<{ workforce: Workforce }>(
    scope ? '/reports/workforce' : null,
    scope ?? undefined,
  );

  const report = data?.workforce;
  const isSaudi = market === 'SA';

  const columns: Column<WorkforceLine>[] = [
    {
      key: 'department',
      header: t('nx.wf.colDepartment'),
      primary: true,
      // The route substitutes an em dash for people with no department set, and
      // that is a real group rather than a formatting artefact.
      cell: (line) =>
        line.department === '—' ? (
          <span className="text-muted">{t('nx.wf.noDepartment')}</span>
        ) : (
          line.department
        ),
    },
    {
      key: 'total',
      header: t('nx.wf.colPeople'),
      numeric: true,
      width: 'w-28',
      cell: (line) => <span className="num">{line.total}</span>,
    },
    ...(isSaudi
      ? [
          {
            key: 'saudi',
            header: t('nx.wf.colSaudi'),
            numeric: true,
            width: 'w-28',
            cell: (line: WorkforceLine) => <span className="num">{line.saudi}</span>,
          },
          {
            key: 'other',
            header: t('nx.wf.colOther'),
            numeric: true,
            width: 'w-28',
            secondary: true,
            cell: (line: WorkforceLine) => (
              <span className="num">{otherThanSaudi(line)}</span>
            ),
          },
        ]
      : []),
  ];

  return (
    <>
      <PageHeader
        title={t('nx.wf.title')}
        description={t('nx.wf.subtitle')}
        actions={
          <Button asChild variant="secondary">
            <Link href="/people/employees">{t('nx.wf.openStaff')}</Link>
          </Button>
        }
      />

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}

      {isLoading && !report ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-20" />
          ))}
        </div>
      ) : null}

      {report ? (
        <>
          {/* The figures, in the order somebody acts on them. */}
          <section className="grid gap-5 rounded-md border border-line bg-surface p-4 sm:grid-cols-2 lg:grid-cols-4">
            <Figure label={t('nx.wf.headCount')} value={report.total} />
            {isSaudi ? (
              <Figure
                label={t('nx.wf.saudiShare')}
                value={`${report.saudi_share}%`}
                caption={t('nx.wf.ofHeadCount', {
                  saudi: String(report.saudi),
                  total: String(report.total),
                })}
              />
            ) : null}
            <Figure
              label={t('nx.wf.expiringSoon')}
              value={report.expiring_soon}
              caption={t('nx.wf.withinSixtyDays')}
              tone={report.expiring_soon > 0 ? 'critical' : undefined}
            />
            <Figure
              label={t('nx.wf.expired')}
              value={report.expired}
              caption={t('nx.wf.cannotWork')}
              tone={report.expired > 0 ? 'critical' : undefined}
            />
          </section>

          {/* Colour is never the only signal: the state carries a word. */}
          <p className="mt-3 flex items-center gap-2 text-body">
            <Badge tone={PRESSURE_TONE[expiryPressure(report)]}>
              {t(PRESSURE_LABEL[expiryPressure(report)])}
            </Badge>
            <span className="text-muted">{t('nx.wf.documentsNote')}</span>
          </p>

          {isSaudi ? (
            <Panel
              className="mt-6"
              title={t('nx.wf.ratioTitle')}
              description={t('nx.wf.ratioHint')}
            >
              {/* Drawn from the counts rather than from parsing the
                  percentage, so the bar and the number cannot disagree. */}
              <div
                className="h-2 w-full overflow-hidden rounded-xs bg-surface-sunken"
                role="img"
                aria-label={t('nx.wf.barLabel', {
                  share: report.saudi_share,
                })}
              >
                <div
                  className="h-full bg-primary"
                  style={{ inlineSize: `${saudiFraction(report) * 100}%` }}
                />
              </div>
              <p className="mt-3 max-w-prose text-caption text-muted">
                {t('nx.wf.notANitaqatBand')}
              </p>
            </Panel>
          ) : null}

          <h2 className="mt-8 mb-3 text-card-title font-semibold text-fg">
            {t('nx.wf.byDepartment')}
          </h2>

          {!departmentsAddUp(report) ? (
            // A compliance figure that quietly excludes people is the kind of
            // error an inspector finds. Said here rather than left to be
            // noticed by adding up a column.
            <p className="mb-3 text-body text-caution-fg" role="status">
              {t('nx.wf.doNotAddUp', { total: String(report.total) })}
            </p>
          ) : null}

          {report.by_department.length === 0 ? (
            <EmptyState
              icon={Users}
              title={t('nx.wf.emptyTitle')}
              description={t('nx.wf.emptyDesc')}
              action={
                <Button asChild variant="primary">
                  <Link href="/people/employees/new">{t('nx.wf.addSomebody')}</Link>
                </Button>
              }
            />
          ) : (
            <DataTable
              caption={t('nx.wf.byDepartment')}
              columns={columns}
              rows={rankedDepartments(report)}
              rowKey={(line) => line.department}
              totals={
                <>
                  <td className={cn('px-3 py-2.5')}>{t('nx.wf.everyone')}</td>
                  <td className="num px-3 py-2.5 text-end">{report.total}</td>
                  {isSaudi ? (
                    <>
                      <td className="num px-3 py-2.5 text-end">{report.saudi}</td>
                      <td className="num hidden px-3 py-2.5 text-end md:table-cell">
                        {report.non_saudi}
                      </td>
                    </>
                  ) : null}
                </>
              }
            />
          )}
        </>
      ) : null}

      {isLoading && !report ? <TableSkeleton columns={4} rows={4} /> : null}
    </>
  );
}

export default function WorkforcePage() {
  return (
    <RequirePermission anyOf={['report.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <WorkforceScreen />
      </Suspense>
    </RequirePermission>
  );
}
