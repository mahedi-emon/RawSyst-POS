// Bringing records in from a spreadsheet, and taking them out again.
//
// # The columns come from the server
//
// `/imports/shapes` says which kinds can be imported and, for each, the columns
// it requires and the ones it will accept. Nothing here holds that list. A
// screen with its own copy offers a column the importer ignores and omits one
// it needs, and the person finds out after uploading two thousand rows.
//
// # Staged, checked, then committed
//
// An upload is three separate acts, and the middle one is the point: a file is
// validated before anything is written, so somebody can see what would fail
// while nothing has yet. A screen that ran them together would turn a typo in
// row nine hundred into a half-imported catalogue.

export interface Shape {
  kind: string;
  label: string;
  required: string[];
  optional: string[];
}

export interface ImportBatch {
  id: string;
  kind: string;
  filename?: string;
  status: string;
  mapping: Record<string, string>;
  total_rows: number;
  valid_rows: number;
  error_rows: number;
  imported_rows: number;
  created_by?: string;
  created_at: string;
  committed_at?: string;
  rows?: ImportRow[];
}

export interface ImportRow {
  row_no: number;
  raw: Record<string, string>;
  status: string;
  /** A row the importer rejected always says why; the schema requires it. */
  error?: string;
}

export type Stage = 'uploaded' | 'validated' | 'committed' | 'failed' | 'cancelled';

/**
 * What can be done to this batch next.
 *
 * `commit` is offered only on a batch that has been checked AND has something
 * worth writing. Committing a file where every row failed writes nothing and
 * reads as success, which is the worst of the four outcomes.
 */
export function nextAction(
  batch: ImportBatch,
): 'check' | 'commit' | 'nothing_valid' | 'done' | 'stopped' {
  if (batch.status === 'committed') return 'done';
  if (batch.status === 'failed' || batch.status === 'cancelled') return 'stopped';
  if (batch.status === 'uploaded') return 'check';
  if (batch.valid_rows === 0) return 'nothing_valid';
  return 'commit';
}

/**
 * Whether some rows would be written and others left behind.
 *
 * Worth saying out loud before committing. "Import 1,847 of 2,000" is a
 * decision; "Import" is a button somebody presses without knowing 153 rows are
 * about to be dropped.
 */
export function partial(batch: ImportBatch): boolean {
  return batch.error_rows > 0 && batch.valid_rows > 0;
}

/**
 * Columns in the file that the importer has nothing to do with.
 *
 * Not an error — a spreadsheet exported from another system carries all sorts
 * of things — but worth naming, because a column that was MEANT to map and was
 * spelled differently looks exactly like a column nobody wanted.
 */
export function unmapped(shape: Shape, columns: readonly string[]): string[] {
  const known = new Set([...shape.required, ...shape.optional]);
  return columns.filter((c) => !known.has(c));
}

/**
 * Required columns the file does not have.
 *
 * The refusal stated before the upload rather than after it.
 */
export function missingColumns(shape: Shape, columns: readonly string[]): string[] {
  const present = new Set(columns);
  return shape.required.filter((c) => !present.has(c));
}

/**
 * The first few rows that failed, with their reasons.
 *
 * Capped, because a file where everything failed produces two thousand
 * identical messages and the useful information is the first one. The count is
 * reported separately so nothing is hidden by the cap.
 */
export function whatFailed(batch: ImportBatch, limit = 10): ImportRow[] {
  return (batch.rows ?? []).filter((r) => r.status === 'invalid').slice(0, limit);
}

/**
 * Splits a pasted or uploaded CSV header line into columns.
 *
 * Deliberately simple: it is used to tell somebody which columns their file
 * appears to have before they upload it, not to parse the file. The importer
 * on the server does that, and it is the one whose answer counts.
 */
export function headerColumns(line: string): string[] {
  return line
    .split(',')
    .map((c) => c.trim().replace(/^"|"$/g, '').trim())
    .filter(Boolean);
}

/**
 * One thing that can be taken away as a spreadsheet.
 *
 * Published alongside the import shapes on `/imports/shapes`, so a screen
 * offering exports holds no list of its own. Worth saying why that matters: a
 * copy of the WRONG list is indistinguishable from a copy of the right one
 * until somebody clicks, and `reports.ExportKinds` is a different list on a
 * different route with overlapping names.
 */
export interface Exportable {
  kind: string;
  label: string;
  /** What the download is called, so four of them are tellable apart. */
  filename: string;
}
