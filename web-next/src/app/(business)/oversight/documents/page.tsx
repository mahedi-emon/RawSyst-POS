'use client';

// The document store.
//
// # What is about to run out leads
//
// A store of files is a filing cabinet, and a filing cabinet does not tell
// anybody what to do this morning. A commercial registration or an Iqama that
// expires stops a business trading, and the renewal takes weeks, so anything
// expiring comes before the search.
//
// # A document with no expiry is not a document expiring today
//
// `days_to_expiry` is omitted entirely for a permanent file. Reading the
// missing field as zero would file every one of them as due this morning, and
// the panel above would cry wolf until nobody read it.
//
// # The checksum is shown because it is the point
//
// A file store that cannot prove a document is the one that was filed is a
// folder. The checksum is what makes it a register, so it is on the row rather
// than behind a detail view.

import { FolderOpen } from 'lucide-react';
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
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { saveAs } from '@/lib/download';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  expiringSoon,
  expiryState,
  fileSize,
  type Document,
  type Expiry,
} from '@/lib/oversight/records';
import { useUrlState } from '@/lib/url-state';

const EXPIRY_TONE: Record<Expiry, Tone> = {
  expired: 'critical',
  expiring: 'caution',
  current: 'positive',
  none: 'neutral',
};

const EXPIRY_LABEL: Record<Expiry, Key> = {
  expired: 'nx.doc.expired',
  expiring: 'nx.doc.expiring',
  current: 'nx.doc.current',
  none: 'nx.doc.noExpiry',
};

/** Reads a picked file into the base64 the upload route takes. */
function readAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error('unreadable'));
    reader.onload = () => {
      const result = String(reader.result ?? '');
      // The route wants the raw base64 with no `data:...;base64,` prefix.
      resolve(result.slice(result.indexOf(',') + 1));
    };
    reader.readAsDataURL(file);
  });
}

function DocumentsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayManage = grants.can('document.manage');

  const [search, setSearch] = useUrlState('q', '');
  const [typed, setTyped] = useState(search);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Which filed document is being taken off the record. Asked twice, because
  // the file goes with the row.
  const [removing, setRemoving] = useState<Document | null>(null);
  const [filing, setFiling] = useState(false);
  const [entityType, setEntityType] = useState('company');
  // Empty by default, and that is the useful default: the server picks a
  // sensitivity from the record the file hangs off, which is a better answer
  // than most people filing a document would give.
  const [classification, setClassification] = useState('');
  const [expiresOn, setExpiresOn] = useState('');
  const picked = useRef<HTMLInputElement>(null);

  const documents = useApiList<Document>(
    scope ? '/documents' : null,
    scope ? { ...scope, ...(search ? { q: search } : {}), limit: 200 } : undefined,
  );
  const rows = documents.data?.data ?? [];
  const soon = expiringSoon(rows);

  async function file() {
    const chosen = picked.current?.files?.[0];
    if (!scope || !chosen) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(`/documents?company_id=${scope.company_id}`, {
        entity_type: entityType,
        entity_id: scope.company_id,
        file_name: chosen.name,
        data: await readAsBase64(chosen),
        // Sent only when overridden, so the server's own choice stands.
        ...(classification ? { classification } : {}),
        // Sent only when given: an empty string is not a date, and a document
        // that never expires must not be filed as expiring on the epoch.
        ...(expiresOn ? { expires_on: expiresOn } : {}),
      });
      if (picked.current) picked.current.value = '';
      setExpiresOn('');
      setFiling(false);
      await documents.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  /**
   * Taking a filed document off the record.
   *
   * `DELETE /documents/{id}` was live and reachable from nothing: a shop could
   * file a supplier contract and never remove one filed by mistake, or one
   * whose retention period has run out. Asked twice because the file goes with
   * the row, and the checksum beside it is the only proof the copy was ever
   * what it said it was.
   */
  async function remove(doc: Document) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      await api.delete(`/documents/${doc.id}?company_id=${scope.company_id}`);
      setRemoving(null);
      void documents.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function download(doc: Document) {
    if (!scope) return;
    setError(null);
    try {
      // Fetched with the token rather than linked to. The API reads the
      // bearer header and nothing else, so an <a href> to the same path
      // answers 401 and the person gets a broken download with no reason.
      const { blob, filename } = await api.download(
        `/documents/${doc.id}/file?company_id=${scope.company_id}`,
      );
      saveAs(blob, filename === 'download' ? doc.file_name : filename);
    } catch (e) {
      setError(messageFor(e, t));
    }
  }

  const columns: Column<Document>[] = [
    {
      key: 'file',
      header: t('nx.doc.colFile'),
      primary: true,
      cell: (d) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{d.file_name}</span>
          <span className="text-caption text-muted">
            {t(`nx.doc.class.${d.classification}` as Key)} · {fileSize(d.byte_size)}
          </span>
        </span>
      ),
    },
    {
      key: 'about',
      header: t('nx.doc.colAbout'),
      width: 'w-40',
      cell: (d) => <span className="text-muted">{d.entity_type}</span>,
    },
    {
      key: 'expiry',
      header: t('nx.doc.colExpiry'),
      width: 'w-48',
      cell: (d) => (
        <span className="flex flex-col gap-1">
          <Badge tone={EXPIRY_TONE[expiryState(d)]}>{t(EXPIRY_LABEL[expiryState(d)])}</Badge>
          {d.expires_on ? (
            <span className="num text-caption text-muted">{d.expires_on}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'checksum',
      header: t('nx.doc.colChecksum'),
      secondary: true,
      width: 'w-48',
      cell: (d) => (
        // Truncated to read, full in the title: it is proof, and proof that
        // cannot be compared is decoration.
        <span className="num text-caption text-muted" title={d.checksum}>
          {d.checksum.slice(0, 16)}…
        </span>
      ),
    },
    {
      key: 'get',
      header: t('nx.doc.colAction'),
      width: mayManage ? 'w-48' : 'w-28',
      cell: (d) => (
        <span className="flex gap-1">
          <Button variant="ghost" onClick={() => void download(d)}>
            {t('nx.doc.open')}
          </Button>
          {mayManage ? (
            <Button variant="ghost" onClick={() => setRemoving(d)}>
              {t('nx.doc.remove')}
            </Button>
          ) : null}
        </span>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.doc.title')}
        description={t('nx.doc.subtitle')}
        actions={
          mayManage ? (
            <Button variant="primary" onClick={() => setFiling((v) => !v)}>
              {t('nx.doc.file')}
            </Button>
          ) : null
        }
      />

      <FormError message={error} className="mb-4" />

      {removing ? (
        <Panel
          className="mb-6"
          title={t('nx.doc.removeTitle', { name: removing.file_name })}
          description={t('nx.doc.removeHint')}
        >
          <div className="flex flex-wrap gap-3">
            <Button variant="destructive" busy={busy} onClick={() => void remove(removing)}>
              {t('nx.doc.confirmRemove')}
            </Button>
            <Button variant="ghost" onClick={() => setRemoving(null)}>
              {t('nx.doc.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {soon.length > 0 ? (
        <Panel className="mb-6" title={t('nx.doc.renewTitle')}>
          <ul className="flex flex-col divide-y divide-line">
            {soon.map((d) => (
              <li key={d.id} className="flex flex-wrap items-center gap-3 py-2 first:pt-0">
                <Badge tone={EXPIRY_TONE[expiryState(d)]}>
                  {t(EXPIRY_LABEL[expiryState(d)])}
                </Badge>
                <span className="min-w-0 flex-1 truncate text-body">{d.file_name}</span>
                <span className="num text-caption text-muted">
                  {(d.days_to_expiry ?? 0) < 0
                    ? t('nx.doc.daysAgo', { n: String(Math.abs(d.days_to_expiry ?? 0)) })
                    : t('nx.doc.inDays', { n: String(d.days_to_expiry ?? 0) })}
                </span>
              </li>
            ))}
          </ul>
        </Panel>
      ) : null}

      {filing && mayManage ? (
        <Panel
          className="mb-6"
          title={t('nx.doc.fileTitle')}
          actions={
            <Button variant="ghost" onClick={() => setFiling(false)}>
              {t('nx.doc.cancel')}
            </Button>
          }
        >
          <div className="flex flex-wrap items-end gap-3">
            <Field name="file" label={t('nx.doc.chooseFile')}>
              <Input ref={picked} type="file" />
            </Field>
            <Field name="entity_type" label={t('nx.doc.colAbout')}>
              <Select value={entityType} onChange={(e) => setEntityType(e.target.value)}>
                <option value="company">{t('nx.doc.about.company')}</option>
                <option value="employee">{t('nx.doc.about.employee')}</option>
                <option value="supplier">{t('nx.doc.about.supplier')}</option>
                <option value="customer">{t('nx.doc.about.customer')}</option>
              </Select>
            </Field>
            <Field
              name="classification"
              label={t('nx.doc.colClassification')}
              hint={t('nx.doc.classificationHint')}
            >
              {/* How sensitive the CONTENT is, not what kind of document it
                  is. Retention and the erasure path read this, which is why
                  it is these four words and not a free-text label. */}
              <Select
                value={classification}
                onChange={(e) => setClassification(e.target.value)}
              >
                <option value="">{t('nx.doc.class.decide')}</option>
                <option value="public">{t('nx.doc.class.public')}</option>
                <option value="internal">{t('nx.doc.class.internal')}</option>
                <option value="personal">{t('nx.doc.class.personal')}</option>
                <option value="sensitive_personal">
                  {t('nx.doc.class.sensitive_personal')}
                </option>
              </Select>
            </Field>
            <Field
              name="expires_on"
              label={t('nx.doc.colExpiry')}
              hint={t('nx.doc.expiryHint')}
            >
              <Input
                type="date"
                value={expiresOn}
                onChange={(e) => setExpiresOn(e.target.value)}
              />
            </Field>
            <Button variant="primary" disabled={busy} onClick={() => void file()}>
              {t('nx.doc.fileIt')}
            </Button>
          </div>
        </Panel>
      ) : null}

      <div className="mb-5 flex flex-wrap items-end gap-3">
        <Field name="q" label={t('nx.doc.search')}>
          <Input
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') setSearch(typed.trim());
            }}
          />
        </Field>
        <Button onClick={() => setSearch(typed.trim())}>{t('nx.doc.searchGo')}</Button>
        {search ? (
          <Button
            variant="ghost"
            onClick={() => {
              setTyped('');
              setSearch('');
            }}
          >
            {t('nx.doc.clear')}
          </Button>
        ) : null}
      </div>

      {documents.error ? (
        <ErrorState error={documents.error} onRetry={() => void documents.refetch()} />
      ) : null}
      {documents.isLoading && !documents.data ? <TableSkeleton columns={5} /> : null}

      {!documents.isLoading && !documents.error && rows.length === 0 ? (
        <EmptyState
          icon={FolderOpen}
          title={t(search ? 'nx.doc.noMatchTitle' : 'nx.doc.emptyTitle')}
          description={t(search ? 'nx.doc.noMatchDesc' : 'nx.doc.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.doc.title')}
          columns={columns}
          rows={rows}
          rowKey={(d) => d.id}
        />
      ) : null}
    </>
  );
}

export default function DocumentsPage() {
  return (
    <RequirePermission anyOf={['document.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <DocumentsScreen />
      </Suspense>
    </RequirePermission>
  );
}
