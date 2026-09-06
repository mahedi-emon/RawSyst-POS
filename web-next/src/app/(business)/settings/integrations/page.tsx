'use client';

// H6: the keys and the callbacks that let something else act on this business.
//
// # A key is readable once, and that is the design rather than a gap
//
// `POST /api-keys` is the only place the secret is ever legible; what the
// database keeps is a hash. The router says so outright — "a product that can
// show a key a second time is a product where the key is recoverable from the
// database." So the handover replaces the form, says plainly that it will not
// be shown again, and the list afterwards shows a prefix and what the key may
// do. There is no reveal button because there is nothing to reveal.
//
// # A key can never be an escalation
//
// The service intersects the permissions asked for with the ones the caller
// actually holds, taken from the token rather than from a request field. So the
// picker offers only what this person already has: offering more would collect
// a choice the server silently narrows, and somebody would walk away believing
// they had granted access they had not.
//
// # There is no un-revoke
//
// A key is revoked because somebody believes it leaked, and undoing that would
// undo the only response available. The confirmation says that rather than
// asking whether somebody is sure.
//
// # Off, not deleted
//
// An endpoint is switched off so the delivery history that explains a missing
// week survives. That is also why the deliveries view carries more weight here
// than the endpoint list does: "it stopped working on Tuesday" is answered by
// attempts and responses, not by a row saying active.

import { Check, Copy, Webhook } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { Tabs } from '@/components/ui/tabs';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

interface Endpoint {
  id: string;
  name: string;
  url: string;
  is_active: boolean;
  events: string[];
  created_at: string;
  created_by?: string;
  queued: number;
  failed: number;
  last_delivered_at?: string;
  last_error?: string;
}

interface WebhookDelivery {
  id: string;
  event: string;
  status: string;
  attempts: number;
  response_status?: number;
  last_error?: string;
  next_attempt_at?: string;
  delivered_at?: string;
  created_at: string;
}

interface ApiKey {
  id: string;
  name: string;
  key_prefix: string;
  permissions: string[];
  last_used_at?: string;
  expires_at?: string;
  revoked_at?: string;
  created_at: string;
  created_by?: string;
}

interface Minted extends ApiKey {
  secret: string;
}

type TabId = 'keys' | 'webhooks';

// The four the column allows. `failed` is still being retried; `abandoned` has
// stopped, and is the one that needs somebody — the same distinction the
// platform's job queue draws between failed and dead.
const DELIVERY_TONE: Record<string, 'positive' | 'caution' | 'critical' | 'neutral'> = {
  queued: 'neutral',
  delivered: 'positive',
  failed: 'caution',
  abandoned: 'critical',
};

/** The secret, at the one moment it can be read. */
function KeyHandover({ minted, onDone }: { minted: Minted; onDone: () => void }) {
  const t = useT();
  const [copied, setCopied] = useState(false);

  return (
    <Panel title={t('nx.itg.mintedTitle')} className="mb-4">
      <p className="text-body text-fg">{t('nx.itg.mintedOnce')}</p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        {/* A credential is a sequence of characters, not prose: LTR and
            monospaced in every language. */}
        <code
          dir="ltr"
          className="break-all rounded border border-line bg-surface-sunken px-2 py-1 font-mono text-body text-fg"
        >
          {minted.secret}
        </code>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            navigator.clipboard
              .writeText(minted.secret)
              .then(() => {
                setCopied(true);
                setTimeout(() => setCopied(false), 2000);
              })
              // A denied clipboard is not worth an error panel: the value is on
              // screen and can be selected.
              .catch(() => setCopied(false));
          }}
        >
          {copied ? <Check className="size-4" aria-hidden /> : <Copy className="size-4" aria-hidden />}
          {copied ? t('nx.itg.copied') : t('nx.itg.copy')}
        </Button>
      </div>
      <p className="mt-3 text-body text-muted">{t('nx.itg.mintedNotAgain')}</p>
      <div className="mt-4">
        <Button variant="secondary" onClick={onDone}>
          {t('nx.itg.savedIt')}
        </Button>
      </div>
    </Panel>
  );
}

function IntegrationsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayManage = grants.can('integration.manage');

  const [tab, setTab] = useUrlState('tab');
  const active: TabId = tab === 'webhooks' ? 'webhooks' : 'keys';

  const keys = useApiList<ApiKey>(scope && active === 'keys' ? '/api-keys' : null, {
    company_id: scope?.company_id,
  });
  // The event vocabulary travels with the list, so the form cannot offer an
  // event the server would refuse.
  const hooks = useApi<{ data: Endpoint[]; events: string[] }>(
    scope && active === 'webhooks' ? '/webhooks' : null,
    { company_id: scope?.company_id },
  );

  const [openHook, setOpenHook] = useState<string | null>(null);
  const deliveries = useApiList<WebhookDelivery>(
    openHook && scope ? `/webhooks/${openHook}/deliveries` : null,
    { company_id: scope?.company_id },
  );

  const [minted, setMinted] = useState<Minted | null>(null);
  const [mintOpen, setMintOpen] = useState(false);
  const [keyName, setKeyName] = useState('');
  const [keyPerms, setKeyPerms] = useState<string[]>([]);
  const [keyExpires, setKeyExpires] = useState('');
  const [revoking, setRevoking] = useState<ApiKey | null>(null);

  const [hookOpen, setHookOpen] = useState(false);
  const [hookName, setHookName] = useState('');
  const [hookUrl, setHookUrl] = useState('');
  const [hookEvents, setHookEvents] = useState<string[]>([]);

  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  // Only what this person already holds, and `list()` is already sorted. The
  // service intersects anyway; the picker matching it is what stops somebody
  // believing they granted more than they did.
  const grantable = grants.list();

  function fail(e: unknown) {
    if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
    setActionError(messageFor(e, t));
  }

  async function mint() {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    try {
      const out = await api.post<Minted>(`/api-keys?company_id=${scope.company_id}`, {
        name: keyName,
        permissions: keyPerms,
        expires_on: keyExpires,
      });
      setMinted(out);
      setMintOpen(false);
      setKeyName('');
      setKeyPerms([]);
      setKeyExpires('');
      void keys.refetch();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function revoke() {
    if (!scope || !revoking) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.delete(`/api-keys/${revoking.id}?company_id=${scope.company_id}`);
      setRevoking(null);
      void keys.refetch();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function saveHook() {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    try {
      await api.post(`/webhooks?company_id=${scope.company_id}`, {
        name: hookName,
        url: hookUrl,
        events: hookEvents,
      });
      setHookOpen(false);
      setHookName('');
      setHookUrl('');
      setHookEvents([]);
      void hooks.refetch();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function toggleHook(endpoint: Endpoint) {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(`/webhooks/${endpoint.id}/active?company_id=${scope.company_id}`, {
        is_active: !endpoint.is_active,
      });
      void hooks.refetch();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  const keyColumns: Column<ApiKey>[] = [
    {
      key: 'name',
      header: t('nx.itg.keyName'),
      primary: true,
      cell: (k) => (
        <span className="flex flex-col">
          <span className="flex items-center gap-2">
            {k.name}
            {k.revoked_at ? <Badge tone="critical">{t('nx.itg.revoked')}</Badge> : null}
          </span>
          {/* A prefix, never the key. There is nothing else to show. */}
          <span className="num text-caption text-muted" dir="ltr">
            {k.key_prefix}…
          </span>
        </span>
      ),
    },
    {
      key: 'permissions',
      header: t('nx.itg.mayDo'),
      cell: (k) => (
        <span className="text-caption text-muted">
          {k.permissions.length === 0
            ? t('nx.itg.noPermissions')
            : t('nx.itg.permissionCount', { count: String(k.permissions.length) })}
        </span>
      ),
    },
    {
      key: 'used',
      header: t('nx.itg.lastUsed'),
      width: 'w-32',
      // Never used is a fact about the integration, not a blank: it usually
      // means the key never reached whoever was meant to use it.
      cell: (k) =>
        k.last_used_at ? (
          <time dateTime={k.last_used_at}>{k.last_used_at.slice(0, 10)}</time>
        ) : (
          <span className="text-muted">{t('nx.itg.neverUsed')}</span>
        ),
    },
    {
      key: 'expires',
      header: t('nx.itg.expires'),
      width: 'w-32',
      cell: (k) =>
        k.expires_at ? (
          <time dateTime={k.expires_at}>{k.expires_at.slice(0, 10)}</time>
        ) : (
          <span className="text-subtle">{t('nx.itg.noExpiry')}</span>
        ),
    },
  ];

  if (mayManage) {
    keyColumns.push({
      key: 'revoke',
      header: t('nx.itg.revokeHeader'),
      width: 'w-28',
      cell: (k) =>
        k.revoked_at ? (
          <span className="text-muted">—</span>
        ) : (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => setRevoking(k)}>
            {t('nx.itg.revoke')}
          </Button>
        ),
    });
  }

  const hookColumns: Column<Endpoint>[] = [
    {
      key: 'name',
      header: t('nx.itg.endpoint'),
      primary: true,
      cell: (h) => (
        <span className="flex flex-col">
          <span className="flex items-center gap-2">
            {h.name}
            {!h.is_active ? <Badge tone="neutral">{t('nx.itg.off')}</Badge> : null}
          </span>
          <span className="break-all text-caption text-muted" dir="ltr">
            {h.url}
          </span>
        </span>
      ),
    },
    {
      key: 'events',
      header: t('nx.itg.events'),
      secondary: true,
      cell: (h) => (
        <span className="text-caption text-muted">
          {t('nx.itg.eventCount', { count: String(h.events.length) })}
        </span>
      ),
    },
    {
      key: 'health',
      header: t('nx.itg.health'),
      width: 'w-44',
      // Queued and failed together. A queue that is draining and a queue that
      // is stuck look identical if only one of them is shown.
      cell: (h) => (
        <span className="flex flex-col gap-1">
          <span className="num text-caption text-muted">
            {t('nx.itg.queuedFailed', {
              queued: String(h.queued),
              failed: String(h.failed),
            })}
          </span>
          {h.last_error ? (
            <span className="truncate text-caption text-critical-fg">{h.last_error}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'open',
      header: t('nx.itg.deliveriesHeader'),
      width: 'w-28',
      cell: (h) => (
        <Button size="sm" variant="ghost" onClick={() => setOpenHook(h.id)}>
          {t('nx.itg.openDeliveries')}
        </Button>
      ),
    },
  ];

  if (mayManage) {
    hookColumns.push({
      key: 'toggle',
      header: t('nx.itg.stateHeader'),
      width: 'w-28',
      cell: (h) => (
        <Button size="sm" variant="ghost" disabled={busy} onClick={() => void toggleHook(h)}>
          {h.is_active ? t('nx.itg.switchOff') : t('nx.itg.switchOn')}
        </Button>
      ),
    });
  }

  const deliveryColumns: Column<WebhookDelivery>[] = [
    {
      key: 'event',
      header: t('nx.itg.event'),
      primary: true,
      cell: (d) => <span className="num">{d.event}</span>,
    },
    {
      key: 'status',
      header: t('nx.itg.deliveryState'),
      width: 'w-32',
      cell: (d) => (
        <Badge tone={DELIVERY_TONE[d.status] ?? 'neutral'}>
          {t(`nx.itg.dstate.${d.status}` as 'nx.itg.dstate.queued')}
        </Badge>
      ),
    },
    {
      key: 'attempts',
      header: t('nx.itg.attempts'),
      numeric: true,
      width: 'w-24',
      cell: (d) => d.attempts,
    },
    {
      key: 'response',
      header: t('nx.itg.response'),
      width: 'w-40',
      // The code the endpoint answered, and what went wrong if nothing did.
      // This is the column that answers "it stopped working on Tuesday".
      cell: (d) => (
        <span className="flex flex-col">
          {d.response_status ? (
            <span className="num">{d.response_status}</span>
          ) : (
            <span className="text-subtle">—</span>
          )}
          {d.last_error ? (
            <span className="truncate text-caption text-critical-fg">{d.last_error}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'when',
      header: t('nx.itg.when'),
      width: 'w-32',
      cell: (d) => (
        <time dateTime={d.delivered_at || d.created_at}>
          {(d.delivered_at || d.created_at).slice(0, 10)}
        </time>
      ),
    },
  ];

  const listError = active === 'keys' ? keys.error : hooks.error;
  if (listError) {
    return (
      <ErrorState
        error={listError}
        onRetry={() => void (active === 'keys' ? keys.refetch() : hooks.refetch())}
      />
    );
  }

  const keyRows = keys.data?.data ?? [];
  const hookRows = hooks.data?.data ?? [];
  const events = hooks.data?.events ?? [];

  return (
    <>
      <PageHeader
        title={t('nx.itg.title')}
        description={t('nx.itg.subtitle')}
        actions={
          mayManage && !mintOpen && !hookOpen ? (
            <Button
              onClick={() => (active === 'keys' ? setMintOpen(true) : setHookOpen(true))}
            >
              {active === 'keys' ? t('nx.itg.newKey') : t('nx.itg.newEndpoint')}
            </Button>
          ) : null
        }
      />

      <Tabs<TabId>
        label={t('nx.itg.tabsLabel')}
        items={[
          { id: 'keys', label: t('nx.itg.tabKeys') },
          { id: 'webhooks', label: t('nx.itg.tabWebhooks') },
        ]}
        value={active}
        onChange={(id) => {
          setTab(id);
          setOpenHook(null);
          setMintOpen(false);
          setHookOpen(false);
        }}
      />

      <FormError message={actionError} fields={fieldErrors} className="my-4" />

      {minted ? <KeyHandover minted={minted} onDone={() => setMinted(null)} /> : null}

      {revoking ? (
        <Panel title={t('nx.itg.revokeTitle', { name: revoking.name })} className="mb-4">
          {/* Not "are you sure". The reason it cannot be undone is the thing
              somebody needs to read before deciding. */}
          <p className="text-body text-fg">{t('nx.itg.revokeExplain')}</p>
          <div className="mt-4 flex flex-wrap gap-2">
            <Button variant="destructive" busy={busy} onClick={() => void revoke()}>
              {t('nx.itg.confirmRevoke')}
            </Button>
            <Button variant="ghost" onClick={() => setRevoking(null)}>
              {t('nx.itg.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {mintOpen ? (
        <Panel title={t('nx.itg.newKeyTitle')} className="mb-4">
          <p className="mb-4 text-body text-muted">{t('nx.itg.intersectHint')}</p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void mint();
            }}
          >
            <div className="grid gap-4 sm:grid-cols-2">
              <Field name="name" label={t('nx.itg.keyName')} hint={t('nx.itg.keyNameHint')}>
                <Input value={keyName} onChange={(e) => setKeyName(e.target.value)} required />
              </Field>
              <Field
                name="expires_on"
                label={t('nx.itg.expires')}
                hint={t('nx.itg.expiresHint')}
              >
                <Input
                  type="date"
                  value={keyExpires}
                  onChange={(e) => setKeyExpires(e.target.value)}
                />
              </Field>
            </div>

            <fieldset className="mt-4">
              <legend className="text-label font-medium text-fg">
                {t('nx.itg.mayDo')}
              </legend>
              <p className="mt-1 mb-3 text-caption text-muted">
                {t('nx.itg.permissionsHint')}
              </p>
              <div className="grid max-h-64 gap-2 overflow-y-auto sm:grid-cols-2">
                {grantable.map((p) => (
                  <Checkbox
                    key={p}
                    checked={keyPerms.includes(p)}
                    onChange={(e) =>
                      setKeyPerms(
                        e.target.checked
                          ? [...keyPerms, p]
                          : keyPerms.filter((x) => x !== p),
                      )
                    }
                    label={<span className="num">{p}</span>}
                  />
                ))}
              </div>
            </fieldset>

            <div className="mt-6 flex flex-wrap gap-2">
              <Button
                type="submit"
                busy={busy}
                busyLabel={t('nx.itg.creating')}
                disabled={keyPerms.length === 0}
              >
                {t('nx.itg.createKey')}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setMintOpen(false)}>
                {t('nx.itg.cancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}

      {hookOpen ? (
        <Panel title={t('nx.itg.newEndpointTitle')} className="mb-4">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void saveHook();
            }}
          >
            <div className="grid gap-4 sm:grid-cols-2">
              <Field name="name" label={t('nx.itg.endpointName')}>
                <Input value={hookName} onChange={(e) => setHookName(e.target.value)} required />
              </Field>
              <Field name="url" label={t('nx.itg.url')} hint={t('nx.itg.urlHint')}>
                <Input
                  type="url"
                  dir="ltr"
                  value={hookUrl}
                  onChange={(e) => setHookUrl(e.target.value)}
                  placeholder="https://"
                  required
                />
              </Field>
            </div>

            <fieldset className="mt-4">
              <legend className="text-label font-medium text-fg">
                {t('nx.itg.events')}
              </legend>
              <p className="mt-1 mb-3 text-caption text-muted">{t('nx.itg.eventsHint')}</p>
              <div className="grid gap-2 sm:grid-cols-2">
                {events.map((ev) => (
                  <Checkbox
                    key={ev}
                    checked={hookEvents.includes(ev)}
                    onChange={(e) =>
                      setHookEvents(
                        e.target.checked
                          ? [...hookEvents, ev]
                          : hookEvents.filter((x) => x !== ev),
                      )
                    }
                    label={<span className="num">{ev}</span>}
                  />
                ))}
              </div>
            </fieldset>

            <div className="mt-6 flex flex-wrap gap-2">
              <Button
                type="submit"
                busy={busy}
                busyLabel={t('nx.itg.saving')}
                disabled={hookEvents.length === 0}
              >
                {t('nx.itg.createEndpoint')}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setHookOpen(false)}>
                {t('nx.itg.cancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}

      {openHook ? (
        <Panel
          title={t('nx.itg.deliveriesTitle')}
          description={t('nx.itg.deliveriesHint')}
          className="mb-4"
          flush
          actions={
            <Button variant="ghost" onClick={() => setOpenHook(null)}>
              {t('nx.itg.closePanel')}
            </Button>
          }
        >
          {deliveries.isLoading && !deliveries.data ? (
            <TableSkeleton columns={5} />
          ) : (deliveries.data?.data ?? []).length === 0 ? (
            <div className="p-6">
              <EmptyState
                icon={Webhook}
                title={t('nx.itg.noDeliveriesTitle')}
                description={t('nx.itg.noDeliveriesDesc')}
              />
            </div>
          ) : (
            <DataTable<WebhookDelivery>
              rows={deliveries.data?.data ?? []}
              columns={deliveryColumns}
              rowKey={(d) => d.id}
              caption={t('nx.itg.deliveriesCaption')}
            />
          )}
        </Panel>
      ) : null}

      {active === 'keys' ? (
        <>
          {keys.isLoading && !keys.data ? <TableSkeleton columns={5} /> : null}
          {!keys.isLoading && keyRows.length === 0 ? (
            <EmptyState
              icon={Webhook}
              title={t('nx.itg.noKeysTitle')}
              description={t('nx.itg.noKeysDesc')}
            />
          ) : null}
          {keyRows.length > 0 ? (
            <DataTable<ApiKey>
              rows={keyRows}
              columns={keyColumns}
              rowKey={(k) => k.id}
              caption={t('nx.itg.keysCaption')}
            />
          ) : null}
        </>
      ) : (
        <>
          {hooks.isLoading && !hooks.data ? <TableSkeleton columns={5} /> : null}
          {!hooks.isLoading && hookRows.length === 0 ? (
            <EmptyState
              icon={Webhook}
              title={t('nx.itg.noHooksTitle')}
              description={t('nx.itg.noHooksDesc')}
            />
          ) : null}
          {hookRows.length > 0 ? (
            <DataTable<Endpoint>
              rows={hookRows}
              columns={hookColumns}
              rowKey={(h) => h.id}
              caption={t('nx.itg.hooksCaption')}
            />
          ) : null}
        </>
      )}
    </>
  );
}

export default function IntegrationsPage() {
  return (
    <RequirePermission anyOf={['integration.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <IntegrationsScreen />
      </Suspense>
    </RequirePermission>
  );
}
