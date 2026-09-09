// What a pasted rate schedule is allowed to say.
//
// This is the step between an authority's spreadsheet and every customer in a
// county being charged. The import is one transaction, so a row that cannot be
// read has to be found HERE — before the whole schedule is refused by a
// constraint name, and long before a misread column becomes a wrong rate.

import { describe, expect, it } from 'vitest';

import { parseSchedule } from './schedule';

describe('parsing a pasted rate schedule', () => {
  it('reads a tab-separated copy out of a spreadsheet', () => {
    const { rows, bad } = parseSchedule(
      'country\tUS\tUnited States\t0\ncounty\tSD\tSan Diego\tCA\t0.0775',
    );
    expect(bad).toEqual([]);
    expect(rows).toEqual([
      { level: 'country', code: 'US', name: 'United States', parent_code: '', rate: '0' },
      {
        level: 'county',
        code: 'SD',
        name: 'San Diego',
        parent_code: 'CA',
        rate: '0.0775',
      },
    ]);
  });

  it('reads commas too, for a copy that came through a CSV', () => {
    const { rows, bad } = parseSchedule('state,CA,California,US,0.06');
    expect(bad).toEqual([]);
    expect(rows[0]?.parent_code).toBe('US');
    expect(rows[0]?.rate).toBe('0.06');
  });

  // A name with a comma in it is ordinary in this data — "Rancho Santa Fe,
  // unincorporated" is a real CDTFA row. Splitting it on the comma would
  // silently shift every column right and file the rate under a parent code.
  it('prefers the tab, so a comma inside a name does not shift the columns', () => {
    const { rows } = parseSchedule(
      'city\tRSF\tRancho Santa Fe, unincorporated\tSD\t0.0775',
    );
    expect(rows[0]?.name).toBe('Rancho Santa Fe, unincorporated');
    expect(rows[0]?.rate).toBe('0.0775');
  });

  it('ignores blank lines rather than counting them as faults', () => {
    const { rows, bad } = parseSchedule('\n\nstate,CA,California,US,0.06\n\n');
    expect(bad).toEqual([]);
    expect(rows).toHaveLength(1);
  });

  it('reports the line number of anything it cannot read', () => {
    const { rows, bad } = parseSchedule(
      ['state,CA,California,US,0.06', 'nonsense', 'state,NV,Nevada,US,0.0685'].join(
        '\n',
      ),
    );
    expect(bad).toEqual([2]);
    // And the good rows are still returned, so the screen can say how many it
    // understood alongside which line it did not.
    expect(rows).toHaveLength(2);
  });

  it('refuses a rate that is not a number', () => {
    const { bad } = parseSchedule('state,CA,California,US,six per cent');
    expect(bad).toEqual([1]);
  });

  // `tax_jurisdiction_root_is_country`, held here rather than collected from
  // the database. A parentless county is a county whose state's share never
  // applies, and nothing downstream would report it.
  it('refuses a county with nothing above it', () => {
    const { bad } = parseSchedule('county,SD,San Diego,0.0775');
    expect(bad).toEqual([1]);
  });

  it('refuses a country that claims a parent', () => {
    const { bad } = parseSchedule('country,US,United States,XX,0');
    expect(bad).toEqual([1]);
  });
});
