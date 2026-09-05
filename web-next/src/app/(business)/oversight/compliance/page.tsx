'use client';

// E7's compliance dashboard.
//
// # What needs doing leads; the readings follow
//
// Nine equal panels would tell an owner nothing, because nothing on the screen
// would be more important than anything else. So the screen opens with what
// needs attention, ordered by how pressing it is, and each line links to the
// screen where it gets fixed. A dashboard that names a problem and leaves you
// hunting for the page is a dashboard people stop opening.
//
// # "We do not know" is not "nothing to do"
//
// A filing deadline the register cannot compute reads as unknown, not as
// settled. An e-invoicing chain nobody has onboarded reads as not started, not
// as healthy. Both would otherwise show green to a business that is exposed.
//
// # Unverified is not broken
//
// `unverified_rules` counts legal values nobody has checked against a primary
// source. `blocking_rules` counts the subset that stop something working.
// Twelve unverified with one blocker is a business trading normally with one
// thing to chase; reporting twelve problems would be as wrong as reporting
// none.

import { ShieldCheck } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Badge, Figure, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { useApi } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  documentUrgency,
  filingUrgency,
  invoicingUrgency,
  needsAttention,
  ratePercent,
  type ComplianceReport,
  type Urgency,
} from '@/lib/tax/compliance';
import { cn } from '@/lib/utils';

/** Each thing that can need attention, what it says, and where it is fixed. */
const ATTENTION: Record<string, { key: Key; href: string }> = {
  invoicing: { key: 'nx.comp.aInvoicing', href: '/settings/einvoicing' },
  filing: { key: 'nx.comp.aFiling', href: '/reports/tax' },
  documents: { key: 'nx.comp.aDocuments', href: '/people/employees' },
  rules: { key: 'nx.comp.aRules', href: '/settings/tax' },
  payroll: { key: 'nx.comp.aPayroll', href: '/people/payroll' },
  privacy: { key: 'nx.comp.aPrivacy', href: '/oversight/privacy' },
  incidents: { key: 'nx.comp.aIncidents', href: '/oversight/privacy' },
  storefront: { key: 'nx.comp.aStorefront', href: '/settings/business' },
  periods: { key: 'nx.comp.aPeriods', href: '/money/chart' },
};

const TONE: Record<Urgency, 'positive' | 'caution' | 'critical' | 'neutral'> = {
  settled: 'positive',
  caution: 'caution',
  critical: 'critical',
  unknown: 'neutral',
};

const URGENCY_LABEL: Record<Urgency, Key> = {
  settled: 'nx.comp.settled',
  caution: 'nx.comp.attention',
  critical: 'nx.comp.urgent',
  unknown: 'nx.comp.notKnown',
};

function Reading({
  label,
  value,
  caption,
  urgency,
}: {
  label: string;
  value: string;
  caption?: string;
  urgency: Urgency;
}) {
  const t = useT();
  return (
    <Panel>
      <Figure
        label={label}
        value={value}
        caption={caption}
        tone={
          urgency === 'critical'
            ? 'critical'
            : urgency === 'settled'
              ? 'positive'
              : undefined
        }
      />
      <p className="mt-2">
        <Badge tone={TONE[urgency]}>{t(URGENCY_LABEL[urgency])}</Badge>
      </p>
    </Panel>
  );
}

function ComplianceScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();

  const { data, isLoading, error, refetch } = useApi<{ report: ComplianceReport }>(
    scope ? '/compliance' : null,
    scope ?? undefined,
  );

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;
  if (isLoading && !data) return <div className="h-64" aria-busy="true" />;
  const report = data?.report;
  if (!report) return null;

  const outstanding = needsAttention(report);
  const filing = filingUrgency(report.vat);
  const invoicing = invoicingUrgency(report.invoicing);
  const documents = documentUrgency(report.people);

  return (
    <>
      <PageHeader title={t('nx.comp.title')} description={t('nx.comp.subtitle')} />

      {outstanding.length === 0 ? (
        <EmptyState
          icon={ShieldCheck}
          title={t('nx.comp.clearTitle')}
          description={t('nx.comp.clearDesc')}
        />
      ) : (
        <section
          className="mb-6 rounded-md border border-line bg-surface p-4"
          aria-labelledby="attention"
        >
          <h2 id="attention" className="text-card-title font-semibold text-fg">
            {t('nx.comp.attentionTitle', { count: String(outstanding.length) })}
          </h2>
          <ul className="mt-3 flex flex-col gap-2">
            {outstanding.map((item) => {
              const meta = ATTENTION[item.key];
              if (!meta) return null;
              return (
                <li key={item.key} className="flex items-start gap-3">
                  {/* Not colour alone: the badge carries a word too. */}
                  <Badge tone={TONE[item.urgency]}>
                    {t(URGENCY_LABEL[item.urgency])}
                  </Badge>
                  <button
                    type="button"
                    onClick={() => router.push(meta.href)}
                    className={cn(
                      'min-h-11 flex-1 text-start text-body text-fg',
                      'underline underline-offset-2 hover:no-underline',
                    )}
                  >
                    {t(meta.key)}
                  </button>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Reading
          label={t('nx.comp.filing')}
          value={report.vat.next_filing_due ?? t('nx.comp.notKnown')}
          caption={
            report.vat.standard_rate
              ? t('nx.comp.atRate', {
                  rate: ratePercent(report.vat.standard_rate) ?? '',
                })
              : undefined
          }
          urgency={filing}
        />
        <Reading
          label={t('nx.comp.invoicing')}
          value={t('nx.comp.readyOf', {
            ready: String(report.invoicing.devices_ready),
            total: String(report.invoicing.devices),
          })}
          caption={
            report.invoicing.rejected > 0
              ? t('nx.comp.rejected', { count: String(report.invoicing.rejected) })
              : report.invoicing.pending > 0
                ? t('nx.comp.pending', { count: String(report.invoicing.pending) })
                : undefined
          }
          urgency={invoicing}
        />
        <Reading
          label={t('nx.comp.documents')}
          value={String(report.people.expiring_soon + report.people.expired)}
          caption={
            report.people.staff_expiring_soon + report.people.staff_expired > 0
              ? t('nx.comp.ofWhichStaff', {
                  count: String(
                    report.people.staff_expiring_soon + report.people.staff_expired,
                  ),
                })
              : undefined
          }
          urgency={documents}
        />
      </div>

      <div className="grid gap-5 lg:grid-cols-2">
        <Panel title={t('nx.comp.payroll')}>
          <dl className="grid gap-3 sm:grid-cols-2">
            <div>
              <dt className="text-label text-muted">{t('nx.comp.lastRun')}</dt>
              <dd className="num mt-0.5 text-body">
                {report.payroll.last_run_period ?? t('nx.comp.never')}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.comp.unsubmitted')}</dt>
              <dd className="num mt-0.5 text-body">
                {report.payroll.unsubmitted_runs}
              </dd>
            </div>
          </dl>
          {!report.payroll.deadline_known ? (
            // Said, not hidden. A wage-file deadline nobody has verified is
            // different from no deadline.
            <p className="mt-3 max-w-prose text-caption text-muted">
              {t('nx.comp.deadlineUnknown')}
            </p>
          ) : null}
        </Panel>

        <Panel title={t('nx.comp.records')}>
          <dl className="grid gap-3 sm:grid-cols-2">
            <div>
              <dt className="text-label text-muted">{t('nx.comp.retention')}</dt>
              <dd className="num mt-0.5 text-body">
                {report.records.retention_years > 0
                  ? t('nx.comp.years', {
                      years: String(report.records.retention_years),
                    })
                  : t('nx.comp.notKnown')}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.comp.oldestInvoice')}</dt>
              <dd className="num mt-0.5 text-body">
                {report.records.oldest_invoice ?? '—'}
              </dd>
            </div>
            <div className="sm:col-span-2">
              <dt className="text-label text-muted">{t('nx.comp.lastBackup')}</dt>
              <dd className="mt-0.5 text-body">
                {report.records.last_verified_backup
                  ? t('nx.comp.backupAge', {
                      days: String(report.records.backup_age_days ?? 0),
                    })
                  : // The only honest answer this product can give about an
                    // archive is whether anybody has proved a restore.
                    t('nx.comp.noBackupProved')}
              </dd>
            </div>
          </dl>
        </Panel>

        <Panel title={t('nx.comp.privacy')}>
          <dl className="grid gap-3 sm:grid-cols-2">
            <div>
              <dt className="text-label text-muted">{t('nx.comp.openRequests')}</dt>
              <dd className="num mt-0.5 text-body">
                {report.privacy.open_requests}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.comp.overdueRequests')}</dt>
              <dd
                className={cn(
                  'num mt-0.5 text-body',
                  report.privacy.overdue_requests > 0 && 'text-critical-fg',
                )}
              >
                {report.privacy.overdue_requests}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.comp.incidents')}</dt>
              <dd className="num mt-0.5 text-body">
                {report.privacy.open_incidents}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.comp.dpo')}</dt>
              <dd className="mt-0.5 text-body">
                {report.privacy.dpo_appointed
                  ? t('nx.comp.appointed')
                  : t('nx.comp.notAppointed')}
              </dd>
            </div>
          </dl>
        </Panel>

        <Panel title={t('nx.comp.rules')}>
          <div className="flex flex-wrap gap-2">
            {report.blocking_rules > 0 ? (
              <Badge tone="critical">
                {t('nx.comp.blocking', { count: String(report.blocking_rules) })}
              </Badge>
            ) : null}
            {report.unverified_rules > 0 ? (
              <Badge tone="caution">
                {t('nx.comp.unverified', {
                  count: String(report.unverified_rules),
                })}
              </Badge>
            ) : (
              <Badge tone="positive">{t('nx.comp.allVerified')}</Badge>
            )}
          </div>
          <p className="mt-3 max-w-prose text-caption text-muted">
            {t('nx.comp.rulesHint')}
          </p>
        </Panel>

        {report.storefront.missing.length > 0 ? (
          <Panel
            className="lg:col-span-2"
            title={t('nx.comp.storefront')}
            description={t('nx.comp.storefrontHint')}
          >
            <ul className="flex flex-wrap gap-2">
              {/* The server's own field names. Inventing friendlier labels for
                  a list that can grow would leave the new ones untranslated. */}
              {report.storefront.missing.map((field) => (
                <li key={field}>
                  <Badge tone="caution">
                    <span className="num">{field}</span>
                  </Badge>
                </li>
              ))}
            </ul>
          </Panel>
        ) : null}
      </div>
    </>
  );
}

export default function CompliancePage() {
  return (
    <RequirePermission anyOf={['compliance.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ComplianceScreen />
      </Suspense>
    </RequirePermission>
  );
}
