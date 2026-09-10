// What the Backup & Recovery screen is allowed to say.
//
// These are not tests of rendering. They are tests of a claim: that no state
// short of "this was restored and compared" is ever shown in the colour a
// person reads as safe, and that every state the database can hold has a
// sentence. Both have failed in real products, and the second one fails
// silently — a phase with no entry renders as a key, or as nothing at all, on
// the screen somebody opens during an outage.

import { describe, expect, it } from 'vitest';

import { en } from '@rawsyst/shared/i18n/strings';

import { PHASE_LABEL, PHASE_TONE, STAGE_LABEL } from './backups';

/**
 * Every phase `backup_record` may hold.
 *
 * Copied from the CHECK constraint in migration 0135 rather than derived from
 * the maps under test, which would make this a test of nothing. When the
 * database learns a phase, this list is where the failure appears.
 */
const PHASES = [
  'pending',
  'creating',
  'uploading',
  'uploaded',
  'verifying',
  'verified',
  'invalid',
  'failed',
  'restore_validating',
  'restore_ready',
  'restoring',
  'restored',
  'restore_failed',
];

/** Every stage the agent writes. From the constants in internal/backup. */
const STAGES = [
  'pending',
  'preparing',
  'dumping',
  'inventory',
  'sealing',
  'uploading',
  'manifest',
  'verifying',
  'restoring',
  'checking',
  'cleaning_up',
  'done',
];

describe('the backup vocabulary', () => {
  it('has a sentence for every phase the database can hold', () => {
    const missing = PHASES.filter((p) => !PHASE_LABEL[p]);
    expect(missing, 'phases with no label').toEqual([]);
  });

  it('has a colour for every phase the database can hold', () => {
    const missing = PHASES.filter((p) => !PHASE_TONE[p]);
    expect(missing, 'phases with no tone').toEqual([]);
  });

  it('has a sentence for every stage the agent writes', () => {
    const missing = STAGES.filter((s) => !STAGE_LABEL[s]);
    expect(missing, 'stages with no label').toEqual([]);
  });

  it('translates every label it names', () => {
    // A key in the map that the catalogue has never heard of renders as the
    // key itself. On this screen that would be the word `nx.pbk.phase.invalid`
    // where the reason a backup cannot be trusted should be.
    const keys = [...Object.values(PHASE_LABEL), ...Object.values(STAGE_LABEL)];
    const unknown = keys.filter((k) => !(k in en));
    expect(unknown, 'labels with no English string').toEqual([]);
  });

  /**
   * The one that matters.
   *
   * A backup that ran and a backup that restores are different claims, and the
   * green word belongs only to the second. `uploaded` is the artifact sitting
   * in the bucket with nothing proved about it; if it were ever tinted the way
   * `verified` is, the screen would be telling a business it is protected on
   * the strength of an upload that returned 200.
   */
  it('gives the safe colour only to a backup that has been restored', () => {
    const safe = Object.entries(PHASE_TONE)
      .filter(([, tone]) => tone === 'positive')
      .map(([phase]) => phase)
      .sort();

    expect(safe).toEqual(['restore_ready', 'restored', 'verified']);

    // Spelled out rather than left to the assertion above, because the point
    // is the reasoning and not the list: each of these three has had a dump
    // pulled down, restored into a database and compared against its manifest.
    expect(PHASE_TONE.uploaded).not.toBe('positive');
    expect(PHASE_TONE.creating).not.toBe('positive');
    expect(PHASE_TONE.uploading).not.toBe('positive');
    expect(PHASE_TONE.pending).not.toBe('positive');
  });

  it('never reads a failure as anything but a failure', () => {
    for (const phase of ['invalid', 'failed', 'restore_failed']) {
      expect(PHASE_TONE[phase], phase).toBe('critical');
    }
  });

  /**
   * The words themselves, in English, because the difference has to survive
   * translation and the English is what the other two are written from.
   *
   * "Not checked yet" and "Proved to restore" cannot be allowed to converge.
   */
  it('keeps "not checked" and "proved to restore" apart in words', () => {
    const uploaded = en[PHASE_LABEL.uploaded];
    const verified = en[PHASE_LABEL.verified];

    expect(uploaded).not.toEqual(verified);
    expect(uploaded.toLowerCase()).toContain('not checked');
    expect(verified.toLowerCase()).toContain('proved to restore');
  });

  it('shows no stage as a percentage', () => {
    // The agent reports what is happening, never how far through it is:
    // neither pg_dump nor an S3 PUT reports progress, so any number here would
    // be invented. A digit in one of these labels is the beginning of that.
    for (const key of Object.values(STAGE_LABEL)) {
      expect(en[key], key).not.toMatch(/\d/);
      expect(en[key], key).not.toContain('%');
    }
  });
});
