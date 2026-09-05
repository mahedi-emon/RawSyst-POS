'use client';

// The regulatory registry: the legal values the product computes with.
//
// # What "verified" means here, and what it does not
//
// It means a person at the platform opened the official document and checked
// the figure against it, and recorded which document and when. It is an
// internal control. It is NOT approval by a tax authority, it does not make a
// rate lawful, and nothing on this screen says otherwise — a screen that
// implied ZATCA or the NBR had signed anything off would be inventing a
// regulatory fact, which is the one thing this registry exists to prevent.
//
// # A release blocker is the operational figure on this screen
//
// Most rules are ordinary. A release BLOCKER is one the product refuses to
// guess: an unverified blocker makes `requireMarketIsUsable` refuse to take on
// a new client in that market, with `unverified_regulatory_rule`. So the count
// of unverified blockers per market is not a statistic, it is the list of
// markets the platform currently cannot sell into, and it leads the screen.
//
// That gate is `registry.New(pool, cfg.Env.IsProduction())`, so in development
// it is off and the same registry reads as harmless. The summary says which
// markets WOULD be closed either way, because an operator wants to fix it
// before it becomes true rather than after.
//
// # A correction supersedes rather than overwrites
//
// Recording a value against a later effective date does not edit the old row.
// A document processed last March must still resolve to the figure that
// governed it in March, so the registry keeps both and resolves by date. That
// is why this screen has no edit button and no delete: the way to correct a
// rule is to record the corrected one.

import { ScrollText } from 'lucide-react';
import { Suspense, useMemo, useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

interface Rule {
  id: string;
  rule_key: string;
  country: string;
  payload: unknown;
  effective_from: string;
  source_authority?: string;
  source_document?: string;
  source_url?: string;
  verified_on?: string;
  verified: boolean;
  release_blocker: boolean;
  notes?: string;
}

const MARKETS = ['sa', 'bd', 'us'] as const;

function RulesScreen() {
  const t = useT();
  const [country, setCountry] = useUrlState('country');
  const { data, isLoading, error, refetch } = useApiList<Rule>('/platform/rules', {
    country,
  });

  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  const [form, setForm] = useState({
    rule_key: '',
    country: '',
    payload: '{\n  \n}',
    effective_from: '',
    source_authority: '',
    source_document: '',
    source_url: '',
    release_blocker: false,
    verified: false,
    notes: '',
  });

  const rows = data?.data ?? [];

  // Markets whose sale is blocked by a legal value nobody has checked. Computed
  // over the WHOLE list rather than the filtered view: filtering to Bangladesh
  // must not make Saudi Arabia's blockers disappear from a warning about which
  // markets are closed.
  const blockedMarkets = useMemo(() => {
    const byMarket = new Map<string, number>();
    for (const r of rows) {
      if (!r.release_blocker || r.verified) continue;
      byMarket.set(r.country, (byMarket.get(r.country) ?? 0) + 1);
    }
    return byMarket;
  }, [rows]);

  async function record() {
    setBusy(true);
    setSaveError(null);
    setFieldErrors(null);

    // Parsed here so a malformed payload is named as such, rather than arriving
    // as a decode failure about the whole request body.
    let payload: unknown;
    try {
      payload = JSON.parse(form.payload);
    } catch {
      setFieldErrors({ payload: t('nx.plat.ruPayloadInvalid') });
      setSaveError(t('nx.plat.ruNotSaved'));
      setBusy(false);
      return;
    }

    try {
      await api.post('/platform/rules', { ...form, payload });
      setOpen(false);
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setSaveError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Rule>[] = [
    {
      key: 'rule_key',
      header: t('nx.plat.ruKey'),
      primary: true,
      // An identifier the product resolves by, not prose: Latin, never mirrored.
      cell: (x) => <span className="num">{x.rule_key}</span>,
    },
    {
      key: 'country',
      header: t('nx.plat.ruCountry'),
      width: 'w-20',
      cell: (x) => <span className="num uppercase text-muted">{x.country}</span>,
    },
    {
      key: 'from',
      header: t('nx.plat.ruFrom'),
      width: 'w-28',
      cell: (x) => <time dateTime={x.effective_from}>{x.effective_from}</time>,
    },
    {
      key: 'source',
      header: t('nx.plat.ruSource'),
      cell: (x) => (
        <span className="text-muted">
          {x.source_document || t('nx.plat.ruNoDocument')}
          {x.source_authority ? (
            <span className="num uppercase"> · {x.source_authority}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'state',
      header: t('nx.plat.ruState'),
      width: 'w-52',
      cell: (x) => (
        <span className="flex flex-wrap items-center gap-1.5">
          {x.verified ? (
            <Badge tone="positive">{t('nx.plat.ruChecked')}</Badge>
          ) : (
            <Badge tone="caution">{t('nx.plat.ruUnchecked')}</Badge>
          )}
          {/* Only meaningful when it is also unverified: a verified blocker
              blocks nothing. */}
          {x.release_blocker && !x.verified ? (
            <Badge tone="critical">{t('nx.plat.ruBlocking')}</Badge>
          ) : null}
        </span>
      ),
    },
  ];

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader
        title={t('nx.plat.ruTitle')}
        description={t('nx.plat.ruSubtitle')}
        actions={
          !open ? (
            <Button onClick={() => setOpen(true)}>{t('nx.plat.ruRecord')}</Button>
          ) : null
        }
      />

      {blockedMarkets.size > 0 ? (
        <Panel title={t('nx.plat.ruBlockedTitle')} className="mb-4">
          <p className="text-body text-muted">{t('nx.plat.ruBlockedDesc')}</p>
          <ul className="mt-3 space-y-1.5">
            {[...blockedMarkets.entries()].map(([market, count]) => (
              <li key={market} className="flex items-center gap-2 text-body">
                <Badge tone="critical">{market.toUpperCase()}</Badge>
                <span className="text-fg">
                  {t('nx.plat.ruBlockedCount', { count: String(count) })}
                </span>
              </li>
            ))}
          </ul>
        </Panel>
      ) : null}

      {open ? (
        <Panel title={t('nx.plat.ruRecordTitle')} className="mb-4">
          <p className="mb-4 text-body text-muted">{t('nx.plat.ruSupersedes')}</p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void record();
            }}
          >
            <FormError message={saveError} fields={fieldErrors} className="mb-4" />
            <div className="grid gap-4 sm:grid-cols-2">
              <Field
                name="rule_key"
                label={t('nx.plat.ruKey')}
                hint={t('nx.plat.ruKeyHint')}
              >
                <Input
                  dir="ltr"
                  value={form.rule_key}
                  onChange={(e) => setForm({ ...form, rule_key: e.target.value })}
                  required
                />
              </Field>
              <Field name="country" label={t('nx.plat.ruCountry')}>
                <Select
                  value={form.country}
                  onChange={(e) => setForm({ ...form, country: e.target.value })}
                  required
                >
                  <option value="">{t('nx.plat.ruChooseCountry')}</option>
                  {MARKETS.map((m) => (
                    <option key={m} value={m}>
                      {m.toUpperCase()}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field
                name="effective_from"
                label={t('nx.plat.ruFrom')}
                hint={t('nx.plat.ruFromHint')}
              >
                <Input
                  type="date"
                  value={form.effective_from}
                  onChange={(e) => setForm({ ...form, effective_from: e.target.value })}
                  required
                />
              </Field>
              <Field
                name="source_authority"
                label={t('nx.plat.ruAuthority')}
                hint={t('nx.plat.ruAuthorityHint')}
              >
                <Input
                  dir="ltr"
                  value={form.source_authority}
                  onChange={(e) =>
                    setForm({ ...form, source_authority: e.target.value })
                  }
                  required
                />
              </Field>
              <Field name="source_document" label={t('nx.plat.ruDocument')}>
                <Input
                  value={form.source_document}
                  onChange={(e) => setForm({ ...form, source_document: e.target.value })}
                  required
                />
              </Field>
              <Field name="source_url" label={t('nx.plat.ruUrl')}>
                <Input
                  type="url"
                  dir="ltr"
                  value={form.source_url}
                  onChange={(e) => setForm({ ...form, source_url: e.target.value })}
                />
              </Field>
            </div>

            <div className="mt-4">
              <Field
                name="payload"
                label={t('nx.plat.ruPayload')}
                hint={t('nx.plat.ruPayloadHint')}
              >
                <Textarea
                  dir="ltr"
                  rows={5}
                  className="font-mono"
                  value={form.payload}
                  onChange={(e) => setForm({ ...form, payload: e.target.value })}
                />
              </Field>
            </div>

            <div className="mt-4">
              <Field name="notes" label={t('nx.plat.ruNotes')}>
                <Textarea
                  rows={2}
                  value={form.notes}
                  onChange={(e) => setForm({ ...form, notes: e.target.value })}
                />
              </Field>
            </div>

            <div className="mt-4 space-y-3">
              <Checkbox
                checked={form.release_blocker}
                onChange={(e) =>
                  setForm({ ...form, release_blocker: e.target.checked })
                }
                label={t('nx.plat.ruBlockerLabel')}
                hint={t('nx.plat.ruBlockerHint')}
              />
              <Checkbox
                checked={form.verified}
                onChange={(e) => setForm({ ...form, verified: e.target.checked })}
                label={t('nx.plat.ruVerifiedLabel')}
                hint={t('nx.plat.ruVerifiedHint')}
              />
            </div>

            <div className="mt-6 flex flex-wrap gap-2">
              <Button type="submit" busy={busy} busyLabel={t('nx.plat.ruSaving')}>
                {t('nx.plat.ruSave')}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
                {t('nx.plat.ruCancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Select
          aria-label={t('nx.plat.ruFilterCountry')}
          value={country}
          onChange={(e) => setCountry(e.target.value)}
        >
          <option value="">{t('nx.plat.ruAllCountries')}</option>
          {MARKETS.map((m) => (
            <option key={m} value={m}>
              {m.toUpperCase()}
            </option>
          ))}
        </Select>
      </div>

      {isLoading && !data ? <TableSkeleton columns={5} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={ScrollText}
          title={t('nx.plat.ruEmptyTitle')}
          description={t('nx.plat.ruEmptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<Rule>
          rows={rows}
          columns={columns}
          rowKey={(x) => x.id}
          caption={t('nx.plat.ruCaption')}
        />
      ) : null}
    </>
  );
}

export default function RulesPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <RulesScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
