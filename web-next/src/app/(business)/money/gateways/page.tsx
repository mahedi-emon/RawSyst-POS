'use client';

// Card connections, and the money arriving in the bank days later.
//
// # Two permissions on one screen
//
// The connections are `gateway.view`; the deposits are `accounting.view`,
// because banking a day's card takings is an accounting act. The page opens on
// the first and the settlement half appears only for somebody holding the
// second — rather than gating the whole screen on both, which would hide the
// card machine from the person who looks after it.
//
// # Configure, check, then switch on
//
// The server refuses to activate a live connection that has never answered a
// check. So the screen offers one next step at a time. A toggle that refuses
// teaches the rule by failing, with a customer at the counter; a step that is
// not offered yet teaches it before anything is lost.
//
// # The form comes from the server
//
// `/payment-providers` says what each acquirer needs, field by field, and which
// half is sealed. Nothing here holds that list: a screen with its own copy asks
// for a key the provider stopped using and omits the one it now needs.

import { CreditCard } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel, type Tone } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  bankable,
  health,
  missingFields,
  nextStep,
  publicFields,
  secretField,
  takingCards,
  totalOf,
  type Attempt,
  type Gateway,
  type Health,
  type PendingTender,
  type Provider,
  type Step,
} from '@/lib/money/settlement';

const HEALTH_TONE: Record<Health, Tone> = {
  answering: 'positive',
  not_answering: 'critical',
  never_checked: 'neutral',
};

const HEALTH_LABEL: Record<Health, Key> = {
  answering: 'nx.gw.answering',
  not_answering: 'nx.gw.notAnswering',
  never_checked: 'nx.gw.neverChecked',
};

const STEP_LABEL: Record<Step, Key> = {
  check: 'nx.gw.stepCheck',
  activate: 'nx.gw.stepActivate',
  ready: 'nx.gw.stepReady',
  testing: 'nx.gw.stepTesting',
};

function GatewaysScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayManage = grants.can('gateway.manage');
  const maySettle = grants.can('accounting.view');

  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState(false);
  const [providerKey, setProviderKey] = useState('');
  const [label, setLabel] = useState('');
  const [mode, setMode] = useState('test');
  const [settings, setSettings] = useState<Record<string, string>>({});
  const [secret, setSecret] = useState('');

  const providers = useApi<{ providers: Provider[] }>(
    scope ? '/payment-providers' : null,
    scope ?? undefined,
  );
  const gateways = useApi<{ gateways: Gateway[] }>(
    scope ? '/payment-gateways' : null,
    scope ?? undefined,
  );
  const attempts = useApi<{ attempts: Attempt[] }>(
    scope ? '/payment-attempts' : null,
    scope ?? undefined,
  );
  const pending = useApi<{ data: PendingTender[] }>(
    scope && maySettle ? '/settlement/pending' : null,
    scope ?? undefined,
  );

  const rows = gateways.data?.gateways ?? [];
  const catalogue = providers.data?.providers ?? [];
  const chosen = catalogue.find((p) => p.key === providerKey) ?? null;
  const missing = chosen ? missingFields(chosen, settings, secret, true) : [];
  const unbanked = bankable(pending.data?.data ?? []);

  async function run(work: () => Promise<unknown>) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      await work();
      await gateways.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const q = `?company_id=${scope?.company_id ?? ''}`;

  const columns: Column<Gateway>[] = [
    {
      key: 'label',
      header: t('nx.gw.colConnection'),
      primary: true,
      cell: (g) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{g.label}</span>
          <span className="text-caption text-muted">
            {catalogue.find((p) => p.key === g.provider)?.name ?? g.provider}
            {g.mode === 'test' ? ` · ${t('nx.gw.testMode')}` : ''}
          </span>
        </span>
      ),
    },
    {
      key: 'health',
      header: t('nx.gw.colAnswering'),
      width: 'w-64',
      cell: (g) => (
        <span className="flex flex-col gap-1">
          <Badge tone={HEALTH_TONE[health(g)]}>{t(HEALTH_LABEL[health(g)])}</Badge>
          {/* The provider's own words about what went wrong. A failed check
              with no reason is one somebody has to reproduce to understand. */}
          {g.last_check_note ? (
            <span className="text-caption text-muted">{g.last_check_note}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'step',
      header: t('nx.gw.colNext'),
      width: 'w-40',
      cell: (g) => {
        const step = nextStep(g);
        return (
          <Badge tone={step === 'ready' ? 'positive' : step === 'testing' ? 'info' : 'caution'}>
            {t(STEP_LABEL[step])}
          </Badge>
        );
      },
    },
    {
      key: 'key',
      header: t('nx.gw.colKey'),
      secondary: true,
      width: 'w-36',
      cell: (g) => (
        // Whether one is stored, never the key. Nothing on this screen can
        // show it back, which is why an empty box on an edit leaves it alone.
        <span className="text-muted">
          {t(g.has_secret ? 'nx.gw.keyStored' : 'nx.gw.noKey')}
        </span>
      ),
    },
    ...(mayManage
      ? [
          {
            key: 'act',
            header: t('nx.gw.colAction'),
            width: 'w-40',
            cell: (g: Gateway) => {
              const step = nextStep(g);
              if (step === 'check' || step === 'testing') {
                return (
                  <Button
                    variant="ghost"
                    disabled={busy}
                    onClick={() =>
                      void run(() => api.post(`/payment-gateways/${g.id}/check${q}`, {}))
                    }
                  >
                    {t('nx.gw.check')}
                  </Button>
                );
              }
              if (step === 'activate') {
                return (
                  <Button
                    variant="ghost"
                    disabled={busy}
                    onClick={() =>
                      void run(() =>
                        api.put(`/payment-gateways/${g.id}${q}`, {
                          provider: g.provider,
                          label: g.label,
                          mode: g.mode,
                          settings: g.settings,
                          methods: g.methods,
                          is_active: true,
                        }),
                      )
                    }
                  >
                    {t('nx.gw.switchOn')}
                  </Button>
                );
              }
              return <span className="text-caption text-muted">—</span>;
            },
          },
        ]
      : []),
  ];

  return (
    <>
      <PageHeader
        title={t('nx.gw.title')}
        description={t('nx.gw.subtitle')}
        actions={
          mayManage ? (
            <Button variant="primary" onClick={() => setAdding((v) => !v)}>
              {t('nx.gw.add')}
            </Button>
          ) : null
        }
      />

      <FormError message={error} className="mb-4" />

      {/* The one question a shopkeeper opens this screen to answer. */}
      {!gateways.isLoading && rows.length > 0 ? (
        <Panel
          className="mb-6"
          title={t('nx.gw.standingTitle')}
          actions={
            <Badge tone={takingCards(rows) ? 'positive' : 'critical'}>
              {t(takingCards(rows) ? 'nx.gw.canTakeCards' : 'nx.gw.cannotTakeCards')}
            </Badge>
          }
        >
          <p className="max-w-prose text-body text-muted">
            {t(takingCards(rows) ? 'nx.gw.canTakeCardsDesc' : 'nx.gw.cannotTakeCardsDesc')}
          </p>
        </Panel>
      ) : null}

      {adding && mayManage ? (
        <Panel
          className="mb-6"
          title={t('nx.gw.addTitle')}
          description={t('nx.gw.addDesc')}
          actions={
            <Button variant="ghost" onClick={() => setAdding(false)}>
              {t('nx.gw.cancel')}
            </Button>
          }
        >
          <div className="flex flex-wrap items-end gap-3">
            <Field name="provider" label={t('nx.gw.provider')}>
              <Select
                value={providerKey}
                onChange={(e) => {
                  setProviderKey(e.target.value);
                  // Cleared on purpose: the fields belong to the provider, and
                  // carrying a value across would send one acquirer's key to
                  // another.
                  setSettings({});
                  setSecret('');
                }}
              >
                <option value="">{t('nx.gw.chooseProvider')}</option>
                {catalogue.map((p) => (
                  <option key={p.key} value={p.key}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field name="label" label={t('nx.gw.label')} hint={t('nx.gw.labelHint')}>
              <Input value={label} onChange={(e) => setLabel(e.target.value)} />
            </Field>
            <Field name="mode" label={t('nx.gw.mode')}>
              <Select value={mode} onChange={(e) => setMode(e.target.value)}>
                <option value="test">{t('nx.gw.modeTest')}</option>
                <option value="live">{t('nx.gw.modeLive')}</option>
              </Select>
            </Field>
          </div>

          {chosen ? (
            <div className="mt-4 flex flex-wrap items-end gap-3">
              {publicFields(chosen).map((f) => (
                <Field key={f.key} name={f.key} label={f.label} hint={f.hint}>
                  <Input
                    value={settings[f.key] ?? ''}
                    onChange={(e) =>
                      setSettings((s) => ({ ...s, [f.key]: e.target.value }))
                    }
                  />
                </Field>
              ))}
              {secretField(chosen) ? (
                <Field
                  name="secret"
                  label={secretField(chosen)?.label ?? ''}
                  hint={secretField(chosen)?.hint}
                >
                  <Input
                    type="password"
                    autoComplete="off"
                    value={secret}
                    onChange={(e) => setSecret(e.target.value)}
                  />
                </Field>
              ) : null}
              <Button
                variant="primary"
                disabled={busy || !label.trim() || missing.length > 0}
                onClick={() =>
                  void run(async () => {
                    await api.post(`/payment-gateways${q}`, {
                      provider: chosen.key,
                      label: label.trim(),
                      mode,
                      settings,
                      // Sent only when typed. A blank box on an edit means
                      // "leave what is there", and the server reads it so.
                      ...(secret.trim() ? { secret: secret.trim() } : {}),
                      methods: chosen.methods,
                      // Never on at birth. A live connection cannot be
                      // switched on until it has answered, and the screen
                      // offers that as the next step instead.
                      is_active: false,
                    });
                    setAdding(false);
                    setLabel('');
                    setSettings({});
                    setSecret('');
                  })
                }
              >
                {t('nx.gw.save')}
              </Button>
            </div>
          ) : null}

          {chosen && missing.length > 0 ? (
            <p className="mt-3 max-w-prose text-caption text-muted">
              {t('nx.gw.stillNeeds', {
                fields: missing
                  .map((k) => chosen.fields.find((f) => f.key === k)?.label ?? k)
                  .join(', '),
              })}
            </p>
          ) : null}
        </Panel>
      ) : null}

      {gateways.error ? (
        <ErrorState error={gateways.error} onRetry={() => void gateways.refetch()} />
      ) : null}
      {gateways.isLoading && !gateways.data ? <TableSkeleton columns={5} /> : null}

      {!gateways.isLoading && !gateways.error && rows.length === 0 ? (
        <EmptyState
          icon={CreditCard}
          title={t('nx.gw.emptyTitle')}
          description={t('nx.gw.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.gw.title')}
          columns={columns}
          rows={rows}
          rowKey={(g) => g.id}
        />
      ) : null}

      {maySettle ? (
        <section className="mt-8">
          <h2 className="mb-1 text-card-title font-semibold text-fg">
            {t('nx.gw.bankingTitle')}
          </h2>
          <p className="mb-3 max-w-prose text-caption text-muted">
            {t('nx.gw.bankingDesc')}
          </p>
          {unbanked.length === 0 ? (
            <EmptyState
              icon={CreditCard}
              title={t('nx.gw.nothingPendingTitle')}
              description={t('nx.gw.nothingPendingDesc')}
            />
          ) : (
            <>
              <DataTable
                caption={t('nx.gw.bankingTitle')}
                columns={[
                  {
                    key: 'invoice',
                    header: t('nx.gw.colInvoice'),
                    primary: true,
                    cell: (p: PendingTender) => (
                      <span className="flex flex-col gap-0.5">
                        <span className="num font-medium">{p.invoice_number}</span>
                        <span className="text-caption text-muted">
                          {p.issued_at.slice(0, 10)}
                        </span>
                      </span>
                    ),
                  },
                  {
                    key: 'method',
                    header: t('nx.gw.colMethod'),
                    width: 'w-32',
                    cell: (p: PendingTender) => <span>{p.method}</span>,
                  },
                  {
                    key: 'ref',
                    header: t('nx.gw.colReference'),
                    secondary: true,
                    cell: (p: PendingTender) => (
                      <span className="num text-muted">{p.reference || '—'}</span>
                    ),
                  },
                  {
                    key: 'amount',
                    header: t('nx.gw.colAmount'),
                    width: 'w-36',
                    cell: (p: PendingTender) => (
                      <span className="num">
                        {p.amount} {p.currency}
                      </span>
                    ),
                  },
                ]}
                rows={unbanked}
                rowKey={(p) => p.tender_id}
              />
              <p className="mt-3 text-body">
                {t('nx.gw.pendingTotal', {
                  total: totalOf(unbanked),
                  n: String(unbanked.length),
                })}
              </p>
            </>
          )}
        </section>
      ) : null}

      {(attempts.data?.attempts ?? []).length > 0 ? (
        <section className="mt-8">
          <h2 className="mb-3 text-card-title font-semibold text-fg">
            {t('nx.gw.attemptsTitle')}
          </h2>
          <DataTable
            caption={t('nx.gw.attemptsTitle')}
            columns={[
              {
                key: 'when',
                header: t('nx.gw.colWhen'),
                primary: true,
                cell: (a: Attempt) => (
                  <span className="num">
                    {a.created_at.slice(0, 16).replace('T', ' ')}
                  </span>
                ),
              },
              {
                key: 'amount',
                header: t('nx.gw.colAmount'),
                width: 'w-36',
                cell: (a: Attempt) => (
                  <span className="num">
                    {a.amount} {a.currency}
                  </span>
                ),
              },
              {
                key: 'status',
                header: t('nx.gw.colOutcome'),
                cell: (a: Attempt) => (
                  <span className="flex flex-col gap-0.5">
                    <span>{a.status}</span>
                    {/* The acquirer's own words. A declined card that says
                        only "failed" sends a cashier to the wrong remedy. */}
                    {a.provider_message ? (
                      <span className="text-caption text-muted">
                        {a.provider_message}
                      </span>
                    ) : null}
                  </span>
                ),
              },
            ]}
            rows={attempts.data?.attempts ?? []}
            rowKey={(a) => a.id}
          />
        </section>
      ) : null}
    </>
  );
}

export default function GatewaysPage() {
  return (
    <RequirePermission anyOf={['gateway.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <GatewaysScreen />
      </Suspense>
    </RequirePermission>
  );
}
