// The central document store (blueprint D6).
//
// # One list, searched or filtered by what it is attached to
//
// D6 asks for central, searchable storage. A shop looking for "the CR
// certificate we scanned last March" does not remember which screen they were
// on when they scanned it, so the default view is everything, newest first,
// with a search box over file names and notes.
//
// # Expiring documents are the reason the store has a date
//
// A supplier's VAT certificate and an employee's Iqama both lapse, and a shop
// finds out when somebody asks for it. The Expiring view is the same list with
// a window applied, and it is what the compliance screen counts.
//
// # A personal file is downloaded, never previewed
//
// The link is an ordinary anchor the browser follows. The server sends it as an
// attachment with caching turned off for anything classified personal, and
// records who read it — which is what E4 asks a controller to be able to say.

import { useCallback, useRef, useState } from 'react';

import {
  documentFileUrl,
  expiringDocuments,
  listDocuments,
  removeDocument,
  uploadDocument,
  type DataClass,
  type StoredDocument,
} from '../api/governance';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError } from '../ui/Form';
import { shortDate } from '../ui/format';
import { LabelledSelect, LabelledText } from './fields';

/** D6's attachment points, as a person would name them. */
const ENTITY_TYPES = [
  'customer',
  'supplier',
  'employee',
  'purchase_invoice',
  'expense',
  'sales_invoice',
  'service_job',
  'asset',
  'installment_plan',
];

const CLASSES: DataClass[] = [
  'public',
  'internal',
  'personal',
  'sensitive_personal',
];

export function DocumentsPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const mayManage = can('document.manage');

  const [view, setView] = useState<'all' | 'expiring'>('all');
  const [term, setTerm] = useState('');

  const load = useCallback(
    () =>
      view === 'expiring'
        ? expiringDocuments(client, companyId, 60)
        : listDocuments(client, companyId, { q: term.trim() || undefined }),
    [client, companyId, view, term],
  );
  const { remote, reload } = useRemote(load);

  const [adding, setAdding] = useState(false);
  const [entityType, setEntityType] = useState('customer');
  const [entityId, setEntityId] = useState('');
  const [classification, setClassification] = useState<DataClass | ''>('');
  const [expiresOn, setExpiresOn] = useState('');
  const [note, setNote] = useState('');
  const [fileName, setFileName] = useState('');
  const [data, setData] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const picker = useRef<HTMLInputElement | null>(null);

  function pick(file: File | undefined) {
    if (!file) return;
    setFileName(file.name);
    const reader = new FileReader();
    reader.onload = () => {
      // FileReader hands back `data:<type>;base64,<payload>`. The server sniffs
      // the type from the bytes and refuses to be told what it is, so only the
      // payload is sent.
      const result = String(reader.result ?? '');
      const comma = result.indexOf(',');
      setData(comma >= 0 ? result.slice(comma + 1) : '');
    };
    reader.readAsDataURL(file);
  }

  async function upload() {
    setBusy(true);
    setFailure(null);
    try {
      await uploadDocument(client, companyId, {
        entity_type: entityType,
        entity_id: entityId.trim(),
        file_name: fileName,
        data,
        classification: classification === '' ? undefined : classification,
        expires_on: expiresOn || undefined,
        note: note || undefined,
      });
      setAdding(false);
      setEntityId('');
      setFileName('');
      setData('');
      setNote('');
      setExpiresOn('');
      if (picker.current) picker.current.value = '';
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function drop(id: string) {
    setBusy(true);
    setFailure(null);
    try {
      await removeDocument(client, companyId, id);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('doc.title')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('doc.title')}</h2>
          <p className="ds-caption">{t('doc.intro')}</p>
        </div>
        <div className="ds-panel__actions">
          <div
            className="segmented"
            role="group"
            aria-label={t('common.whatToShow')}
          >
            <button
              className={`segmented__btn${view === 'all' ? ' segmented__btn--on' : ''}`}
              aria-pressed={view === 'all'}
              onClick={() => setView('all')}
            >
              {t('doc.all')}
            </button>
            <button
              className={`segmented__btn${view === 'expiring' ? ' segmented__btn--on' : ''}`}
              aria-pressed={view === 'expiring'}
              onClick={() => setView('expiring')}
            >
              {t('doc.expiring')}
            </button>
          </div>
          {mayManage && (
            <button
              className="ds-btn ds-btn--primary"
              onClick={() => setAdding((v) => !v)}
            >
              {t('doc.attach')}
            </button>
          )}
        </div>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />

        {view === 'all' && (
          <div className="doc__search">
            <LabelledText
              id="doc-q"
              label={t('doc.search')}
              value={term}
              onChange={setTerm}
              placeholder={t('doc.searchHint')}
            />
          </div>
        )}

        {adding && (
          <div className="pri__form">
            <LabelledSelect
              id="doc-entity"
              label={t('doc.attachedTo')}
              value={entityType}
              onChange={setEntityType}
              options={ENTITY_TYPES.map((e) => ({
                value: e,
                label: t(`doc.entity.${e}` as Key),
              }))}
            />
            <LabelledText
              id="doc-entity-id"
              label={t('doc.recordReference')}
              hint={t('doc.recordReferenceHint')}
              value={entityId}
              onChange={setEntityId}
            />
            <div className="field">
              <label className="field__label" htmlFor="doc-file">
                {t('doc.file')}
              </label>
              <input
                id="doc-file"
                className="input"
                type="file"
                ref={picker}
                onChange={(e) => pick(e.target.files?.[0])}
              />
            </div>
            <LabelledSelect
              id="doc-class"
              label={t('doc.classification')}
              hint={t('doc.classificationHint')}
              value={classification}
              onChange={(v) => setClassification(v as DataClass | '')}
              options={[
                { value: '', label: t('doc.decideForMe') },
                ...CLASSES.map((c) => ({
                  value: c,
                  label: t(`doc.class.${c}` as Key),
                })),
              ]}
            />
            <LabelledText
              id="doc-expires"
              label={t('doc.expiresOn')}
              hint={t('doc.expiresHint')}
              value={expiresOn}
              onChange={setExpiresOn}
              type="date"
            />
            <LabelledText
              id="doc-note"
              label={t('doc.note')}
              value={note}
              onChange={setNote}
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={busy || data === '' || entityId.trim() === ''}
                onClick={() => void upload()}
              >
                {t('doc.store')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setAdding(false)}
              >
                {t('action.cancel')}
              </button>
            </div>
          </div>
        )}
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: StoredDocument[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('doc.noneTitle')}
                body={t('doc.noneBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('doc.file')}</th>
                    <th scope="col">{t('doc.attachedTo')}</th>
                    <th scope="col">{t('doc.classification')}</th>
                    <th scope="col">{t('doc.expiresOn')}</th>
                    <th scope="col">{t('doc.added')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((d) => (
                    <tr key={d.id}>
                      <td>
                        <a
                          className="ds-link"
                          href={documentFileUrl(companyId, d.id)}
                        >
                          {d.file_name}
                        </a>
                      </td>
                      <td>{t(`doc.entity.${d.entity_type}` as Key)}</td>
                      <td>
                        <span
                          className={`ds-badge ds-badge--${
                            d.classification === 'sensitive_personal'
                              ? 'danger'
                              : d.classification === 'personal'
                                ? 'warning'
                                : 'neutral'
                          }`}
                        >
                          {t(`doc.class.${d.classification}` as Key)}
                        </span>
                      </td>
                      <td>
                        {d.expires_on ? (
                          <>
                            {shortDate(d.expires_on, locale)}
                            {d.days_to_expiry !== undefined &&
                              d.days_to_expiry < 0 && (
                                <span className="ds-badge ds-badge--danger">
                                  {t('doc.expired')}
                                </span>
                              )}
                          </>
                        ) : (
                          '—'
                        )}
                      </td>
                      <td>{shortDate(d.created_at, locale)}</td>
                      <td className="ds-table__actions">
                        {mayManage && (
                          <button
                            className="ds-btn ds-btn--quiet ds-btn--sm"
                            disabled={busy}
                            onClick={() => void drop(d.id)}
                          >
                            {t('action.remove')}
                          </button>
                        )}
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
  );
}
