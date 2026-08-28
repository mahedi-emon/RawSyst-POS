// The terminal's schema, checked without a terminal.
//
// # What went wrong
//
// SCHEMA was one template string and openLocalStore split it on `;`. A
// semicolon is not a statement terminator in a file that also contains English:
// the comment above local_chain reads "Unused until signing is verified; the
// server allocates the ICV today", so the split cut mid-sentence and handed
// SQLite a fragment beginning with the word "the".
//
//   error returned from database: (code: 1) near "the": syntax error
//
// openLocalStore caught it, useTerminal caught that and set the queue to null
// on the assumption it was running in a browser, and the till told the cashier
// "This terminal has no local storage, so a sale cannot be recorded safely" and
// refused to sell. Every installed terminal, on every start, from the moment
// that comment was written.
//
// Nothing saw it. The vitest suite never opened a database, the Go suite tests
// the server, and a browser has no SQL plugin so the browser path was the one
// everybody exercised. It took driving the packaged application under
// tauri-driver — e2e/tauri.mjs.
//
// # What these check
//
// Not that the SQL is valid — that needs SQLite, and e2e/tauri.mjs runs it
// against the real one. These check the SHAPE that broke: that the schema is a
// list of whole statements rather than a string somebody will split again, and
// that no entry begins with something SQLite could not parse.

import { describe, expect, it } from 'vitest';

import { SCHEMA } from './sqlite';

/** Strips SQL line comments, which is what the reader has to do too. */
const code = (statement: string) =>
  statement
    .split('\n')
    .filter((line) => !line.trim().startsWith('--'))
    .join('\n')
    .trim();

describe('the terminal schema', () => {
  it('is a list of statements, not a script to be split', () => {
    expect(Array.isArray(SCHEMA)).toBe(true);
    expect(SCHEMA.length).toBeGreaterThan(5);
  });

  it('starts every statement with a word SQLite can parse', () => {
    // The exact failure: a fragment beginning "the server allocates the ICV
    // today" is not a statement, and the only sign of it was a syntax error
    // naming a word from a comment.
    const verbs = /^(CREATE|INSERT|UPDATE|DELETE|ALTER|DROP|PRAGMA|BEGIN|COMMIT)\b/i;
    for (const statement of SCHEMA) {
      const sql = code(statement);
      expect(sql, `a schema entry does not begin with a statement:\n${sql.slice(0, 120)}`)
        .toMatch(verbs);
    }
  });

  it('puts exactly one statement in each entry', () => {
    // Two statements in one entry would work today — the plugin would run the
    // first and ignore the rest, silently — and that is the quiet half of the
    // same bug: a table that never gets created and a till that discovers it
    // mid-sale.
    for (const statement of SCHEMA) {
      const terminators = (code(statement).match(/;/g) ?? []).length;
      expect(terminators, `this entry holds ${terminators} statements:\n${code(statement).slice(0, 120)}`)
        .toBeLessThanOrEqual(1);
    }
  });

  it('still describes everything the till stores', () => {
    // A list is easy to shorten by accident in a way a string was not, so the
    // tables the offline stores read from are named here.
    const all = SCHEMA.join('\n');
    for (const table of [
      'queued_sale',
      'local_chain',
      'cached_variant',
      'catalogue_cursor',
      'cached_customer',
      'customer_cursor',
      'cached_stationery',
      'held_cart',
    ]) {
      expect(all, `the schema no longer creates ${table}`).toContain(table);
    }
  });

  it('keeps the prose that explains it', () => {
    // The comments are the reason the split broke, and deleting them would be
    // the wrong lesson to take from that. A list can hold a semicolon in a
    // sentence; the point of the fix is that it no longer matters.
    expect(SCHEMA.join('\n')).toContain('--');
  });
});
