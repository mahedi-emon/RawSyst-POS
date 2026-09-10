'use client';

// The document a legal value came out of.
//
// # What this screen is for, and how it differs from the one next door
//
// `/platform/rules` holds the figures this product computes with and the
// citation beside each one — an authority, a document name, a URL. A citation
// is what somebody typed about a publication. It is not the publication.
//
// This screen holds the publication: the bytes as retrieved, their SHA-256, the
// address they came from and the moment they arrived, and the reading taken out
// of them field by field with the sentence supporting each figure. It is the
// difference between "this came from the Labour Law" and "here is the Labour
// Law, here is the sentence, and here is what was read from it".
//
// # The workflow, and why the last step is a person's
//
//     fetch or upload -> read -> validate -> PREVIEW -> apply -> audit
//
// Everything up to the preview is automatic. Applying is not, and will not
// become so: a legal value in this registry carries the name of whoever put it
// there and the date they did, and neither a retrieval nor a pattern can supply
// either. What has changed is what that person is being asked to do. They are
// no longer transcribing figures out of a PDF into a form; they are checking a
// reading against sentences shown beside it.
//
// # Why upload is not the lesser half
//
// A ministry may block automated requests, publish behind a portal, or hand the
// document out at a counter. None of that is a software condition. The bytes
// arrive, and they are hashed, read, validated and applied by exactly the same
// code — so the only thing an upload cannot do is claim an address it did not
// come from, which is why the address on an upload is recorded as something the
// operator asserts rather than as somewhere this product went.

import { FileText, Upload } from 'lucide-react';
import { Suspense, useMemo, useRef, useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { TableSkeleton } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { saveAs } from '@/lib/download';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

/** One field, as read out of the document. */
interface Extracted {
  field: string;
  value: string;
  article: string;
  evidence: string;
  derived?: string;
}

/** A retrieved artefact and what was read out of it. */
interface SourceDocument {
  id: string;
  rule_key: string;
  country: string;
  source_authority: string;
  title: string;
  url?: string;
  origin: 'fetch' | 'upload' | 'refresh';
  media_type: string;
  byte_size: number;
  content_sha256: string;
  retrieved_on: string;
  retrieved_by?: string;
  extracted?: Extracted[];
  extraction_error?: string;
  validation_error?: string;
  valid: boolean;
  status: 'candidate' | 'applied' | 'superseded' | 'rejected';
  applied_rule_id?: string;
  applied_at?: string;
  rejected_reason?: string;
  notes?: string;
}

/** A rule, as the registry holds it. Shown beside the document so an operator
 *  can see what is in force against what a candidate proposes. */
interface Rule {
  id: string;
  rule_key: string;
  country: string;
  payload: unknown;
  effective_from: string;
  effective_to?: string;
  source_document?: string;
  source_url?: string;
  verified: boolean;
  verified_on?: string;
  blocks?: 'onboarding' | 'feature';
  release_blocker: boolean;
}

/** What the source pack says about a rule: which document, which articles. */
interface SourceRule {
  rule_key: string;
  country: string;
  title: string;
  authority: string;
  document: string;
  url: string;
  articles?: string[];
  reading: string;
}

function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

function statusTone(status: SourceDocument['status']) {
  switch (status) {
    case 'applied':
      return 'positive' as const;
    case 'candidate':
      return 'caution' as const;
    default:
      return 'neutral' as const;
  }
}

function RegulatorySourcesScreen() {
  const t = useT();
  const [ruleKey, setRuleKey] = useUrlState('rule_key');

  const documents = useApiList<SourceDocument>('/platform/regulatory-sources', {
    rule_key: ruleKey,
  });
  const rules = useApiList<Rule>('/platform/rules');
  const pack = useApi<{ version: string; rules: SourceRule[] }>(
    '/platform/rules/sources',
  );

  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string> | null>(null);
  const [open, setOpen] = useState<string | null>(null);

  const rows = documents.data?.data ?? [];
  const described = pack.data?.rules ?? [];

  // The rule in force for a document's key, so a candidate can be read against
  // what it would replace rather than in isolation.
  const inForce = useMemo(() => {
    const byKey = new Map<string, Rule>();
    for (const r of rules.data?.data ?? []) {
      if (r.effective_to) continue;
      byKey.set(`${r.rule_key}:${r.country}`, r);
    }
    return byKey;
  }, [rules.data]);

  const packFor = (key: string) => described.find((r) => r.rule_key === key);

  function refresh() {
    void documents.refetch();
    void rules.refetch();
  }

  async function act(id: string, run: () => Promise<unknown>) {
    setBusy(id);
    setError(null);
    setFields(null);
    try {
      await run();
      refresh();
      return true;
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setError(messageFor(e, t));
      return false;
    } finally {
      setBusy(null);
    }
  }

  if (documents.error) {
    return <ErrorState error={documents.error} onRetry={refresh} />;
  }

  return (
    <>
      <PageHeader
        title={t('nx.plat.rsTitle')}
        description={t('nx.plat.rsSubtitle')}
      />

      <FormError message={error} fields={fields} className="mb-4" />

      {/* What can be retrieved, and from where. Driven by the source pack, so a
          rule added to the pack appears here without a code change. */}
      <Panel title={t('nx.plat.rsRetrieveTitle')} className="mb-4">
        <p className="max-w-prose text-body text-muted">
          {t('nx.plat.rsRetrieveDesc')}
        </p>
        <div className="mt-4 flex flex-col gap-4">
          {described.map((src) => (
            <RetrievalRow
              key={src.rule_key}
              source={src}
              busy={busy === src.rule_key}
              onFetch={() =>
                act(src.rule_key, () =>
                  api.post('/platform/regulatory-sources/fetch', {
                    rule_key: src.rule_key,
                  }),
                )
              }
              onUpload={(form) =>
                act(src.rule_key, () =>
                  api.upload('/platform/regulatory-sources/upload', form),
                )
              }
            />
          ))}
        </div>
      </Panel>

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Input
          aria-label={t('nx.plat.rsFilterRule')}
          dir="ltr"
          placeholder={t('nx.plat.rsFilterRule')}
          value={ruleKey}
          onChange={(e) => setRuleKey(e.target.value.toUpperCase())}
        />
      </div>

      {documents.isLoading && !documents.data ? <TableSkeleton columns={4} /> : null}

      {!documents.isLoading && rows.length === 0 ? (
        <EmptyState
          icon={FileText}
          title={t('nx.plat.rsEmptyTitle')}
          description={t('nx.plat.rsEmptyDesc')}
        />
      ) : null}

      <div className="flex flex-col gap-4">
        {rows.map((doc) => (
          <DocumentCard
            key={doc.id}
            doc={doc}
            pack={packFor(doc.rule_key)}
            inForce={inForce.get(`${doc.rule_key}:${doc.country}`)}
            expanded={open === doc.id}
            onToggle={() => setOpen(open === doc.id ? null : doc.id)}
            busy={busy === doc.id}
            onApply={(from, verified) =>
              act(doc.id, () =>
                api.post(`/platform/regulatory-sources/${doc.id}/apply`, {
                  effective_from: from,
                  verified,
                }),
              )
            }
            onReject={(reason) =>
              act(doc.id, () =>
                api.post(`/platform/regulatory-sources/${doc.id}/reject`, {
                  reason,
                }),
              )
            }
          />
        ))}
      </div>
    </>
  );
}

/** Fetch from the authority, or supply the document by hand. */
function RetrievalRow({
  source,
  busy,
  onFetch,
  onUpload,
}: {
  source: SourceRule;
  busy: boolean;
  onFetch: () => void;
  onUpload: (form: FormData) => void;
}) {
  const t = useT();
  const input = useRef<HTMLInputElement>(null);

  return (
    <div className="flex flex-col gap-2 border-t border-line pt-4 first:border-0 first:pt-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="num font-medium text-fg">{source.rule_key}</span>
        <Badge tone="neutral">{source.country.toUpperCase()}</Badge>
        <span className="num text-caption uppercase text-muted">
          {source.authority}
        </span>
      </div>
      <p className="text-body text-fg">{source.document}</p>
      {source.articles?.length ? (
        <p className="text-caption text-muted">
          {t('nx.plat.rsArticles', { articles: source.articles.join(', ') })}
        </p>
      ) : null}
      <p className="max-w-prose text-caption text-muted">{source.reading}</p>
      {source.url ? (
        <a
          href={source.url}
          target="_blank"
          rel="noreferrer"
          dir="ltr"
          className="num max-w-full truncate text-caption text-primary underline"
        >
          {source.url}
        </a>
      ) : null}

      <div className="mt-1 flex flex-wrap items-center gap-2">
        <Button size="sm" busy={busy} onClick={onFetch}>
          {t('nx.plat.rsFetch')}
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => input.current?.click()}
        >
          <Upload aria-hidden className="size-4" />
          {t('nx.plat.rsUpload')}
        </Button>
        <input
          ref={input}
          type="file"
          hidden
          accept=".pdf,.html,.htm,.txt,application/pdf,text/html,text/plain"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            const form = new FormData();
            form.set('file', file);
            form.set('rule_key', source.rule_key);
            form.set('country', source.country);
            form.set('title', source.document);
            form.set('url', source.url);
            onUpload(form);
            e.target.value = '';
          }}
        />
      </div>
      {/* Said before it is needed rather than after a fetch fails. A ministry
          refusing automated requests is an ordinary condition, not an error. */}
      <p className="max-w-prose text-caption text-muted">
        {t('nx.plat.rsFetchOrUpload')}
      </p>
    </div>
  );
}

/** One retrieved document: its provenance, its reading, and what to do next. */
function DocumentCard({
  doc,
  pack,
  inForce,
  expanded,
  onToggle,
  busy,
  onApply,
  onReject,
}: {
  doc: SourceDocument;
  pack?: SourceRule;
  inForce?: Rule;
  expanded: boolean;
  onToggle: () => void;
  busy: boolean;
  onApply: (from: string, verified: boolean) => void;
  onReject: (reason: string) => void;
}) {
  const t = useT();
  const [from, setFrom] = useState('');
  const [verified, setVerified] = useState(false);
  const [reason, setReason] = useState('');

  const canApply = doc.status === 'candidate' && doc.valid;

  // The artefact itself.
  //
  // Storing the bytes is only worth anything if somebody can open them: the
  // whole claim of this screen is that a reader need not take the product's
  // word for what the document said. Fetched through the client rather than
  // linked, because the API reads a bearer token and a plain link carries none.
  async function openFile() {
    try {
      const { blob, filename } = await api.download(
        `/platform/regulatory-sources/${doc.id}/content`,
      );
      saveAs(blob, filename);
    } catch {
      // Reported by the surrounding screen's error line on the next action;
      // a failed download must not blank the provenance already on screen.
    }
  }

  return (
    <Panel
      title={doc.rule_key}
      className="mb-0"
      actions={
        <span className="flex flex-wrap items-center gap-1.5">
          <Badge tone="neutral">{doc.country.toUpperCase()}</Badge>
          <Badge tone={statusTone(doc.status)}>
            {t(`nx.plat.rsStatus.${doc.status}`)}
          </Badge>
          {doc.valid ? (
            <Badge tone="positive">{t('nx.plat.rsValid')}</Badge>
          ) : (
            <Badge tone="critical">{t('nx.plat.rsInvalid')}</Badge>
          )}
        </span>
      }
    >
      {/* Provenance: everything needed to find this document again and prove it
          has not changed since. */}
      <dl className="grid gap-x-6 gap-y-2 sm:grid-cols-2">
        <Detail label={t('nx.plat.rsDocument')} value={doc.title} />
        <Detail
          label={t('nx.plat.rsAuthority')}
          value={doc.source_authority.toUpperCase()}
          mono
        />
        <Detail
          label={t('nx.plat.rsRetrieved')}
          value={`${doc.retrieved_on}${doc.retrieved_by ? ` · ${doc.retrieved_by}` : ''}`}
        />
        <Detail
          label={t('nx.plat.rsOrigin')}
          value={t(`nx.plat.rsOrigin.${doc.origin}`)}
        />
        <Detail
          label={t('nx.plat.rsFile')}
          value={`${doc.media_type} · ${bytes(doc.byte_size)}`}
          mono
        />
        <Detail label={t('nx.plat.rsHash')} value={doc.content_sha256} mono />
        <div className="min-w-0">
          <dt className="text-caption text-muted">{t('nx.plat.rsOpenFile')}</dt>
          <dd>
            <Button size="sm" variant="ghost" onClick={() => void openFile()}>
              {t('nx.plat.rsOpenFileAction')}
            </Button>
          </dd>
        </div>
        {doc.url ? (
          <div className="sm:col-span-2">
            <dt className="text-caption text-muted">{t('nx.plat.rsUrl')}</dt>
            <dd className="min-w-0">
              <a
                href={doc.url}
                target="_blank"
                rel="noreferrer"
                dir="ltr"
                className="num block truncate text-body text-primary underline"
              >
                {doc.url}
              </a>
            </dd>
          </div>
        ) : null}
        {pack?.articles?.length ? (
          <Detail
            label={t('nx.plat.rsArticlesRead')}
            value={pack.articles.join(', ')}
            mono
          />
        ) : null}
        {inForce ? (
          <Detail
            label={t('nx.plat.rsInForce')}
            value={
              inForce.verified
                ? t('nx.plat.rsInForceVerified', {
                    from: inForce.effective_from,
                    on: inForce.verified_on ?? '',
                  })
                : t('nx.plat.rsInForceUnverified', {
                    from: inForce.effective_from,
                  })
            }
          />
        ) : null}
        {doc.applied_at ? (
          <Detail label={t('nx.plat.rsAppliedAt')} value={doc.applied_at} />
        ) : null}
      </dl>

      {doc.notes ? (
        <p className="mt-3 max-w-prose text-caption text-muted">{doc.notes}</p>
      ) : null}

      {/* What was read, and what it was read out of. */}
      {doc.extraction_error ? (
        <div className="mt-4 border-t border-line pt-4">
          <p className="text-body text-fg">{t('nx.plat.rsNotRead')}</p>
          <p className="mt-1 max-w-prose text-caption text-muted">
            {doc.extraction_error}
          </p>
          <p className="mt-2 max-w-prose text-caption text-muted">
            {t('nx.plat.rsNotReadWhatNow')}
          </p>
        </div>
      ) : null}

      {doc.extracted?.length ? (
        <div className="mt-4 border-t border-line pt-4">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-body font-medium text-fg">
              {t('nx.plat.rsReading', {
                count: String(doc.extracted.length),
              })}
            </p>
            <Button size="sm" variant="ghost" onClick={onToggle}>
              {expanded ? t('nx.plat.rsHideEvidence') : t('nx.plat.rsShowEvidence')}
            </Button>
          </div>

          <ul className="mt-3 flex flex-col gap-3">
            {doc.extracted.map((f) => (
              <li key={f.field} className="flex flex-col gap-1">
                <div className="flex flex-wrap items-baseline gap-2">
                  <span className="num text-body text-muted">{f.field}</span>
                  <span className="num text-body font-medium text-fg">
                    {f.value}
                  </span>
                  <Badge tone="neutral">
                    {t('nx.plat.rsArticle', { article: f.article })}
                  </Badge>
                </div>
                {/* The sentence, not a paraphrase. This is what an operator
                    checks the reading against, and hiding it behind a link
                    would make confirming a figure an act of faith. */}
                {expanded ? (
                  <>
                    <blockquote className="max-w-prose border-s-2 border-line ps-3 text-caption text-muted">
                      {f.evidence}
                    </blockquote>
                    {f.derived ? (
                      <p className="max-w-prose text-caption text-muted">
                        {t('nx.plat.rsDerived')}: {f.derived}
                      </p>
                    ) : null}
                  </>
                ) : null}
              </li>
            ))}
          </ul>

          {!doc.valid && doc.validation_error ? (
            <p className="mt-3 max-w-prose text-caption text-critical">
              {doc.validation_error}
            </p>
          ) : null}
        </div>
      ) : null}

      {/* Applying: a person's act, and the only one on this screen. */}
      {canApply ? (
        <form
          className="mt-4 border-t border-line pt-4"
          onSubmit={(e) => {
            e.preventDefault();
            onApply(from, verified);
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="effective_from"
              label={t('nx.plat.rsFrom')}
              hint={t('nx.plat.rsFromHint')}
            >
              <Input
                type="date"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
                required
              />
            </Field>
          </div>
          <div className="mt-4">
            <Checkbox
              checked={verified}
              onChange={(e) => setVerified(e.target.checked)}
              label={t('nx.plat.rsVerifiedLabel')}
              hint={t('nx.plat.rsVerifiedHint')}
            />
          </div>
          <div className="mt-4 flex flex-wrap items-end gap-2">
            <Button type="submit" busy={busy} busyLabel={t('nx.plat.rsApplying')}>
              {t('nx.plat.rsApply')}
            </Button>
          </div>
          <p className="mt-3 max-w-prose text-caption text-muted">
            {t('nx.plat.rsApplyNote')}
          </p>
        </form>
      ) : null}

      {doc.status === 'candidate' ? (
        <form
          className="mt-4 flex flex-wrap items-end gap-2 border-t border-line pt-4"
          onSubmit={(e) => {
            e.preventDefault();
            onReject(reason);
          }}
        >
          <div className="min-w-56 flex-1">
            <Field
              name="reason"
              label={t('nx.plat.rsRejectLabel')}
              hint={t('nx.plat.rsRejectHint')}
            >
              <Input
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                required
              />
            </Field>
          </div>
          <Button type="submit" variant="ghost" busy={busy}>
            {t('nx.plat.rsReject')}
          </Button>
        </form>
      ) : null}

      {doc.rejected_reason ? (
        <p className="mt-3 max-w-prose text-caption text-muted">
          {t('nx.plat.rsRejectedFor', { reason: doc.rejected_reason })}
        </p>
      ) : null}
    </Panel>
  );
}

function Detail({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-caption text-muted">{label}</dt>
      <dd
        className={`break-words text-body text-fg${mono ? ' num' : ''}`}
        dir={mono ? 'ltr' : undefined}
      >
        {value}
      </dd>
    </div>
  );
}

export default function RegulatorySourcesPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <RegulatorySourcesScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
