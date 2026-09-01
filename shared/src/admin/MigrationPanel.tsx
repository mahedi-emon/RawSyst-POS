// The migration wizard and the exports (blueprint H7).
//
// # Three presses, and the middle one is the point
//
// Upload stages and checks. Nothing has moved. The Error Report is read, the
// file is corrected, and only then does Write commit — in one transaction, so
// every valid row lands or none does. A single "import this" button would
// remove exactly the step that makes a bad file recoverable.
//
// # The mapping is built from the file's own header
//
// The uploaded text is parsed here for its first line only, so the screen can
// offer the shop's column names against this product's fields. The rows
// themselves are never interpreted in the browser.

import { useCallback, useState } from 'react';

import {
  cancelImport,
  commitImport,
  exportURL,
  importShapes,
  listImports,
  readImport,
  uploadImport,
  type ImportBatch,
  type ImportShape,
} from '../api/admin';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Field, FormError, SelectInput } from '../ui/Form';
import { shortDate } from '../ui/format';

export function MigrationPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();

  const load = useCallback(
    () => importShapes(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(payload: {
        data: ImportShape[];
        exports: Array<{ kind: string; label: string; filename: string }>;
      }) => (
        <>
          <ExportsPanel companyId={companyId} exports={payload.exports} />
          <ImportPanel companyId={companyId} shapes={payload.data} />
        </>
      )}
    </RemoteBody>
  );
}

function ExportsPanel({
  companyId,
  exports,
}: {
  companyId: string;
  exports: Array<{ kind: string; label: string; filename: string }>;
}) {
  const { can } = useAuth();
  const t = useT();

  if (!can('data.export')) return null;

  return (
    <section className="ds-panel" aria-label={t('adm.exports')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('adm.exports')}</h2>
          <p className="ds-caption">{t('adm.exportsHint')}</p>
        </div>
      </div>
      <div className="ds-panel__body adm__exports">
        {exports.map((x) => (
          // A link rather than a fetch: saving a file is the browser's job,
          // and streaming a large export through JavaScript would hold the
          // whole thing in memory to hand it straight back.
          <a
            key={x.kind}
            className="ds-btn ds-btn--quiet"
            href={exportURL(companyId, x.kind)}
            download={x.filename}
          >
            {x.label}
          </a>
        ))}
      </div>
    </section>
  );
}

function ImportPanel({
  companyId,
  shapes,
}: {
  companyId: string;
  shapes: ImportShape[];
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayImport = can('data.import');
  const load = useCallback(
    () => listImports(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [open, setOpen] = useState<ImportBatch | null>(null);
  const [uploading, setUploading] = useState(false);

  if (open) {
    return (
      <BatchDetail
        companyId={companyId}
        batch={open}
        onBack={() => {
          setOpen(null);
          reload();
        }}
      />
    );
  }

  return (
    <>
      {uploading && (
        <UploadForm
          companyId={companyId}
          shapes={shapes}
          onCancel={() => setUploading(false)}
          onStaged={(b) => {
            setUploading(false);
            setOpen(b);
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('adm.imports')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('adm.imports')}</h2>
            <p className="ds-caption">{t('adm.importsHint')}</p>
          </div>
          {mayImport && !uploading && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setUploading(true)}
            >
              {t('adm.startImport')}
            </button>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: ImportBatch[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('adm.noImportsTitle')}
                  body={t('adm.noImportsBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('adm.file')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col" className="num">
                        {t('adm.rows')}
                      </th>
                      <th scope="col" className="num">
                        {t('adm.inError')}
                      </th>
                      <th scope="col" className="num">
                        {t('adm.written')}
                      </th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((b) => (
                      <tr key={b.id}>
                        <td>
                          <span className="detail__strong">
                            {b.filename ?? t(`adm.kind.${b.kind}` as Key)}
                          </span>
                          <span className="ds-caption">
                            {t(`adm.kind.${b.kind}` as Key)} ·{' '}
                            {shortDate(b.created_at, locale)}
                          </span>
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${importBadge(b.status)}`}
                          >
                            {t(`adm.importStatus.${b.status}` as Key)}
                          </span>
                        </td>
                        <td className="num">{b.total_rows}</td>
                        <td className="num">
                          {b.error_rows > 0 ? (
                            <span className="adm__errorcount">{b.error_rows}</span>
                          ) : (
                            '—'
                          )}
                        </td>
                        <td className="num">{b.imported_rows}</td>
                        <td>
                          <button
                            className="ds-btn ds-btn--quiet"
                            onClick={() => setOpen(b)}
                          >
                            {t('action.view')}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          }
        </RemoteBody>
      </section>
    </>
  );
}

function importBadge(status: string): string {
  switch (status) {
    case 'committed':
      return 'success';
    case 'failed':
    case 'cancelled':
      return 'neutral';
    case 'validated':
      return 'warning';
    default:
      return 'info';
  }
}

function UploadForm({
  companyId,
  shapes,
  onCancel,
  onStaged,
}: {
  companyId: string;
  shapes: ImportShape[];
  onCancel: () => void;
  onStaged: (b: ImportBatch) => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [kind, setKind] = useState(shapes[0]?.kind ?? 'customers');
  const [filename, setFilename] = useState('');
  const [csv, setCsv] = useState('');
  const [mapping, setMapping] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const shape = shapes.find((s) => s.kind === kind);
  // The header row only, and an empty file is a real case: a shop that
  // has chosen nothing yet has no columns to map. The rows themselves
  // are never interpreted here.
  const header = (csv.split(/\r?\n/, 1)[0] ?? '')
    .split(',')
    .map((h) => h.trim().replace(/^"|"$/g, ''))
    .filter((h) => h !== '');

  async function pickFile(file: File) {
    setFilename(file.name);
    setCsv(await file.text());
    setMapping({});
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      onStaged(
        await uploadImport(client, companyId, {
          kind,
          filename: filename || undefined,
          mapping,
          csv,
        }),
      );
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  const fields = shape ? [...shape.required, ...shape.optional] : [];

  return (
    <form className="ds-panel adm__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('adm.startImport')}</h2>
        <p className="ds-caption">{t('adm.importFlow')}</p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field label={t('adm.whatKind')} htmlFor="im-kind" required>
            <SelectInput
              id="im-kind"
              value={kind}
              onChange={(v) => {
                setKind(v);
                setMapping({});
              }}
              options={shapes.map((s) => ({ id: s.kind, label: s.label }))}
              label={(s) => (s as { label: string }).label}
            />
          </Field>
          <Field label={t('adm.chooseFile')} htmlFor="im-file" required>
            <input
              id="im-file"
              type="file"
              accept=".csv,text/csv"
              className="field__input"
              onChange={(e) => {
                const file = e.target.files?.[0];
                if (file) void pickFile(file);
              }}
            />
          </Field>
        </div>

        {shape && (
          <p className="ds-caption">
            {t('adm.needsFields', { fields: shape.required.join(', ') })}
          </p>
        )}

        {header.length > 0 && (
          <div className="ds-scroll-x">
            <h3 className="ds-h3">{t('adm.mapColumns')}</h3>
            <table className="ds-table">
              <thead>
                <tr>
                  <th scope="col">{t('adm.yourColumn')}</th>
                  <th scope="col">{t('adm.ourField')}</th>
                </tr>
              </thead>
              <tbody>
                {header.map((column) => (
                  <tr key={column}>
                    <td>{column}</td>
                    <td>
                      <select
                        className="input"
                        aria-label={column}
                        value={mapping[column] ?? ''}
                        onChange={(e) =>
                          setMapping((prev) => {
                            const next = { ...prev };
                            if (e.target.value === '') delete next[column];
                            else next[column] = e.target.value;
                            return next;
                          })
                        }
                      >
                        <option value="">{t('adm.ignoreColumn')}</option>
                        {fields.map((f) => (
                          <option key={f} value={f}>
                            {f}
                            {shape?.required.includes(f) ? ' *' : ''}
                          </option>
                        ))}
                      </select>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <div className="form__actions">
          <button
            className="ds-btn ds-btn--primary"
            type="submit"
            disabled={busy || csv.trim() === ''}
          >
            {t('adm.checkFile')}
          </button>
          <button className="ds-btn ds-btn--quiet" type="button" onClick={onCancel}>
            {t('action.cancel')}
          </button>
        </div>
      </div>
    </form>
  );
}

function BatchDetail({
  companyId,
  batch,
  onBack,
}: {
  companyId: string;
  batch: ImportBatch;
  onBack: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();

  const mayImport = can('data.import');
  const load = useCallback(
    () => readImport(client, companyId, batch.id),
    [client, companyId, batch.id],
  );
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function run(work: () => Promise<unknown>) {
    setBusy(true);
    setFailure(null);
    try {
      await work();
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(b: ImportBatch) => (
        <section className="ds-panel">
          <div className="ds-panel__head">
            <div>
              <button className="ds-btn ds-btn--quiet" onClick={onBack}>
                {t('action.back')}
              </button>
              <h2 className="ds-h3">
                {b.filename ?? t(`adm.kind.${b.kind}` as Key)}
              </h2>
              <p className="ds-caption">
                {t('adm.checkedSummary', {
                  valid: String(b.valid_rows),
                  errors: String(b.error_rows),
                  total: String(b.total_rows),
                })}
              </p>
            </div>

            <div className="adm__rowactions">
              {mayImport && b.status === 'validated' && (
                <>
                  <button
                    className="ds-btn ds-btn--primary"
                    disabled={busy || b.valid_rows === 0}
                    onClick={() =>
                      void run(() => commitImport(client, companyId, b.id))
                    }
                  >
                    {t('adm.writeRows', { count: String(b.valid_rows) })}
                  </button>
                  <button
                    className="ds-btn ds-btn--quiet"
                    disabled={busy}
                    onClick={() =>
                      void run(() => cancelImport(client, companyId, b.id))
                    }
                  >
                    {t('adm.abandon')}
                  </button>
                </>
              )}
            </div>
          </div>

          <div className="ds-panel__body">
            <FormError message={failure} />
            {b.status === 'committed' && (
              <p className="ds-caption" role="status">
                {t('adm.wrote', { count: String(b.imported_rows) })}
              </p>
            )}
            {/* Said where somebody is about to press Write. */}
            {b.status === 'validated' && (
              <p className="ds-caption">{t('adm.allOrNothing')}</p>
            )}
          </div>

          <div className="ds-panel__body ds-scroll-x">
            <h3 className="ds-h3">{t('adm.errorReport')}</h3>
            <table className="ds-table">
              <thead>
                <tr>
                  <th scope="col">{t('adm.rowNo')}</th>
                  <th scope="col">{t('common.status')}</th>
                  <th scope="col">{t('adm.whatIsWrong')}</th>
                  <th scope="col">{t('adm.theRow')}</th>
                </tr>
              </thead>
              <tbody>
                {(b.rows ?? []).map((r) => (
                  <tr
                    key={r.row_no}
                    className={r.status === 'invalid' ? 'adm__row--bad' : undefined}
                  >
                    <td>{r.row_no}</td>
                    <td>
                      <span
                        className={`ds-badge ds-badge--${rowBadge(r.status)}`}
                      >
                        {t(`adm.rowStatus.${r.status}` as Key)}
                      </span>
                    </td>
                    <td>{r.error ?? '—'}</td>
                    <td className="adm__raw">
                      {Object.entries(r.raw)
                        .map(([k, v]) => `${k}: ${v}`)
                        .join(' · ')}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </RemoteBody>
  );
}

function rowBadge(status: string): string {
  switch (status) {
    case 'imported':
      return 'success';
    case 'invalid':
      return 'danger';
    case 'valid':
      return 'info';
    default:
      return 'neutral';
  }
}
