import { describe, expect, it } from 'vitest';

import {
  bySection,
  copyOf,
  grantableIn,
  removalBlock,
  roleProblem,
  signInState,
  tickedIn,
  ungrantable,
  type PermissionOption,
  type Person,
} from './roles';

function permission(
  name: string,
  section: string,
  holds = true,
): PermissionOption {
  return { permission: name, section, label: name, holds };
}

const CATALOGUE: PermissionOption[] = [
  permission('sales.view', 'selling'),
  permission('sales.refund', 'selling'),
  permission('catalog.view', 'stock'),
  permission('accounting.close_period', 'money', false),
  permission('identity.manage_roles', 'system', false),
];

describe('bySection', () => {
  it('puts selling before system', () => {
    const order = bySection(CATALOGUE).map((s) => s.section);
    expect(order.indexOf('selling')).toBeLessThan(order.indexOf('system'));
  });

  it('keeps the order the server sent within a section', () => {
    // 0101 gives each row a sort_order so "the dangerous ones do not land at
    // the top by alphabetical accident". Re-sorting would throw that away.
    const selling = bySection(CATALOGUE).find((s) => s.section === 'selling');
    expect(selling?.permissions.map((p) => p.permission)).toEqual([
      'sales.view',
      'sales.refund',
    ]);
  });

  it('keeps a section this build has never heard of', () => {
    // The server owns the vocabulary. A permission dropped because its heading
    // was unfamiliar is a permission nobody could grant.
    const withNew = [...CATALOGUE, permission('future.thing', 'brandnew')];
    const sections = bySection(withNew);
    expect(sections.map((s) => s.section)).toContain('brandnew');
    expect(sections[sections.length - 1]?.section).toBe('brandnew');
  });

  it('loses nothing', () => {
    const total = bySection(CATALOGUE).reduce(
      (n, s) => n + s.permissions.length,
      0,
    );
    expect(total).toBe(CATALOGUE.length);
  });
});

describe('roleProblem', () => {
  it('passes a named role with something in it', () => {
    expect(roleProblem('Floor staff', ['sales.view'], CATALOGUE)).toBe('none');
  });

  it('needs a name', () => {
    expect(roleProblem('   ', ['sales.view'], CATALOGUE)).toBe('no_name');
  });

  it('refuses a role that can do nothing', () => {
    // Not a server refusal — an unfinished form. Somebody holding an empty
    // role signs in able to do nothing, which reads as a broken account.
    expect(roleProblem('Floor staff', [], CATALOGUE)).toBe('nothing_ticked');
  });

  it('mirrors the subset rule before the server has to refuse', () => {
    expect(
      roleProblem('Sneaky', ['sales.view', 'identity.manage_roles'], CATALOGUE),
    ).toBe('cannot_grant');
  });
});

describe('ungrantable', () => {
  it('names exactly what the server would refuse', () => {
    expect(
      ungrantable(
        ['sales.view', 'identity.manage_roles', 'accounting.close_period'],
        CATALOGUE,
      ),
    ).toEqual(['identity.manage_roles', 'accounting.close_period']);
  });

  it('is empty when everything is held', () => {
    expect(ungrantable(['sales.view', 'catalog.view'], CATALOGUE)).toEqual([]);
  });
});

describe('removalBlock', () => {
  it('will not remove one the product ships', () => {
    expect(removalBlock({ is_system: true, in_use: 0 })).toBe('built_in');
  });

  it('will not remove one somebody holds', () => {
    // Cascading the assignment away would strip that person of everything.
    expect(removalBlock({ is_system: false, in_use: 3 })).toBe('in_use');
  });

  it('allows one the business made that nobody holds', () => {
    expect(removalBlock({ is_system: false, in_use: 0 })).toBe('none');
  });

  it('says built-in first, because that reason never goes away', () => {
    expect(removalBlock({ is_system: true, in_use: 11 })).toBe('built_in');
  });
});

describe('copyOf', () => {
  it('keeps only what the copier can actually grant', () => {
    // Copying the Owner role as somebody who is not the owner would otherwise
    // produce a draft that cannot be saved, refused over permissions the
    // person never chose.
    expect(
      copyOf(
        {
          permissions: [
            'sales.view',
            'identity.manage_roles',
            'accounting.close_period',
          ],
        },
        CATALOGUE,
      ),
    ).toEqual(['sales.view']);
  });

  it('drops a permission this build does not know about', () => {
    expect(copyOf({ permissions: ['nothing.known'] }, CATALOGUE)).toEqual([]);
  });
});

describe('tickedIn and grantableIn', () => {
  const selling = bySection(CATALOGUE).find((s) => s.section === 'selling')!;
  const system = bySection(CATALOGUE).find((s) => s.section === 'system')!;

  it('counts what a collapsed section is holding', () => {
    expect(tickedIn(selling, ['sales.view', 'catalog.view'])).toBe(1);
  });

  it('counts nothing when nothing is ticked', () => {
    expect(tickedIn(selling, [])).toBe(0);
  });

  it('offers only what the caller may grant', () => {
    expect(grantableIn(selling)).toEqual(['sales.view', 'sales.refund']);
    expect(grantableIn(system)).toEqual([]);
  });
});

describe('signInState', () => {
  function person(over: Partial<Person> = {}): Person {
    return {
      id: 'u1',
      email: 'a@example.test',
      full_name: 'A Person',
      status: 'active',
      must_change_password: false,
      locked: false,
      roles: [],
      ...over,
    };
  }

  it('keeps suspended apart from locked', () => {
    // One is an administrator's decision and the other the sign-in system's
    // after too many failures. What the owner does next differs.
    expect(signInState(person({ status: 'disabled' }))).toBe('suspended');
    expect(signInState(person({ locked: true }))).toBe('locked');
  });

  it('says when an account has never been collected', () => {
    expect(signInState(person({ must_change_password: true }))).toBe(
      'never_signed_in',
    );
  });

  it('is active otherwise', () => {
    expect(signInState(person())).toBe('active');
  });

  it('lets suspension win over a lock', () => {
    // A suspended account is not going to be unlocked into working.
    expect(signInState(person({ status: 'disabled', locked: true }))).toBe(
      'suspended',
    );
  });
});
