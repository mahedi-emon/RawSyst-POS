// Reading a rate schedule an authority published as a spreadsheet.
//
// This is the step between a state's own file and every customer in a county
// being charged. CDTFA issues a spreadsheet each quarter rather than an API, so
// somebody copies the columns and pastes them in; the import applies the result
// in ONE transaction, which means a row that cannot be read has to be found
// here — before the whole schedule is refused by a constraint name, and long
// before a misread column becomes a wrong rate on a receipt.

interface ImportRow {
  level: string;
  code: string;
  name: string;
  parent_code: string;
  rate: string;
}

/**
 * Parses a pasted schedule.
 *
 * Returns the rows, or the line numbers that could not be read. Never a partial
 * result: an import that silently dropped the rows it did not understand would
 * leave a county untaxed and nothing would say so.
 */
export function parseSchedule(text: string): {
  rows: ImportRow[];
  bad: number[];
} {
  const rows: ImportRow[] = [];
  const bad: number[] = [];

  text.split(/\r?\n/).forEach((raw, i) => {
    const line = raw.trim();
    if (line === '') return;
    // Tab first: a spreadsheet copy is tab-separated, and a name with a comma
    // in it ("Rancho Santa Fe, unincorporated") would otherwise split wrongly.
    const parts = (line.includes('\t') ? line.split('\t') : line.split(',')).map(
      (c) => c.trim(),
    );
    if (parts.length < 4) {
      bad.push(i + 1);
      return;
    }
    const [level = '', code = '', name = '', third = '', fourth = ''] = parts;
    // Four columns means no parent; five means the fourth is the parent code.
    const parentCode = parts.length >= 5 ? third : '';
    const rate = parts.length >= 5 ? fourth : third;
    if (
      level === '' ||
      code === '' ||
      name === '' ||
      rate === '' ||
      Number.isNaN(Number(rate)) ||
      // The same rule the database holds: a country is the root of its own
      // tree, and everything else hangs off something. A parentless county in
      // a pasted schedule is a county whose state's share never applies, and
      // the import is one transaction — so it is caught here, before the whole
      // schedule is refused by a constraint name.
      (level === 'country') !== (parentCode === '')
    ) {
      bad.push(i + 1);
      return;
    }
    rows.push({ level, code, name, parent_code: parentCode, rate });
  });

  return { rows, bad };
}
