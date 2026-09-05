import { describe, expect, it } from 'vitest';

import {
  documentUrgency,
  filingUrgency,
  invoicingUrgency,
  needsAttention,
  ratePercent,
  type ComplianceReport,
} from './compliance';

function report(over: Partial<ComplianceReport> = {}): ComplianceReport {
  return {
    invoicing: {
      started: true,
      status: 'live',
      devices: 2,
      devices_ready: 2,
      pending: 0,
      failed: 0,
      rejected: 0,
    },
    vat: {
      registered: true,
      vat_number: '355766015445003',
      standard_rate: '15.00',
      next_filing_due: '2026-10-31',
      days_to_filing: 55,
      open_ended_periods: 0,
    },
    privacy: {
      customers: 2,
      marketing_consent: 0,
      open_requests: 0,
      overdue_requests: 0,
      open_incidents: 0,
      incidents_unnotified: 0,
      retention_policies: 0,
      processing_activities: 0,
      dpo_appointed: false,
      legal_holds: 0,
    },
    storefront: { missing: [] },
    payroll: { unsubmitted_runs: 0, deadline_known: false },
    people: {
      expiring_soon: 0,
      expired: 0,
      staff_expiring_soon: 0,
      staff_expired: 0,
    },
    records: { retention_years: 6 },
    unverified_rules: 0,
    blocking_rules: 0,
    ...over,
  };
}

describe('filingUrgency', () => {
  it('is settled with weeks to go', () => {
    expect(filingUrgency(report().vat)).toBe('settled');
  });

  it('warns inside a fortnight', () => {
    expect(filingUrgency({ ...report().vat, days_to_filing: 9 })).toBe('caution');
  });

  it('is critical once the date has passed', () => {
    expect(filingUrgency({ ...report().vat, days_to_filing: -3 })).toBe('critical');
  });

  it('says unknown rather than settled when the server did not say', () => {
    // Which happens when the filing rule for this market is unverified. That
    // reads very differently from "nothing due", and conflating them would tell
    // an owner they were up to date on a deadline nobody has computed.
    expect(
      filingUrgency({
        registered: true,
        open_ended_periods: 0,
      }),
    ).toBe('unknown');
  });

  it('is settled for a business that is not registered', () => {
    expect(filingUrgency({ registered: false, open_ended_periods: 0 })).toBe(
      'settled',
    );
  });
});

describe('invoicingUrgency', () => {
  it('is settled when every till is ready and nothing is stuck', () => {
    expect(invoicingUrgency(report().invoicing)).toBe('settled');
  });

  it('puts a rejection above a failure', () => {
    // A failure is ours or the network's and will be retried. A rejection is
    // the authority refusing the document, and will not fix itself.
    expect(
      invoicingUrgency({ ...report().invoicing, failed: 4, rejected: 1 }),
    ).toBe('critical');
    expect(invoicingUrgency({ ...report().invoicing, failed: 4 })).toBe('caution');
  });

  it('warns when a till is not ready', () => {
    expect(
      invoicingUrgency({ ...report().invoicing, devices_ready: 1 }),
    ).toBe('caution');
  });

  it('says unknown before anything has been onboarded', () => {
    expect(
      invoicingUrgency({ ...report().invoicing, started: false, devices_ready: 0 }),
    ).toBe('unknown');
  });
});

describe('documentUrgency', () => {
  it('lets expired outrank expiring', () => {
    // An expired permit is not a warning about next month. It is somebody who
    // cannot legally work today.
    expect(
      documentUrgency({
        expiring_soon: 3,
        expired: 1,
        staff_expiring_soon: 3,
        staff_expired: 1,
      }),
    ).toBe('critical');
  });

  it('warns on something about to lapse', () => {
    expect(
      documentUrgency({
        expiring_soon: 1,
        expired: 0,
        staff_expiring_soon: 1,
        staff_expired: 0,
      }),
    ).toBe('caution');
  });

  it('is settled when nothing is near', () => {
    expect(documentUrgency(report().people)).toBe('settled');
  });
});

describe('needsAttention', () => {
  it('is empty for a business with nothing outstanding', () => {
    expect(needsAttention(report())).toEqual([]);
  });

  it('puts the critical readings first', () => {
    const list = needsAttention(
      report({
        storefront: { missing: ['cr_number'] },
        payroll: { unsubmitted_runs: 2, deadline_known: false },
        blocking_rules: 1,
      }),
    );
    expect(list[0]?.urgency).toBe('critical');
    expect(list.map((a) => a.key)).toContain('rules');
    expect(list.map((a) => a.key)).toContain('payroll');
    expect(list.map((a) => a.key)).toContain('storefront');
  });

  it('counts a blocking rule and leaves an unverified one alone', () => {
    // Twelve unverified rules with no blocker is a business that trades
    // normally, not a business with twelve problems.
    expect(needsAttention(report({ unverified_rules: 12 }))).toEqual([]);
    expect(
      needsAttention(report({ unverified_rules: 12, blocking_rules: 1 })).map(
        (a) => a.key,
      ),
    ).toEqual(['rules']);
  });

  it('raises an unnotified incident as critical', () => {
    const list = needsAttention(
      report({
        privacy: { ...report().privacy, incidents_unnotified: 1 },
      }),
    );
    expect(list).toEqual([{ key: 'incidents', urgency: 'critical' }]);
  });
});

describe('ratePercent', () => {
  it('tidies the trailing zeros', () => {
    expect(ratePercent('15.00')).toBe('15%');
  });

  it('keeps a fractional rate', () => {
    expect(ratePercent('7.50')).toBe('7.5%');
  });

  it('never multiplies', () => {
    // The register speaks in percent. A helper that converted would turn a
    // fraction-shaped rate into 1500%.
    expect(ratePercent('0.15')).toBe('0.15%');
  });

  it('is null when there is no rate on file', () => {
    expect(ratePercent(undefined)).toBeNull();
    expect(ratePercent('  ')).toBeNull();
  });
});
