'use client';

// D5's approval inbox: everything waiting for somebody to say yes.
//
// # Two lists, because "what happened to mine" is a different question
//
// `GET /approvals` is everything awaiting sign-off; `GET /approvals/mine` is
// what the caller themselves asked for. The route comment is explicit that
// these are separate on purpose — somebody chasing their own request should not
// have to read past everybody else's queue to find it. So they are tabs, and
// the queue is the one that opens, because that is the one with work in it.
//
// # A step count, not a spinner
//
// `current_step` and `steps_total` are both on the row, and a screen that
// showed neither would leave an approver wondering whether their signature was
// the last one needed. "2 of 3" is the difference between approving a thing and
// finishing it.
//
// # Approving is not the same authority as asking
//
// `approval.view` reads; `approval.decide` decides. Somebody who may see the
// queue but not act on it gets the list without the buttons, rather than
// buttons that refuse — and the server refuses regardless, which is what makes
// the hidden button a convenience rather than the boundary.
//
// # Escalation is on demand, and that is deliberate
//
// The route moves what has waited too long only when somebody presses it,
// rather than on a timer, so an escalation is always attributable to a person.
// The button says how many it moved, because pressing it and being told nothing
// happened is a different outcome from pressing it and moving eleven.

import { Inbox } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { Tabs } from '@/components/ui/tabs';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

interface Decision {
  step: number;
  decision: string;
  reason?: string;
  decided_by?: string;
  decided_at: string;
}

interface ApprovalRequest {
  id: string;
  subject: string;
  subject_id: string;
  summary: string;
  amount?: string;
  currency?: string;
  status: string;
  current_step: number;
  steps_total: number;
  rule_name?: string;
  requested_by?: string;
  requested_at: string;
  escalate_at?: string;
  decisions?: Decision[];
}

const STATUS_TONE: Record<string, 'neutral' | 'caution' | 'positive' | 'critical'> = {
  pending: 'caution',
  escalated: 'critical',
  approved: 'positive',
  rejected: 'critical',
  cancelled: 'neutral',
};

type TabId = 'queue' | 'mine';

function ApprovalsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayDecide = grants.can('approval.decide');

  const [tab, setTab] = useUrlState('tab');
  const active: TabId = tab === 'mine' ? 'mine' : 'queue';

  const path = scope
    ? active === 'mine'
      ? '/approvals/mine'
      : '/approvals'
    : null;
  const { data, isLoading, error, refetch } = useApiList<ApprovalRequest>(path, {
    company_id: scope?.company_id,
  });

  const [deciding, setDeciding] = useState<string | null>(null);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const rows = data?.data ?? [];

  async function decide(request: ApprovalRequest, approve: boolean) {
    if (!scope) return;
    // A refusal must say why. The server requires it and the form asks for it
    // rather than collecting a 400.
    if (!approve && reason.trim() === '') return;
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      await api.post(
        `/approvals/${request.id}/decide?company_id=${scope.company_id}`,
        { approve, reason: reason.trim() },
      );
      setDeciding(null);
      setReason('');
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function escalate() {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    setNote(null);
    try {
      const out = await api.post<{ escalated: number }>(
        `/approvals/escalate?company_id=${scope.company_id}`,
        {},
      );
      // Nothing to escalate is an outcome, not a failure.
      setNote(
        out.escalated > 0
          ? t('nx.apr.escalated', { count: String(out.escalated) })
          : t('nx.apr.escalatedNone'),
      );
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<ApprovalRequest>[] = [
    {
      key: 'summary',
      header: t('nx.apr.what'),
      primary: true,
      cell: (x) => (
        <span className="flex flex-col">
          <span>{x.summary}</span>
          <span className="text-caption text-muted">
            {x.subject}
            {x.rule_name ? ` · ${x.rule_name}` : ''}
          </span>
        </span>
      ),
    },
    {
      key: 'amount',
      header: t('nx.apr.amount'),
      numeric: true,
      width: 'w-36',
      // Never mirrored, and the currency is named: an approver signing off
      // "12,000.00" is entitled to know whether that is riyals or taka.
      cell: (x) =>
        x.amount ? (
          <span className="num" dir="ltr">
            {x.amount} {x.currency ?? ''}
          </span>
        ) : (
          <span className="text-muted">—</span>
        ),
    },
    {
      key: 'step',
      header: t('nx.apr.step'),
      width: 'w-24',
      // "2 of 3" tells an approver whether their signature finishes it.
      cell: (x) => (
        <span className="num">
          {t('nx.apr.stepOf', {
            step: String(x.current_step),
            total: String(x.steps_total),
          })}
        </span>
      ),
    },
    {
      key: 'who',
      header: t('nx.apr.askedBy'),
      secondary: true,
      cell: (x) => x.requested_by ?? '—',
    },
    {
      key: 'when',
      header: t('nx.apr.asked'),
      width: 'w-28',
      cell: (x) => (
        <time dateTime={x.requested_at}>{x.requested_at.slice(0, 10)}</time>
      ),
    },
    {
      key: 'status',
      header: t('nx.apr.status'),
      width: 'w-32',
      cell: (x) => (
        <Badge tone={STATUS_TONE[x.status] ?? 'neutral'}>
          {t(`nx.apr.state.${x.status}` as 'nx.apr.state.pending')}
        </Badge>
      ),
    },
  ];

  if (mayDecide && active === 'queue') {
    columns.push({
      key: 'decide',
      header: t('nx.apr.decide'),
      width: 'w-44',
      cell: (x) =>
        x.status !== 'pending' && x.status !== 'escalated' ? (
          <span className="text-muted">—</span>
        ) : deciding === x.id ? (
          <span className="text-caption text-muted">{t('nx.apr.below')}</span>
        ) : (
          <Button size="sm" variant="ghost" onClick={() => setDeciding(x.id)}>
            {t('nx.apr.open')}
          </Button>
        ),
    });
  }

  const open = rows.find((r) => r.id === deciding);

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader
        title={t('nx.apr.title')}
        description={t('nx.apr.subtitle')}
        actions={
          mayDecide ? (
            <Button
              variant="secondary"
              busy={busy}
              busyLabel={t('nx.apr.escalating')}
              onClick={() => void escalate()}
            >
              {t('nx.apr.escalate')}
            </Button>
          ) : null
        }
      />

      <Tabs<TabId>
        label={t('nx.apr.tabsLabel')}
        items={[
          { id: 'queue', label: t('nx.apr.tabQueue') },
          { id: 'mine', label: t('nx.apr.tabMine') },
        ]}
        value={active}
        onChange={(id) => {
          setTab(id);
          setDeciding(null);
        }}
      />

      <FormError message={actionError} className="my-4" />
      {note ? (
        <p className="my-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      {open ? (
        <Panel title={open.summary} className="my-4">
          <p className="text-body text-muted">
            {t('nx.apr.decidingStep', {
              step: String(open.current_step),
              total: String(open.steps_total),
            })}
          </p>

          {open.decisions && open.decisions.length > 0 ? (
            <ul className="mt-4 space-y-2 border-t border-line pt-4">
              {open.decisions.map((d) => (
                <li key={`${d.step}-${d.decided_at}`} className="text-body">
                  <span className="num">{d.step}</span> ·{' '}
                  <span className="text-fg">{d.decision}</span>
                  {d.decided_by ? ` · ${d.decided_by}` : ''}
                  {d.reason ? (
                    <span className="text-muted"> — {d.reason}</span>
                  ) : null}
                </li>
              ))}
            </ul>
          ) : null}

          <div className="mt-4">
            <Field
              name="reason"
              label={t('nx.apr.reason')}
              hint={t('nx.apr.reasonHint')}
            >
              <Textarea
                rows={2}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </Field>
          </div>

          <div className="mt-4 flex flex-wrap gap-2">
            <Button busy={busy} onClick={() => void decide(open, true)}>
              {t('nx.apr.approve')}
            </Button>
            <Button
              variant="destructive"
              busy={busy}
              // A refusal without a reason is not something to send and be
              // refused for; the button says so by staying unavailable.
              disabled={reason.trim() === ''}
              onClick={() => void decide(open, false)}
            >
              {t('nx.apr.reject')}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setDeciding(null);
                setReason('');
              }}
            >
              {t('nx.apr.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {isLoading && !data ? <TableSkeleton columns={6} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={Inbox}
          title={
            active === 'mine' ? t('nx.apr.emptyMineTitle') : t('nx.apr.emptyTitle')
          }
          description={
            active === 'mine' ? t('nx.apr.emptyMineDesc') : t('nx.apr.emptyDesc')
          }
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<ApprovalRequest>
          rows={rows}
          columns={columns}
          rowKey={(x) => x.id}
          caption={t('nx.apr.caption')}
        />
      ) : null}
    </>
  );
}

export default function ApprovalsPage() {
  return (
    <RequirePermission anyOf={['approval.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <ApprovalsScreen />
      </Suspense>
    </RequirePermission>
  );
}
