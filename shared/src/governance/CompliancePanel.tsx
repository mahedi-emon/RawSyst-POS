// The compliance monitoring dashboard (blueprint E7).
//
// # Readings, not a score
//
// E7 asks one question — "am I legally exposed right now?" — and answers it
// with nine specific readings. This screen shows those nine and nothing else.
// There is no overall traffic light: a single green tick over nine unrelated
// legal obligations is a claim this product cannot make and a shop should not
// rely on.
//
// # A deadline that is not known says so
//
// The wage-file window is one of the rules the blueprint flags as needing
// verification against the official source, and until somebody has verified it
// the payroll tile says the deadline is not known rather than showing a date
// this product invented. A shop that plans around a made-up deadline and misses
// the real one is worse off than a shop that was told nobody knows.

import { useCallback } from 'react';

import { complianceReport, type ComplianceReport } from '../api/governance';
import { useAuth } from '../auth/session';
import { RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { shortDate } from '../ui/format';

/** How a reading is behaving. Per reading, never for the shop as a whole. */
type Tone = 'ok' | 'watch' | 'late' | 'quiet';

export function CompliancePanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () => complianceReport(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(payload: { report: ComplianceReport }) => {
        const r = payload.report;
        return (
          <div className="cmp">
            <Reading
              title={t('cmp.invoicing')}
              tone={
                !r.invoicing.started
                  ? 'quiet'
                  : r.invoicing.failed > 0 || r.invoicing.rejected > 0
                    ? 'late'
                    : r.invoicing.pending > 0
                      ? 'watch'
                      : 'ok'
              }
              headline={
                r.invoicing.started
                  ? t('cmp.invoicingPending', {
                      n: String(r.invoicing.pending),
                    })
                  : t('cmp.notStarted')
              }
              rows={[
                [t('cmp.terminals'),
                 `${r.invoicing.devices_ready} / ${r.invoicing.devices}`],
                [t('cmp.failed'), String(r.invoicing.failed)],
                [t('cmp.rejected'), String(r.invoicing.rejected)],
              ]}
            />

            <Reading
              title={t('cmp.vat')}
              tone={
                !r.vat.registered
                  ? 'quiet'
                  : r.vat.days_to_filing !== undefined &&
                      r.vat.days_to_filing < 0
                    ? 'late'
                    : r.vat.days_to_filing !== undefined &&
                        r.vat.days_to_filing < 7
                      ? 'watch'
                      : 'ok'
              }
              headline={
                r.vat.registered
                  ? r.vat.next_filing_due
                    ? t('cmp.filingDue', {
                        when: shortDate(r.vat.next_filing_due, locale),
                      })
                    : t('cmp.noFilingDate')
                  : t('cmp.notRegistered')
              }
              rows={[
                [t('cmp.rate'), r.vat.standard_rate ?? '—'],
                [t('cmp.vatNumber'), r.vat.vat_number ?? '—'],
                [
                  t('cmp.openPeriods'),
                  String(r.vat.open_ended_periods),
                ],
              ]}
            />

            <Reading
              title={t('cmp.requests')}
              tone={
                r.privacy.overdue_requests > 0
                  ? 'late'
                  : r.privacy.soonest_due_days !== undefined &&
                      r.privacy.soonest_due_days < 7
                    ? 'watch'
                    : r.privacy.open_requests > 0
                      ? 'ok'
                      : 'quiet'
              }
              headline={t('cmp.requestsOpen', {
                n: String(r.privacy.open_requests),
              })}
              rows={[
                [t('cmp.overdue'), String(r.privacy.overdue_requests)],
                [
                  t('cmp.soonest'),
                  r.privacy.soonest_due_days !== undefined
                    ? t('cmp.inDays', {
                        n: String(r.privacy.soonest_due_days),
                      })
                    : '—',
                ],
              ]}
            />

            <Reading
              title={t('cmp.incidents')}
              tone={
                r.privacy.incident_hours_left !== undefined &&
                r.privacy.incident_hours_left < 0
                  ? 'late'
                  : r.privacy.incidents_unnotified > 0
                    ? 'watch'
                    : r.privacy.open_incidents > 0
                      ? 'ok'
                      : 'quiet'
              }
              headline={t('cmp.incidentsOpen', {
                n: String(r.privacy.open_incidents),
              })}
              rows={[
                [
                  t('cmp.unnotified'),
                  String(r.privacy.incidents_unnotified),
                ],
                [
                  t('cmp.hoursLeft'),
                  r.privacy.incident_hours_left !== undefined
                    ? String(r.privacy.incident_hours_left)
                    : '—',
                ],
              ]}
            />

            <Reading
              title={t('cmp.consent')}
              tone={r.privacy.dpo_appointed ? 'ok' : 'watch'}
              headline={t('cmp.consentHeld', {
                held: String(r.privacy.marketing_consent),
                of: String(r.privacy.customers),
              })}
              rows={[
                [
                  t('cmp.dpo'),
                  r.privacy.dpo_appointed ? t('common.yes') : t('common.no'),
                ],
                [
                  t('cmp.activities'),
                  String(r.privacy.processing_activities),
                ],
                [t('cmp.holds'), String(r.privacy.legal_holds)],
              ]}
            />

            <Reading
              title={t('cmp.retention')}
              tone={r.privacy.retention_policies > 0 ? 'ok' : 'watch'}
              headline={t('cmp.policies', {
                n: String(r.privacy.retention_policies),
              })}
              rows={[
                [
                  t('cmp.lastRun'),
                  r.privacy.retention_last_run
                    ? shortDate(r.privacy.retention_last_run, locale)
                    : t('cmp.neverRun'),
                ],
              ]}
            />

            <Reading
              title={t('cmp.storefront')}
              tone={r.storefront.missing.length === 0 ? 'ok' : 'watch'}
              headline={
                r.storefront.missing.length === 0
                  ? t('cmp.disclosuresComplete')
                  : t('cmp.disclosuresMissing', {
                      n: String(r.storefront.missing.length),
                    })
              }
              rows={r.storefront.missing.map((m) => [
                t(`cmp.disc.${m}` as Key),
                t('cmp.notFilledIn'),
              ])}
            />

            <Reading
              title={t('cmp.payroll')}
              tone={
                r.payroll.unsubmitted_runs > 0
                  ? 'watch'
                  : r.payroll.last_run_period
                    ? 'ok'
                    : 'quiet'
              }
              headline={
                r.payroll.last_run_period
                  ? t('cmp.lastRunPeriod', {
                      period: r.payroll.last_run_period,
                    })
                  : t('cmp.noPayrollYet')
              }
              rows={[
                [
                  t('cmp.unsubmitted'),
                  String(r.payroll.unsubmitted_runs),
                ],
                [
                  t('cmp.deadline'),
                  r.payroll.deadline_known && r.payroll.next_deadline
                    ? shortDate(r.payroll.next_deadline, locale)
                    : t('cmp.deadlineUnknown'),
                ],
              ]}
            />

            <Reading
              title={t('cmp.documents')}
              tone={
                r.people.expired > 0
                  ? 'late'
                  : r.people.expiring_soon > 0
                    ? 'watch'
                    : 'ok'
              }
              headline={t('cmp.docsExpiring', {
                n: String(r.people.expiring_soon),
              })}
              rows={[[t('cmp.alreadyExpired'), String(r.people.expired)]]}
            />

            <Reading
              title={t('cmp.records')}
              tone={
                r.records.backup_age_days === undefined
                  ? 'watch'
                  : r.records.backup_age_days > 7
                    ? 'late'
                    : 'ok'
              }
              headline={
                r.records.last_verified_backup
                  ? t('cmp.backupVerified', {
                      when: shortDate(r.records.last_verified_backup, locale),
                    })
                  : t('cmp.noVerifiedBackup')
              }
              rows={[
                [
                  t('cmp.mustKeep'),
                  t('cmp.years', { n: String(r.records.retention_years) }),
                ],
                [
                  t('cmp.oldestInvoice'),
                  r.records.oldest_invoice
                    ? shortDate(r.records.oldest_invoice, locale)
                    : '—',
                ],
              ]}
            />

            <Reading
              title={t('cmp.rules')}
              tone={
                r.blocking_rules > 0
                  ? 'late'
                  : r.unverified_rules > 0
                    ? 'watch'
                    : 'ok'
              }
              headline={t('cmp.unverified', {
                n: String(r.unverified_rules),
              })}
              rows={[[t('cmp.blocking'), String(r.blocking_rules)]]}
            />
          </div>
        );
      }}
    </RemoteBody>
  );
}

/** One reading: a headline, a tone, and the numbers behind it. */
function Reading({
  title,
  tone,
  headline,
  rows,
}: {
  title: string;
  tone: Tone;
  headline: string;
  rows: Array<[string, string]>;
}) {
  return (
    <article className={`cmp__card cmp__card--${tone}`}>
      <h3 className="cmp__title">{title}</h3>
      <p className="cmp__headline">{headline}</p>
      {rows.length > 0 && (
        <dl className="cmp__rows">
          {rows.map(([label, value], i) => (
            <div className="cmp__row" key={i}>
              <dt>{label}</dt>
              <dd className="num">{value}</dd>
            </div>
          ))}
        </dl>
      )}
    </article>
  );
}
