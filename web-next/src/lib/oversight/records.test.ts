import { describe, expect, it } from 'vitest';

import {
  backupState,
  changeKind,
  changedFields,
  expiringSoon,
  expiryState,
  fileSize,
  restorable,
  type AuditRecord,
  type Backup,
  type Document,
} from './records';

const entry = (over: Partial<AuditRecord> = {}): AuditRecord => ({
  occurred_at: '2026-09-06T04:17:46+00:00',
  actor: 'Demo Owner',
  action: 'role_saved',
  entity_type: 'role',
  ...over,
});

describe('what an audit entry is', () => {
  it('tells a deletion from an amendment', () => {
    // Both shown as "changed" would make a removal look like an edit, which
    // is the difference between somebody adjusting a record and somebody
    // getting rid of it.
    expect(changeKind(entry({ after: { name: 'x' } }))).toBe('created');
    expect(changeKind(entry({ before: { name: 'x' } }))).toBe('removed');
    expect(changeKind(entry({ before: { name: 'x' }, after: { name: 'y' } }))).toBe('changed');
  });

  it('has a fourth case for something that changed no stored row', () => {
    // A sign-in and a period close belong in the trail and have no before or
    // after. Filing them as "created" would be a lie about what happened.
    expect(changeKind(entry({ action: 'login' }))).toBe('happened');
  });

  it('names only the fields that moved, keeping both sides as recorded', () => {
    const changes = changedFields(
      entry({
        before: { name: 'Evening cover', permissions: ['sales.view'], active: true },
        after: { name: 'Evening cover', permissions: ['sales.view', 'catalog.view'], active: true },
      }),
    );
    expect(changes).toEqual([
      {
        field: 'permissions',
        before: ['sales.view'],
        after: ['sales.view', 'catalog.view'],
      },
    ]);
  });

  it('reports a field that appeared and one that went away', () => {
    const changes = changedFields(
      entry({ before: { a: 1, gone: 'x' }, after: { a: 1, added: 'y' } }),
    );
    expect(changes.map((c) => c.field)).toEqual(['added', 'gone']);
    expect(changes[0]).toEqual({ field: 'added', before: undefined, after: 'y' });
  });

  it('says nothing rather than guessing when only one side was recorded', () => {
    // A creation has no "before" to diff against, and inventing an empty one
    // would report every field of a new record as a change.
    expect(changedFields(entry({ after: { name: 'x' } }))).toEqual([]);
    expect(changedFields(entry({}))).toEqual([]);
  });

  it('does not fall over on a recorded value that is not an object', () => {
    expect(changedFields(entry({ before: 'x', after: 'y' }))).toEqual([]);
    expect(changedFields(entry({ before: [1], after: [2] }))).toEqual([]);
  });
});

describe('a document against its own expiry', () => {
  const doc = (over: Partial<Document> = {}): Document => ({
    id: 'd1',
    entity_type: 'employee',
    entity_id: 'e1',
    file_name: 'iqama.pdf',
    content_type: 'application/pdf',
    byte_size: 148_000,
    checksum: 'abc',
    classification: 'identity',
    ...over,
  });

  it('does not read a document with no expiry as one expiring today', () => {
    // days_to_expiry is omitted entirely for a permanent document. Treating
    // the missing field as 0 would file every one of them as due this
    // morning -- the same mistake as reading an absent count as none.
    expect(expiryState(doc())).toBe('none');
    expect(expiryState(doc({ days_to_expiry: 0 }))).toBe('expiring');
  });

  it('separates already-expired from about to be', () => {
    expect(expiryState(doc({ days_to_expiry: -1 }))).toBe('expired');
    expect(expiryState(doc({ days_to_expiry: 30 }))).toBe('expiring');
    expect(expiryState(doc({ days_to_expiry: 31 }))).toBe('current');
  });

  it('lists what needs renewing soonest first, and leaves permanent ones out', () => {
    const out = expiringSoon([
      doc({ id: 'later', days_to_expiry: 20 }),
      doc({ id: 'permanent' }),
      doc({ id: 'gone', days_to_expiry: -5 }),
      doc({ id: 'fine', days_to_expiry: 200 }),
    ]);
    expect(out.map((d) => d.id)).toEqual(['gone', 'later']);
  });
});

describe('a size somebody can read', () => {
  it('drops the decimal once the number is big enough not to need it', () => {
    expect(fileSize(812)).toBe('812 B');
    // Below ten of a unit the decimal carries real information.
    expect(fileSize(5_000)).toBe('4.9 KB');
    // Above it, it is noise: nobody renewing a licence needs 144.5 rather
    // than 145.
    expect(fileSize(148_000)).toBe('145 KB');
    expect(fileSize(15_000_000)).toBe('14 MB');
  });
});

describe('a backup that ran is not a backup that restores', () => {
  const backup = (over: Partial<Backup> = {}): Backup => ({
    id: 'b1',
    kind: 'full',
    status: 'finished',
    started_at: '2026-09-06T02:00:00Z',
    finished_at: '2026-09-06T02:14:00Z',
    ...over,
  });

  it('will not call an unverified backup done', () => {
    // The whole point. A file nobody has proved can be restored is not a
    // backup, and showing it beside a verified one as "done" is how a
    // business finds out on its worst day.
    expect(backupState(backup())).toBe('unverified');
    expect(backupState(backup({ verified_at: '2026-09-06T03:00:00Z' }))).toBe('verified');
  });

  it('treats a verification that failed as a failure, not as unverified', () => {
    // A stamped attempt that came back bad is worse than never having tried:
    // it is a file known to be unreadable.
    expect(
      backupState(backup({
        verified_at: '2026-09-06T03:00:00Z',
        verify_error: 'checksum did not match',
      })),
    ).toBe('failed');
  });

  it('separates still running from finished', () => {
    expect(backupState(backup({ finished_at: undefined }))).toBe('running');
    expect(backupState(backup({ error: 'disk full' }))).toBe('failed');
    expect(backupState(backup({ status: 'failed' }))).toBe('failed');
  });

  it('counts only what could actually be restored from', () => {
    expect(
      restorable([
        backup({ id: '1', verified_at: '2026-09-06T03:00:00Z' }),
        backup({ id: '2' }),
        backup({ id: '3', error: 'disk full' }),
      ]),
    ).toBe(1);
  });
});
