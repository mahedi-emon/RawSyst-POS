import { plainEnglish, plain } from '@rawsyst/shared/i18n/strings';
import { describe, expect, it } from 'vitest';

import {
  FREQUENCY,
  PRESETS,
  describeCadence,
  isDue,
  presetFor,
  type Recurring,
} from './expenses';

const t = (key: Parameters<typeof plainEnglish>[0], params?: Record<string, string | number>) =>
  plain(plainEnglish(key, params));

describe('how often a standing cost repeats', () => {
  it('offers only the three frequencies the API accepts', () => {
    // The service refuses anything else outright, and the refusal is the
    // documentation: "A schedule repeats weekly, monthly or yearly." A fourth
    // option here would be a select that produces a 400 on save.
    expect(Object.keys(FREQUENCY).sort()).toEqual(['monthly', 'weekly', 'yearly']);
  });

  it('sends quarterly as monthly, three at a time', () => {
    // Nobody signs a lease "monthly, every three". The person picks the word
    // they use; the request carries the pair the API stores.
    const quarterly = PRESETS.find((p) => p.id === 'quarterly');
    expect(quarterly?.cadence).toEqual({ frequency: 'monthly', interval_count: 3 });
  });

  it('never offers a preset the API would refuse', () => {
    for (const preset of PRESETS) {
      expect(Object.keys(FREQUENCY)).toContain(preset.cadence.frequency);
      // The column is `interval_count > 0 AND <= 60`.
      expect(preset.cadence.interval_count).toBeGreaterThan(0);
      expect(preset.cadence.interval_count).toBeLessThanOrEqual(60);
    }
  });

  it('gives every preset its own cadence', () => {
    // Two presets meaning the same thing would make `presetFor` pick one and
    // silently rename the other on the way back from the server.
    const pairs = PRESETS.map((p) => `${p.cadence.frequency}:${p.cadence.interval_count}`);
    expect(new Set(pairs).size).toBe(PRESETS.length);
  });
});

describe('a cadence in words', () => {
  it('names the one a person chose', () => {
    expect(describeCadence({ frequency: 'monthly', interval_count: 3 }, t)).toBe(
      'Every quarter',
    );
    expect(describeCadence({ frequency: 'weekly', interval_count: 2 }, t)).toBe(
      'Every two weeks',
    );
    expect(describeCadence({ frequency: 'yearly', interval_count: 1 }, t)).toBe(
      'Every year',
    );
  });

  it('describes an interval no preset covers', () => {
    // A schedule set through the API, or imported. It has to read as something
    // rather than as a blank cell.
    expect(describeCadence({ frequency: 'monthly', interval_count: 4 }, t)).toBe(
      'Every 4 months',
    );
    expect(describeCadence({ frequency: 'weekly', interval_count: 6 }, t)).toBe(
      'Every 6 weeks',
    );
  });

  it('never reaches the plural fallback with a one in it', () => {
    // "Every 1 months" is the sentence this design exists to make impossible:
    // an interval of one is always a preset, so the fallback only renders a
    // number above one and needs no plural machinery.
    for (const frequency of Object.keys(FREQUENCY)) {
      expect(presetFor({ frequency, interval_count: 1 })).toBeDefined();
    }
  });

  it('shows the raw frequency rather than nothing when it is unknown', () => {
    expect(describeCadence({ frequency: 'daily', interval_count: 2 }, t)).toBe('daily');
  });
});

describe('whether a schedule would book anything today', () => {
  const base: Recurring = {
    id: 'r1',
    name: 'Shop rent',
    head_id: 'h1',
    amount: '12000.0000',
    currency: 'SAR',
    paid_from: 'bank',
    frequency: 'monthly',
    interval_count: 1,
    starts_on: '2026-09-01',
    next_due_on: '2026-09-01',
    is_active: true,
  };

  it('is due when the date has arrived', () => {
    expect(isDue(base, '2026-09-05')).toBe(true);
    expect(isDue(base, '2026-09-01')).toBe(true);
  });

  it('is not due before it', () => {
    expect(isDue(base, '2026-08-31')).toBe(false);
  });

  it('is not due while paused', () => {
    // The whole point of pausing rather than deleting: the schedule and its
    // history stay, and nothing is booked from it.
    expect(isDue({ ...base, is_active: false }, '2026-09-05')).toBe(false);
  });

  it('is not due past the end that was agreed', () => {
    // The generator's own condition: `ends_on IS NULL OR next_due_on <=
    // ends_on`. A row saying "due now" that the button then skips would read
    // as a broken button.
    expect(isDue({ ...base, ends_on: '2026-08-01' }, '2026-09-05')).toBe(false);
    expect(isDue({ ...base, ends_on: '2026-12-01' }, '2026-09-05')).toBe(true);
  });

  it('compares dates as the strings they arrive as', () => {
    // ISO dates sort lexically, so there is no Date to build and no timezone
    // to get wrong. A schedule due on the 1st must not read as due on the 31st
    // of the month before because the browser is west of UTC.
    expect(isDue({ ...base, next_due_on: '2026-10-01' }, '2026-09-30')).toBe(false);
    expect(isDue({ ...base, next_due_on: '2026-10-01' }, '2026-10-01')).toBe(true);
  });
});
