import { describe, expect, it } from 'vitest';

import {
  activityGaps,
  activityRefused,
  blockedBy,
  consentStands,
  dueOn,
  holdStands,
  incidentClock,
  pressing,
  requestClock,
  uncovered,
  type Activity,
  type Consent,
  type Hold,
  type Incident,
  type Request,
  type Retention,
} from './governance';

const request = (over: Partial<Request> = {}): Request => ({
  id: 'r1',
  request_no: 'DSR-000030',
  kind: 'access',
  status: 'received',
  subject_type: 'customer',
  subject_name: 'Al Salam Trading',
  subject_contact: '+966500000001',
  received_at: '2026-09-05T23:01:27Z',
  due_at: '2026-10-05T23:01:27Z',
  days_left: 29,
  legal_hold_applied: false,
  ...over,
});

const incident = (over: Partial<Incident> = {}): Incident => ({
  id: 'i1',
  incident_no: 'INC-000024',
  title: 'Till receipt roll left on the counter overnight',
  severity: 'low',
  status: 'open',
  what_happened: 'A roll of customer receipts was left out.',
  data_categories: 'name, phone',
  discovered_at: '2026-09-05T23:01:57Z',
  notify_due_at: '2026-09-08T23:01:57Z',
  hours_left: 71,
  ...over,
});

describe('a subject request against its deadline', () => {
  it('counts what the server counted and does not work out its own', () => {
    // The whole point: 29 is the server's answer to a statutory question.
    // Nothing here parses due_at and subtracts today.
    expect(requestClock(request({ days_left: 29 }))).toBe('running');
    expect(requestClock(request({ days_left: 7 }))).toBe('due_soon');
    expect(requestClock(request({ days_left: -1 }))).toBe('overdue');
  });

  it('treats a request waiting on the person as a different queue', () => {
    // The clock keeps running, but nobody in the shop can move it forward,
    // and a queue that mixed the two has somebody chasing work that is not
    // theirs to do.
    expect(requestClock(request({ status: 'awaiting_subject', days_left: 20 })))
      .toBe('waiting_on_subject');
  });

  it('still calls a waiting request overdue once the deadline has passed', () => {
    // Waiting on the subject is not an excuse the deadline recognises.
    expect(requestClock(request({ status: 'awaiting_subject', days_left: -3 })))
      .toBe('overdue');
  });

  it('stops counting a request that has been answered', () => {
    // There is no `closed` status; a request ends fulfilled or refused, and
    // a refusal is an answer, so it stops the clock exactly as a grant does.
    expect(requestClock(request({ status: 'fulfilled', days_left: -40 }))).toBe('settled');
    expect(requestClock(request({ status: 'refused', days_left: -40 }))).toBe('settled');
  });

  it('counts an extended request against the new date, not the original', () => {
    // Showing due_at on an extended request would have somebody reporting a
    // breach of a deadline that legitimately moved.
    expect(dueOn(request({ extended_to: '2026-11-04T23:01:27Z' })))
      .toBe('2026-11-04T23:01:27Z');
    expect(dueOn(request())).toBe('2026-10-05T23:01:27Z');
  });

  it('says when a legal hold is what is stopping it', () => {
    expect(blockedBy(request({ legal_hold_applied: true }))).toBe('legal_hold');
    expect(blockedBy(request())).toBeNull();
  });

  it('does not read day zero as no deadline', () => {
    // Number 0 is falsy. A request due today is the most urgent thing on the
    // screen, and a truthiness check would have dropped it.
    expect(requestClock(request({ days_left: 0 }))).toBe('due_soon');
  });
});

describe('a breach against its notification window', () => {
  it('measures in hours, because the window is three days and not thirty', () => {
    expect(incidentClock(incident({ hours_left: 71 }))).toBe('running');
    expect(incidentClock(incident({ hours_left: 24 }))).toBe('due_soon');
    expect(incidentClock(incident({ hours_left: -2 }))).toBe('overdue');
  });

  it('stops counting once the authority has actually been told', () => {
    // The window is a window to NOTIFY. A notified incident is still open
    // work, but it is no longer a countdown, and leaving it in the queue
    // would bury the one that has not been reported.
    expect(incidentClock(incident({ sdaia_notified_at: '2026-09-06T09:00:00Z', hours_left: -5 })))
      .toBe('settled');
    expect(incidentClock(incident({ status: 'notified', hours_left: -5 }))).toBe('settled');
  });

  it('does not treat contained as notified', () => {
    // Containing a breach stops it getting worse; it does not discharge the
    // duty to report it. Conflating the two is how a deadline is missed by a
    // shop that believes it has dealt with the problem.
    expect(incidentClock(incident({ status: 'open', hours_left: 3 }))).toBe('due_soon');
    expect(incidentClock(incident({ status: 'contained', hours_left: 3 }))).toBe('due_soon');
  });
});

describe('the queue the screen opens with', () => {
  it('leaves out everything nobody has to act on today', () => {
    const out = pressing(
      [request({ days_left: 29 }), request({ id: 'r2', status: 'fulfilled' })],
      [incident({ hours_left: 71 })],
    );
    expect(out).toEqual([]);
  });

  it('puts overdue first and a breach ahead of a request at the same urgency', () => {
    const out = pressing(
      [
        request({ id: 'soon', request_no: 'DSR-2', days_left: 2 }),
        request({ id: 'late', request_no: 'DSR-1', days_left: -1 }),
      ],
      [incident({ id: 'inc', incident_no: 'INC-1', hours_left: 5 })],
    );
    expect(out.map((p) => p.id)).toEqual(['late', 'inc', 'soon']);
  });

  it('never converts hours into days to compare them', () => {
    // An incident with 4 hours left and a request with 4 days left are both
    // urgent. Expressing one in the other's unit would invent a precision
    // the deadline does not have, so each carries its own.
    const out = pressing(
      [request({ id: 'r', days_left: 4 })],
      [incident({ id: 'i', hours_left: 4 })],
    );
    expect(out.map((p) => [p.kind, p.left, p.unit])).toEqual([
      ['incident', 4, 'hours'],
      ['request', 4, 'days'],
    ]);
  });
});

describe('a record of processing that can actually be read', () => {
  const activity = (over: Partial<Activity> = {}): Activity => ({
    id: 'a1',
    name: 'Loyalty programme',
    purpose: 'Awarding and redeeming points',
    lawful_basis: 'consent',
    data_categories: 'name, phone, purchase history',
    subject_categories: 'customers enrolled in the scheme',
    cross_border: false,
    retention_note: '3 years after the last purchase',
    owner_name: 'Store manager',
    ...over,
  });

  it('reports the refusal the server would give before it gives it', () => {
    // The database constraint refuses a cross-border entry that names no
    // destination and no safeguard. Saying so in the form is the same rule
    // stated before the refusal instead of after it.
    const sending = activity({ cross_border: true });
    expect(activityGaps(sending)).toContain('destination_country');
    expect(activityGaps(sending)).toContain('transfer_safeguard');
    expect(activityRefused(sending)).toBe(true);

    const complete = activity({
      cross_border: true,
      destination_country: 'Ireland',
      transfer_safeguard: 'Standard contractual clauses',
    });
    expect(activityRefused(complete)).toBe(false);
  });

  it('separates what makes it unreadable from what makes it refused', () => {
    // A missing owner does not stop the row being saved, and reporting it as
    // an error would teach people to ignore the errors.
    const anonymous = activity({ owner_name: '' });
    expect(activityGaps(anonymous)).toEqual(['owner_name']);
    expect(activityRefused(anonymous)).toBe(false);
  });

  it('does not accept whitespace as a destination', () => {
    expect(
      activityRefused(activity({
        cross_border: true,
        destination_country: '   ',
        transfer_safeguard: 'Standard contractual clauses',
      })),
    ).toBe(true);
  });
});

describe('what is collected but never disposed of', () => {
  const activity = (categories: string): Activity => ({
    id: Math.random().toString(),
    name: 'x',
    purpose: 'x',
    lawful_basis: 'consent',
    data_categories: categories,
    subject_categories: 'customers',
    cross_border: false,
  });
  const retention = (category: string, active = true): Retention => ({
    id: Math.random().toString(),
    data_category: category,
    retain_months: 36,
    action: 'destroy',
    is_active: active,
  });

  it('names a category the register collects and the regime never mentions', () => {
    expect(
      uncovered(
        [activity('name, phone, purchase history')],
        [retention('name'), retention('phone')],
      ),
    ).toEqual(['purchase history']);
  });

  it('compares the way a person would, since both sides are free text', () => {
    expect(uncovered([activity(' Name , PHONE ')], [retention('name'), retention('phone')]))
      .toEqual([]);
  });

  it('does not count a policy that has been switched off as cover', () => {
    // An inactive policy deletes nothing, so the data is kept for ever and
    // the register says it is handled. That is the gap this looks for.
    expect(uncovered([activity('phone')], [retention('phone', false)])).toEqual(['phone']);
  });

  it('says nothing when the register is empty rather than reporting all clear', () => {
    expect(uncovered([], [retention('name')])).toEqual([]);
  });
});

describe('records that are stamped rather than deleted', () => {
  it('refuses a consent that either column calls withdrawn', () => {
    // The server sets granted false and stamps withdrawn_at together, and the
    // schema requires them to agree, so on a healthy row either answers. Both
    // are checked because the redundancy costs nothing and being wrong means
    // marketing to somebody who said stop.
    const c = (over: Partial<Consent>): Consent => ({
      id: 'c1',
      subject_type: 'customer',
      subject_id: 's1',
      lawful_basis: 'consent',
      purpose: 'marketing',
      channel: 'sms',
      granted: true,
      granted_at: '2026-09-05T23:01:57Z',
      proof: 'Signed at the counter',
      ...over,
    });
    expect(consentStands(c({}))).toBe(true);
    expect(consentStands(c({ withdrawn_at: '2026-09-06T10:00:00Z' }))).toBe(false);
    expect(consentStands(c({ granted: false }))).toBe(false);
  });

  it('reads a released hold the same way', () => {
    const h = (over: Partial<Hold>): Hold => ({
      id: 'h1',
      name: 'Disputed return',
      reason: 'Erasure would remove the evidence.',
      placed_at: '2026-09-05T23:01:57Z',
      ...over,
    });
    expect(holdStands(h({}))).toBe(true);
    expect(holdStands(h({ released_at: '2026-09-09T10:00:00Z' }))).toBe(false);
  });
});
