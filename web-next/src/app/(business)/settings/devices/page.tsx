'use client';

// The counters.
//
// # "Pending" means two different things
//
// On a PAIRED till it is the ordinary state between registering the counter and
// the machine enrolling with a code: nothing is wrong, and somebody needs to go
// and type six characters into that machine. On a SESSION till — a counter
// somebody opens in a browser, which is the web-first default — it should never
// last, because the authorisation is the person's.
//
// One word for both would send a manager to fix a till that is working
// correctly, so the two states are named differently and only the first is
// offered a code.
//
// # An enrolment code is shown once
//
// The server keeps a hash and the response is the only copy. The panel says so
// rather than implying it can be looked up later, and stays until dismissed.
//
// # Revoking is not suspending
//
// Suspending stops a till selling and can be undone. Revoking ends its
// credential: the machine has to enrol again from nothing. A screen that made
// them look alike would get one used where the other was meant.

import { MonitorSmartphone } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { TerminalSettingsPanel } from './settings';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import {
  BINDINGS,
  needsEnrolmentCode,
  needsSigningUnit,
  terminalState,
  type Terminal,
} from '@/lib/devices/hardware';
import { useT, type Key } from '@/lib/i18n/locale';

interface Branch {
  id: string;
  code: string;
  name: string;
}

interface EGSUnit {
  id: string;
  label: string;
}

const STATE_LABEL: Record<string, Key> = {
  working: 'nx.dev.working',
  awaiting_machine: 'nx.dev.awaitingMachine',
  awaiting_registration: 'nx.dev.awaitingRegistration',
  suspended: 'nx.dev.suspended',
  revoked: 'nx.dev.revoked',
};

const STATE_TONE: Record<string, 'positive' | 'caution' | 'critical' | 'neutral'> = {
  working: 'positive',
  awaiting_machine: 'caution',
  awaiting_registration: 'critical',
  suspended: 'neutral',
  revoked: 'critical',
};

const BINDING_LABEL: Record<string, Key> = {
  session: 'nx.dev.bindingSession',
  paired: 'nx.dev.bindingPaired',
};

/** The enrolment code, shown once and never retrievable. */
function EnrolmentCode({
  label,
  code,
  onDismiss,
}: {
  label: string;
  code: string;
  onDismiss: () => void;
}) {
  const t = useT();
  return (
    <section
      className="mb-6 rounded-md border border-caution/25 bg-caution-subtle p-4"
      aria-labelledby="code-title"
    >
      <h2 id="code-title" className="text-card-title font-semibold text-caution-fg">
        {t('nx.dev.codeTitle', { till: label })}
      </h2>
      <p className="mt-1 max-w-prose text-body text-caution-fg">
        {t('nx.dev.codeBody')}
      </p>
      <p className="num mt-3 rounded-sm border border-line bg-surface px-3 py-2 text-card-title font-semibold tracking-widest select-all">
        {code}
      </p>
      <div className="mt-3">
        <Button size="sm" onClick={onDismiss}>
          {t('nx.dev.codeDismiss')}
        </Button>
      </div>
    </section>
  );
}

function DevicesScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const { market } = useCompany();
  const grants = useGrants();
  const mayManage = grants.can('devices.manage');

  const terminals = useApiList<Terminal>(
    scope ? '/devices' : null,
    scope ?? undefined,
  );
  const branches = useApiList<Branch>(
    scope ? '/devices/stores' : null,
    scope ?? undefined,
  );
  const units = useApiList<EGSUnit>(
    scope && needsSigningUnit(market) ? '/einvoicing/units' : null,
    scope ?? undefined,
  );

  const [adding, setAdding] = useState(false);
  const [label, setLabel] = useState('');
  const [storeID, setStoreID] = useState('');
  const [binding, setBinding] = useState<string>('session');
  const [unitID, setUnitID] = useState('');
  const [issued, setIssued] = useState<{ label: string; code: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  // Which counter is open for configuration. One at a time: two open
  // editors would let somebody save two sets of settings from one reading
  // of the page, and the route only sends what changed.
  const [configuring, setConfiguring] = useState<Terminal | null>(null);

  const rows = terminals.data?.data ?? [];
  const signingRequired = needsSigningUnit(market);

  async function register() {
    if (!scope || !label.trim() || !storeID) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      await api.post(`/devices?company_id=${scope.company_id}`, {
        terminal_label: label.trim(),
        store_id: storeID,
        binding,
        ...(unitID ? { egs_unit_id: unitID } : {}),
      });
      setLabel('');
      setAdding(false);
      void terminals.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function issueCode(terminal: Terminal) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      const out = await api.post<{ code?: string; enrolment_code?: string }>(
        `/devices/${terminal.id}/enrolment-code?company_id=${scope.company_id}`,
        {},
      );
      const code = out.code ?? out.enrolment_code ?? '';
      if (code) setIssued({ label: terminal.terminal_label, code });
      void terminals.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function setActive(terminal: Terminal, active: boolean) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/devices/${terminal.id}/active?company_id=${scope.company_id}`,
        { active },
      );
      void terminals.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function revoke(terminal: Terminal) {
    if (!scope) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(
        `/devices/${terminal.id}/revoke?company_id=${scope.company_id}`,
        {},
      );
      void terminals.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Terminal>[] = [
    {
      key: 'till',
      header: t('nx.dev.colTill'),
      primary: true,
      cell: (d) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{d.terminal_label}</span>
          <span className="text-caption text-muted">{d.store}</span>
        </span>
      ),
    },
    {
      key: 'binding',
      header: t('nx.dev.colBinding'),
      width: 'w-44',
      cell: (d) => (
        <span className="flex flex-col gap-0.5">
          <span>{t(BINDING_LABEL[d.binding] ?? 'nx.dev.bindingSession')}</span>
          <span className="text-caption text-muted">
            {d.binding === 'paired'
              ? t('nx.dev.bindingPairedHint')
              : t('nx.dev.bindingSessionHint')}
          </span>
        </span>
      ),
    },
    {
      key: 'signing',
      header: t('nx.dev.colSigning'),
      secondary: true,
      width: 'w-40',
      cell: (d) =>
        d.egs_unit ? (
          <span className="flex flex-col gap-0.5">
            <span className="text-muted">{d.egs_unit}</span>
            {d.csid_status ? (
              <span className="num text-caption text-muted">{d.csid_status}</span>
            ) : null}
          </span>
        ) : (
          <span className="text-muted">—</span>
        ),
    },
    {
      key: 'state',
      header: t('nx.dev.colState'),
      width: 'w-44',
      cell: (d) => {
        const state = terminalState(d);
        return (
          <span className="flex flex-col gap-1">
            <Badge tone={STATE_TONE[state]}>{t(STATE_LABEL[state] as Key)}</Badge>
            {d.pending_code ? (
              <span className="text-caption text-muted">
                {t('nx.dev.codeOutstanding')}
              </span>
            ) : null}
          </span>
        );
      },
    },
    {
      key: 'actions',
      header: t('nx.dev.colActions'),
      width: 'w-64',
      cell: (d) => {
        if (!mayManage) return null;
        const state = terminalState(d);
        return (
          <span className="flex flex-wrap gap-2">
            {needsEnrolmentCode(d) ? (
              <Button size="sm" disabled={busy} onClick={() => void issueCode(d)}>
                {d.pending_code ? t('nx.dev.newCode') : t('nx.dev.issueCode')}
              </Button>
            ) : null}
            {state === 'working' ? (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => void setActive(d, false)}
              >
                {t('nx.dev.suspend')}
              </Button>
            ) : null}
            {state === 'suspended' ? (
              <Button size="sm" disabled={busy} onClick={() => void setActive(d, true)}>
                {t('nx.dev.restore')}
              </Button>
            ) : null}
            {state !== 'revoked' ? (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => setConfiguring(d)}
              >
                {t('nx.dev.configure')}
              </Button>
            ) : null}
            {state !== 'revoked' ? (
              // A different act from suspending, and named differently: this
              // ends the credential and the machine must enrol again.
              <Button
                size="sm"
                variant="destructive"
                disabled={busy}
                onClick={() => void revoke(d)}
              >
                {t('nx.dev.revoke')}
              </Button>
            ) : null}
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.dev.title')}
        description={t('nx.dev.subtitle')}
        actions={
          mayManage ? (
            <Button variant="primary" onClick={() => setAdding((v) => !v)}>
              {t('nx.dev.register')}
            </Button>
          ) : null
        }
      />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      {issued ? (
        <EnrolmentCode
          label={issued.label}
          code={issued.code}
          onDismiss={() => setIssued(null)}
        />
      ) : null}

      {adding ? (
        <Panel
          className="mb-5"
          title={t('nx.dev.registerTitle')}
          description={t('nx.dev.registerHint')}
        >
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Field
              name="terminal_label"
              label={t('nx.dev.tillName')}
              hint={t('nx.dev.tillNameHint')}
              error={fieldErrors.terminal_label}
              required
            >
              <Input value={label} onChange={(e) => setLabel(e.target.value)} />
            </Field>
            <Field
              name="store_id"
              label={t('nx.dev.branch')}
              error={fieldErrors.store_id}
              required
            >
              <Select value={storeID} onChange={(e) => setStoreID(e.target.value)}>
                <option value="">{t('nx.dev.chooseBranch')}</option>
                {(branches.data?.data ?? []).map((b) => (
                  <option key={b.id} value={b.id}>
                    {b.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              name="binding"
              label={t('nx.dev.binding')}
              hint={
                binding === 'paired'
                  ? t('nx.dev.bindingPairedHint')
                  : t('nx.dev.bindingSessionHint')
              }
            >
              <Select value={binding} onChange={(e) => setBinding(e.target.value)}>
                {BINDINGS.map((b) => (
                  <option key={b} value={b}>
                    {t(BINDING_LABEL[b] as Key)}
                  </option>
                ))}
              </Select>
            </Field>
            {/* Only where the market obliges it. A shop with no e-invoicing
                obligation has nothing to put in this box. */}
            {signingRequired ? (
              <Field
                name="egs_unit_id"
                label={t('nx.dev.signingUnit')}
                hint={t('nx.dev.signingUnitHint')}
                error={fieldErrors.egs_unit_id}
                required
              >
                <Select value={unitID} onChange={(e) => setUnitID(e.target.value)}>
                  <option value="">{t('nx.dev.chooseUnit')}</option>
                  {(units.data?.data ?? []).map((u) => (
                    <option key={u.id} value={u.id}>
                      {u.label}
                    </option>
                  ))}
                </Select>
              </Field>
            ) : null}
          </div>
          <div className="mt-4 flex gap-3">
            <Button
              variant="primary"
              disabled={busy || !label.trim() || !storeID}
              onClick={() => void register()}
            >
              {t('nx.dev.registerIt')}
            </Button>
            <Button variant="ghost" onClick={() => setAdding(false)}>
              {t('nx.dev.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {terminals.error ? (
        <ErrorState error={terminals.error} onRetry={() => void terminals.refetch()} />
      ) : null}
      {/* One counter's own configuration: which warehouse it sells out of,
          which printer it prints on, whether it may take a sale with no
          customer named. Both routes were live and reachable from nothing. */}
      {configuring && scope ? (
        <TerminalSettingsPanel
          companyId={scope.company_id}
          deviceId={configuring.id}
          label={configuring.terminal_label}
          mayManage={mayManage}
          onClose={() => setConfiguring(null)}
        />
      ) : null}

      {terminals.isLoading && !terminals.data ? <TableSkeleton columns={5} /> : null}

      {!terminals.isLoading && !terminals.error && rows.length === 0 ? (
        <EmptyState
          icon={MonitorSmartphone}
          title={t('nx.dev.emptyTitle')}
          description={t('nx.dev.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.dev.title')}
          columns={columns}
          rows={rows}
          rowKey={(d) => d.id}
        />
      ) : null}
    </>
  );
}

export default function DevicesPage() {
  return (
    <RequirePermission anyOf={['devices.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <DevicesScreen />
      </Suspense>
    </RequirePermission>
  );
}
