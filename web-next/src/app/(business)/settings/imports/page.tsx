'use client';

// Bringing records in from a spreadsheet.
//
// # Staged, checked, then committed — as three visible acts
//
// The route that takes the file writes nothing; its own description says so.
// That separation is the whole value: somebody sees what would fail while
// nothing yet has. A screen that ran upload and commit together would turn a
// typo in row nine hundred into a half-imported catalogue.
//
// # The columns are the server's
//
// `/imports/shapes` says what each kind requires and accepts. Nothing here
// holds a copy: a screen with its own list asks for a column the importer
// ignores and omits one it needs, and the person finds out after uploading two
// thousand rows.
//
// # A partial import is a decision, not a button
//
// "Import 1,847 of 2,000" is something somebody chooses. "Import" is something
// they press without knowing 153 rows are about to be dropped.

import { Upload } from 'lucide-react';
import { Suspense, useRef, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel, type Tone } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import type { Exportable } from '@/lib/data/transfer';
import {
  headerColumns,
  missingColumns,
  nextAction,
  partial,
  unmapped,
  whatFailed,
  type ImportBatch,
  type Shape,
} from '@/lib/data/transfer';
import { saveAs } from '@/lib/download';
import { useT, type Key } from '@/lib/i18n/locale';


/**
 * Which column feeds which field.
 *
 * Matched on the name, case-insensitively and ignoring surrounding space,
 * because a spreadsheet exported by hand has "SKU " as often as "sku". Only
 * columns the shape knows about are mapped; anything else is reported to the
 * person as ignored rather than guessed at.
 */
function mappingFor(shape: Shape | null, columns: readonly string[]): Record<string, string> {
  if (!shape) return {};
  const byName = new Map(columns.map((c) => [c.trim().toLowerCase(), c]));
  const out: Record<string, string> = {};
  for (const field of [...shape.required, ...shape.optional]) {
    const column = byName.get(field.toLowerCase());
    if (column) out[field] = column;
  }
  return out;
}

const STATUS_TONE: Record<string, Tone> = {
  uploaded: 'info',
  validated: 'caution',
  committed: 'positive',
  failed: 'critical',
  cancelled: 'neutral',
};

const STATUS_LABEL: Record<string, Key> = {
  uploaded: 'nx.imp.uploaded',
  validated: 'nx.imp.validated',
  committed: 'nx.imp.committed',
  failed: 'nx.imp.failedState',
  cancelled: 'nx.imp.cancelled',
};

function ImportsScreen() {
  const t = useT();
  const scope = useCompanyScope();

  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [kind, setKind] = useState('');
  const [csv, setCsv] = useState('');
  const [filename, setFilename] = useState('');
  const [openID, setOpenID] = useState<string | null>(null);
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const picked = useRef<HTMLInputElement>(null);

  // One request, two answers. `/imports/shapes` carries the export list beside
  // the import shapes, so the screen holds no copy of either -- which is just
  // as well: the copy I first wrote here was of `reports.ExportKinds`, a
  // DIFFERENT list belonging to a different route, and six of the eight kinds
  // it offered did not exist here.
  const shapes = useApi<{ data: Shape[]; exports: Exportable[] }>(
    scope ? '/imports/shapes' : null,
    scope ?? undefined,
  );
  const batches = useApiList<ImportBatch>(scope ? '/imports' : null, scope ?? undefined);
  const open = useApi<ImportBatch>(
    scope && openID ? `/imports/${openID}` : null,
    scope ?? undefined,
  );

  const kinds = shapes.data?.data ?? [];
  const exportable = shapes.data?.exports ?? [];
  const rows = batches.data?.data ?? [];
  const shape = kinds.find((s) => s.kind === kind) ?? null;

  // Read from the chosen file's first line, so somebody sees whether their
  // columns line up BEFORE uploading rather than after.
  const columns = csv ? headerColumns(csv.split(/\r?\n/)[0] ?? '') : [];
  const absent = shape ? missingColumns(shape, columns) : [];
  const ignored = shape ? unmapped(shape, columns) : [];

  async function run(work: () => Promise<unknown>) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      await work();
      await batches.refetch();
      if (openID) await open.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const q = `?company_id=${scope?.company_id ?? ''}`;

  async function takeAway(kind: string) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      // Sent only when both are given. The route reads them where the export
      // covers a period and ignores them where it does not, so there is
      // nothing here that needs to know which is which.
      const range = from && to ? `&from=${from}&to=${to}` : '';
      const { blob, filename } = await api.download(
        `/exports/${kind}?company_id=${scope.company_id}${range}`,
      );
      saveAs(blob, filename);
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function choose(file: File) {
    setFilename(file.name);
    setCsv(await file.text());
  }

  const columnsSpec: Column<ImportBatch>[] = [
    {
      key: 'file',
      header: t('nx.imp.colFile'),
      primary: true,
      cell: (b) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{b.filename || t('nx.imp.noName')}</span>
          <span className="text-caption text-muted">
            {kinds.find((s) => s.kind === b.kind)?.label ?? b.kind} ·{' '}
            {b.created_at.slice(0, 16).replace('T', ' ')}
          </span>
        </span>
      ),
    },
    {
      key: 'status',
      header: t('nx.imp.colState'),
      width: 'w-36',
      cell: (b) => (
        <Badge tone={STATUS_TONE[b.status] ?? 'neutral'}>
          {t(STATUS_LABEL[b.status] ?? 'nx.imp.uploaded')}
        </Badge>
      ),
    },
    {
      key: 'rows',
      header: t('nx.imp.colRows'),
      width: 'w-52',
      cell: (b) => (
        <span className="flex flex-col gap-0.5">
          <span className="num">
            {t('nx.imp.ofRows', {
              valid: String(b.valid_rows),
              total: String(b.total_rows),
            })}
          </span>
          {b.error_rows > 0 ? (
            <span className="num text-caption text-critical-fg">
              {t('nx.imp.willBeLeft', { n: String(b.error_rows) })}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'imported',
      header: t('nx.imp.colWritten'),
      secondary: true,
      width: 'w-32',
      cell: (b) => (
        <span className="num text-muted">
          {b.committed_at ? b.imported_rows : '—'}
        </span>
      ),
    },
    {
      key: 'act',
      header: t('nx.imp.colAction'),
      width: 'w-44',
      cell: (b) => {
        const next = nextAction(b);
        return (
          <span className="flex flex-wrap gap-2">
            {next === 'check' ? (
              <Button
                variant="ghost"
                disabled={busy}
                onClick={() => void run(() => api.post(`/imports/${b.id}/validate${q}`, {}))}
              >
                {t('nx.imp.check')}
              </Button>
            ) : null}
            {next === 'commit' ? (
              <Button
                variant="primary"
                disabled={busy}
                onClick={() => void run(() => api.post(`/imports/${b.id}/commit${q}`, {}))}
              >
                {/* Says how many, because a partial import is a decision. */}
                {partial(b)
                  ? t('nx.imp.commitSome', { n: String(b.valid_rows) })
                  : t('nx.imp.commitAll')}
              </Button>
            ) : null}
            {next === 'nothing_valid' ? (
              <span className="text-caption text-muted">{t('nx.imp.nothingValid')}</span>
            ) : null}
            <Button variant="ghost" onClick={() => setOpenID(openID === b.id ? null : b.id)}>
              {t(openID === b.id ? 'nx.imp.hide' : 'nx.imp.look')}
            </Button>
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.imp.title')} description={t('nx.imp.subtitle')} />

      <FormError message={error} className="mb-4" />

      <Panel
        className="mb-6"
        title={t('nx.imp.exportTitle')}
        description={t('nx.imp.exportDesc')}
      >
        <div className="mb-4 flex flex-wrap items-end gap-3">
          <Field name="export_from" label={t('nx.imp.from')} hint={t('nx.imp.periodHint')}>
            <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
          </Field>
          <Field name="export_to" label={t('nx.imp.to')}>
            <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
          </Field>
        </div>
        <ul className="flex flex-wrap gap-2">
          {exportable.map((x) => (
            <li key={x.kind}>
              {/* The server's own label. It names what is in the file --
                  "Sales, line by line" rather than "Sales" -- which is the
                  difference between choosing one and guessing. */}
              <Button disabled={busy} onClick={() => void takeAway(x.kind)}>
                {x.label}
              </Button>
            </li>
          ))}
        </ul>
      </Panel>

      <Panel
        className="mb-6"
        title={t('nx.imp.stageTitle')}
        description={t('nx.imp.stageDesc')}
      >
        <div className="flex flex-wrap items-end gap-3">
          <Field name="kind" label={t('nx.imp.what')}>
            <Select value={kind} onChange={(e) => setKind(e.target.value)}>
              <option value="">{t('nx.imp.chooseKind')}</option>
              {kinds.map((s) => (
                <option key={s.kind} value={s.kind}>
                  {s.label}
                </option>
              ))}
            </Select>
          </Field>
          <Field name="file" label={t('nx.imp.chooseFile')}>
            <Input
              ref={picked}
              type="file"
              accept=".csv,text/csv"
              onChange={(e) => {
                const file = e.target.files?.[0];
                if (file) void choose(file);
              }}
            />
          </Field>
          <Button
            variant="primary"
            disabled={busy || !shape || !csv || absent.length > 0}
            onClick={() =>
              void run(async () => {
                await api.post(`/imports${q}`, {
                  kind,
                  filename,
                  // Field to column, which is the direction the server reads.
                  // An empty mapping is refused with "this file has nothing
                  // mapped to name, sku" -- rightly, because a file whose
                  // columns happen to be named the same thing is a
                  // coincidence, not an instruction.
                  mapping: mappingFor(shape, columns),
                  csv,
                });
                if (picked.current) picked.current.value = '';
                setCsv('');
                setFilename('');
              })
            }
          >
            {t('nx.imp.stage')}
          </Button>
        </div>

        {shape ? (
          <dl className="mt-4 grid gap-4 sm:grid-cols-2">
            <div>
              <dt className="text-label text-muted">{t('nx.imp.needs')}</dt>
              <dd className="mt-1 flex flex-wrap gap-1.5">
                {shape.required.map((c) => (
                  <Badge key={c} tone={absent.includes(c) ? 'critical' : 'neutral'}>
                    {c}
                  </Badge>
                ))}
              </dd>
            </div>
            <div>
              <dt className="text-label text-muted">{t('nx.imp.accepts')}</dt>
              <dd className="mt-1 flex flex-wrap gap-1.5">
                {shape.optional.map((c) => (
                  <Badge key={c}>{c}</Badge>
                ))}
              </dd>
            </div>
          </dl>
        ) : null}

        {csv && absent.length > 0 ? (
          <p className="mt-3 max-w-prose text-body text-critical-fg">
            {t('nx.imp.missingColumns', { columns: absent.join(', ') })}
          </p>
        ) : null}
        {csv && ignored.length > 0 ? (
          // Not an error. But a column that was MEANT to map and was spelled
          // differently looks exactly like one nobody wanted.
          <p className="mt-3 max-w-prose text-caption text-muted">
            {t('nx.imp.ignoredColumns', { columns: ignored.join(', ') })}
          </p>
        ) : null}
      </Panel>

      {open.data && openID ? (
        <Panel
          className="mb-6"
          title={t('nx.imp.whatFailedTitle')}
          actions={
            <Button variant="ghost" onClick={() => setOpenID(null)}>
              {t('nx.imp.hide')}
            </Button>
          }
        >
          {whatFailed(open.data).length === 0 ? (
            <p className="max-w-prose text-body text-muted">{t('nx.imp.nothingFailed')}</p>
          ) : (
            <>
              <ul className="flex flex-col divide-y divide-line">
                {whatFailed(open.data).map((r) => (
                  <li key={r.row_no} className="flex flex-wrap gap-3 py-2 first:pt-0">
                    <span className="num text-caption text-muted">
                      {t('nx.imp.rowNo', { n: String(r.row_no) })}
                    </span>
                    {/* The importer's own reason. The schema requires a
                        rejected row to carry one, so there is always
                        something to show. */}
                    <span className="min-w-0 flex-1 text-body">{r.error}</span>
                  </li>
                ))}
              </ul>
              {open.data.error_rows > whatFailed(open.data).length ? (
                <p className="mt-3 text-caption text-muted">
                  {t('nx.imp.andMore', {
                    n: String(open.data.error_rows - whatFailed(open.data).length),
                  })}
                </p>
              ) : null}
            </>
          )}
        </Panel>
      ) : null}

      {batches.error ? (
        <ErrorState error={batches.error} onRetry={() => void batches.refetch()} />
      ) : null}
      {batches.isLoading && !batches.data ? <TableSkeleton columns={5} /> : null}

      {!batches.isLoading && !batches.error && rows.length === 0 ? (
        <EmptyState
          icon={Upload}
          title={t('nx.imp.emptyTitle')}
          description={t('nx.imp.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.imp.title')}
          columns={columnsSpec}
          rows={rows}
          rowKey={(b) => b.id}
        />
      ) : null}
    </>
  );
}

export default function ImportsPage() {
  return (
    <RequirePermission anyOf={['data.import']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ImportsScreen />
      </Suspense>
    </RequirePermission>
  );
}
