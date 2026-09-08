'use client';

// Which modules a client may use, and the exceptions to their plan.
//
// # Why this exists
//
// `PUT /platform/tenants/{id}/features` is H5's commercial flexibility — a
// module granted to one client independently of their tier, with the reason
// recorded — and it has been live and uncalled since the module gate landed.
// There was no GET beside it either, so the operator who may grant a module
// had no way to see which modules that client already had. Both halves are
// here: the read was added for this screen.
//
// # Three states, not two
//
// A module is in the plan or it is not; on top of that a client may have an
// exception either way. So a row can read "in their plan and allowed", "not in
// their plan but granted", or "in their plan and withdrawn" — and the third is
// the one an operator most needs to see, because it is the one that will
// produce a support call.
//
// "Back to the plan" is a third button rather than a different screen: one row,
// three states, and clearing an exception is not a different kind of act from
// making one.
//
// # The reason is required, and that is the point
//
// The service refuses an exception without one. Six months later an exception
// with no reason is indistinguishable from a mistake, which is the same
// argument that makes a legal hold carry one.

import { useEffect, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

interface Entitlement {
  feature: string;
  /** What the tier itself says. */
  in_plan: boolean;
  /** What is true after any exception. */
  allowed: boolean;
  /** Set only on an exception; a reason for the plan itself would be a lie. */
  reason?: string;
  expires_on?: string;
}

export function ModulesPanel({ tenantId }: { tenantId: string }) {
  const t = useT();

  const { data, isLoading, error, refetch } = useApiList<Entitlement>(
    tenantId ? `/platform/tenants/${tenantId}/features` : null,
  );

  const [editing, setEditing] = useState<Entitlement | null>(null);
  const [reason, setReason] = useState('');
  const [expires, setExpires] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string> | null>(null);

  // A draft typed for one client must never be sent for another.
  useEffect(() => {
    setEditing(null);
    setReason('');
    setExpires('');
    setActionError(null);
  }, [tenantId]);

  const rows = data?.data ?? [];

  async function apply(enabled: boolean, clear = false) {
    if (!editing) return;
    setBusy(true);
    setActionError(null);
    setFields(null);
    try {
      await api.put(`/platform/tenants/${tenantId}/features`, {
        feature: editing.feature,
        enabled,
        reason,
        expires_on: expires,
        clear,
      });
      setEditing(null);
      setReason('');
      setExpires('');
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const yesNo = (v: boolean) => (v ? t('nx.plat.featYes') : t('nx.plat.featNo'));

  const columns: Column<Entitlement>[] = [
    {
      key: 'module',
      header: t('nx.plat.featColModule'),
      primary: true,
      cell: (e) => <span className="num">{e.feature}</span>,
    },
    {
      key: 'plan',
      header: t('nx.plat.featColPlan'),
      secondary: true,
      width: 'w-32',
      cell: (e) => <span className="text-muted">{yesNo(e.in_plan)}</span>,
    },
    {
      key: 'now',
      header: t('nx.plat.featColNow'),
      width: 'w-44',
      // The word as well as the colour: a tone alone is not a signal somebody
      // with a colour vision deficiency can read, and this is the column that
      // decides whether a client can work.
      cell: (e) => (
        <span className="flex flex-wrap items-center gap-2">
          <Badge tone={e.allowed ? 'positive' : 'neutral'}>{yesNo(e.allowed)}</Badge>
          {e.allowed !== e.in_plan ? (
            <Badge tone="caution">{t('nx.plat.featException')}</Badge>
          ) : null}
        </span>
      ),
    },
    {
      key: 'why',
      header: t('nx.plat.featColWhy'),
      cell: (e) => (
        <span className="flex flex-col gap-0.5">
          <span className="text-muted">{e.reason ?? '—'}</span>
          {e.expires_on ? (
            <span className="text-caption text-subtle">
              {t('nx.plat.featUntil', { date: e.expires_on })}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      width: 'w-28',
      cell: (e) => (
        <span className="flex justify-end">
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setEditing(e);
              setReason(e.reason ?? '');
              setExpires(e.expires_on ?? '');
              setActionError(null);
              setFields(null);
            }}
          >
            {e.allowed ? t('nx.plat.featWithdraw') : t('nx.plat.featGrant')}
          </Button>
        </span>
      ),
    },
  ];

  return (
    <Panel
      title={t('nx.plat.featuresTitle')}
      description={t('nx.plat.featuresHint')}
      className="mb-5"
      flush={rows.length > 0 && !editing}
    >
      {error ? <FormError message={messageFor(error, t)} /> : null}
      {isLoading && rows.length === 0 ? <TableSkeleton columns={5} rows={4} /> : null}

      {editing ? (
        <div className="p-4">
          <h3 className="text-label font-medium text-fg">
            {t('nx.plat.featEditing', { feature: editing.feature })}
          </h3>

          <div className="mt-3 grid gap-4 sm:grid-cols-2">
            <Field
              name="reason"
              label={t('nx.plat.featReason')}
              hint={t('nx.plat.featReasonHint')}
              error={fields?.reason}
            >
              <Textarea
                rows={2}
                value={reason}
                onChange={(ev) => setReason(ev.target.value)}
                required
              />
            </Field>
            <Field
              name="expires_on"
              label={t('nx.plat.featExpires')}
              hint={t('nx.plat.featExpiresHint')}
              error={fields?.expires_on}
            >
              <Input
                type="date"
                dir="ltr"
                className="num"
                value={expires}
                onChange={(ev) => setExpires(ev.target.value)}
              />
            </Field>
          </div>

          {actionError ? <FormError message={actionError} /> : null}

          <div className="mt-4 flex flex-wrap gap-2">
            <Button
              variant="primary"
              disabled={busy}
              onClick={() => void apply(!editing.allowed)}
            >
              {editing.allowed ? t('nx.plat.featWithdraw') : t('nx.plat.featGrant')}
            </Button>
            {editing.allowed !== editing.in_plan ? (
              <Button disabled={busy} onClick={() => void apply(editing.in_plan, true)}>
                {t('nx.plat.featClear')}
              </Button>
            ) : null}
            <Button variant="ghost" disabled={busy} onClick={() => setEditing(null)}>
              {t('nx.plat.featCancel')}
            </Button>
          </div>
        </div>
      ) : null}

      {!isLoading && rows.length === 0 ? (
        <p className="text-body text-muted">{t('nx.plat.featNone')}</p>
      ) : null}

      {rows.length > 0 && !editing ? (
        <DataTable
          caption={t('nx.plat.featuresTitle')}
          columns={columns}
          rows={rows}
          rowKey={(e) => e.feature}
          className="rounded-none border-0"
        />
      ) : null}
    </Panel>
  );
}
