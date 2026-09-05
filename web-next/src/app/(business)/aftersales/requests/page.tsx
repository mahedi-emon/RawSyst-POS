'use client';

// What customers have asked to send back, waiting on somebody to answer.
//
// # This is the request, not the return
//
// Accepting one here does not move stock, refund anything or touch the ledger.
// It answers the customer. The goods coming back and the money going out are
// the returns workflow, and conflating the two on this screen would let
// somebody believe a refund had been made because they pressed Accept.
//
// # A refusal has to say why, and the customer reads it
//
// `decision_note` goes back to the person who asked. That makes it the one
// field on this screen written for somebody outside the business, so the form
// says so — a note reading "no" is a sentence a customer will phone about.
//
// # Open first, everything on demand
//
// `?open=true` narrows to what still needs an answer, which is what somebody
// opens this screen to do. The decided ones stay reachable because "what did we
// tell them in March" is a question that gets asked.

import { PackageOpen } from 'lucide-react';
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

interface ReturnRequest {
  id: string;
  request_no: string;
  invoice_id?: string;
  invoice_no?: string;
  kind: string;
  reason: string;
  items: string;
  status: string;
  decision_note?: string;
  created_at: string;
  decided_at?: string;
  customer_name?: string;
}

// The four states the column allows. `requested` is the one awaiting an answer,
// and `completed` is a request whose goods have since come back through the
// returns workflow — which is why accepting one here is not the end of it.
const STATUS_TONE: Record<string, 'caution' | 'positive' | 'critical' | 'neutral'> = {
  requested: 'caution',
  accepted: 'positive',
  refused: 'critical',
  completed: 'neutral',
};

type TabId = 'open' | 'all';

function RequestsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayDecide = grants.can('portal.manage');

  const [tab, setTab] = useUrlState('tab');
  const active: TabId = tab === 'all' ? 'all' : 'open';

  const { data, isLoading, error, refetch } = useApiList<ReturnRequest>(
    scope ? '/portal/return-requests' : null,
    { company_id: scope?.company_id, open: active === 'open' ? 'true' : undefined },
  );

  const [deciding, setDeciding] = useState<ReturnRequest | null>(null);
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const rows = data?.data ?? [];

  async function decide(accept: boolean) {
    if (!scope || !deciding) return;
    // A refusal the customer cannot understand is worse than a slow answer.
    if (!accept && note.trim() === '') return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(
        `/portal/return-requests/${deciding.id}/decide?company_id=${scope.company_id}`,
        { accept, note: note.trim() },
      );
      setDeciding(null);
      setNote('');
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<ReturnRequest>[] = [
    {
      key: 'no',
      header: t('nx.rr.request'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col">
          <span className="num">{r.request_no}</span>
          <span className="text-caption text-muted">
            {r.customer_name ?? '—'}
            {r.invoice_no ? (
              <>
                {' · '}
                <span className="num">{r.invoice_no}</span>
              </>
            ) : null}
          </span>
        </span>
      ),
    },
    {
      key: 'kind',
      header: t('nx.rr.kind'),
      width: 'w-28',
      // A return and an exchange are different asks, and the word is the
      // customer's, so it is translated rather than printed as the column value.
      cell: (r) => (
        <span className="text-muted">
          {t(`nx.rr.kind.${r.kind}` as 'nx.rr.kind.return')}
        </span>
      ),
    },
    {
      key: 'items',
      header: t('nx.rr.items'),
      cell: (r) => r.items,
    },
    {
      key: 'reason',
      header: t('nx.rr.reason'),
      secondary: true,
      cell: (r) => <span className="text-muted">{r.reason}</span>,
    },
    {
      key: 'asked',
      header: t('nx.rr.asked'),
      width: 'w-28',
      cell: (r) => <time dateTime={r.created_at}>{r.created_at.slice(0, 10)}</time>,
    },
    {
      key: 'status',
      header: t('nx.rr.status'),
      width: 'w-40',
      cell: (r) => (
        <span className="flex flex-col gap-1">
          <Badge tone={STATUS_TONE[r.status] ?? 'neutral'}>
            {t(`nx.rr.state.${r.status}` as 'nx.rr.state.requested')}
          </Badge>
          {r.decision_note ? (
            <span className="text-caption text-muted">{r.decision_note}</span>
          ) : null}
        </span>
      ),
    },
  ];

  if (mayDecide) {
    columns.push({
      key: 'answer',
      header: t('nx.rr.answerHeader'),
      width: 'w-28',
      cell: (r) =>
        r.status !== 'requested' ? (
          <span className="text-muted">—</span>
        ) : (
          <Button size="sm" variant="ghost" onClick={() => setDeciding(r)}>
            {t('nx.rr.answer')}
          </Button>
        ),
    });
  }

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader title={t('nx.rr.title')} description={t('nx.rr.subtitle')} />

      <Tabs<TabId>
        label={t('nx.rr.tabsLabel')}
        items={[
          { id: 'open', label: t('nx.rr.tabOpen') },
          { id: 'all', label: t('nx.rr.tabAll') },
        ]}
        value={active}
        onChange={(id) => {
          setTab(id);
          setDeciding(null);
        }}
      />

      <FormError message={actionError} className="my-4" />

      {deciding ? (
        <Panel title={t('nx.rr.answering', { no: deciding.request_no })} className="my-4">
          <dl className="grid gap-3 sm:grid-cols-2">
            <div>
              <dt className="text-caption text-muted">{t('nx.rr.items')}</dt>
              <dd className="text-body text-fg">{deciding.items}</dd>
            </div>
            <div>
              <dt className="text-caption text-muted">{t('nx.rr.reason')}</dt>
              <dd className="text-body text-fg">{deciding.reason}</dd>
            </div>
          </dl>

          <div className="mt-4">
            <Field
              name="note"
              label={t('nx.rr.note')}
              hint={t('nx.rr.noteHint')}
            >
              <Textarea rows={2} value={note} onChange={(e) => setNote(e.target.value)} />
            </Field>
          </div>

          <p className="mt-4 text-body text-muted">{t('nx.rr.notARefund')}</p>

          <div className="mt-4 flex flex-wrap gap-2">
            <Button busy={busy} onClick={() => void decide(true)}>
              {t('nx.rr.accept')}
            </Button>
            <Button
              variant="destructive"
              busy={busy}
              disabled={note.trim() === ''}
              onClick={() => void decide(false)}
            >
              {t('nx.rr.refuse')}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setDeciding(null);
                setNote('');
              }}
            >
              {t('nx.rr.cancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {isLoading && !data ? <TableSkeleton columns={6} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={PackageOpen}
          title={active === 'open' ? t('nx.rr.emptyOpenTitle') : t('nx.rr.emptyTitle')}
          description={
            active === 'open' ? t('nx.rr.emptyOpenDesc') : t('nx.rr.emptyDesc')
          }
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<ReturnRequest>
          rows={rows}
          columns={columns}
          rowKey={(r) => r.id}
          caption={t('nx.rr.caption')}
        />
      ) : null}
    </>
  );
}

export default function RequestsPage() {
  return (
    <RequirePermission anyOf={['portal.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <RequestsScreen />
      </Suspense>
    </RequirePermission>
  );
}
