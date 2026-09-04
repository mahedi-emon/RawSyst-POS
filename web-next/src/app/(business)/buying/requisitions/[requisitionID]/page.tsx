'use client';

// One request, and the decision on it.
//
// # Approving is somebody else's job, and the screen is built that way
//
// `purchasing.request` raises one; `purchasing.approve_request` decides it. B5.1
// separates them so that asking cannot become authorising, and this screen shows
// the decision panel only to somebody who holds the second — not greyed out,
// absent. A disabled button that the requester can see is an invitation to ask
// why it is disabled.
//
// # A rejection must say why, and an approval need not
//
// `DecideRequisition` refuses a rejection with no note: "the requester has to be
// able to act on the answer — order less, order later, or stop asking — and
// 'rejected' alone tells them none of those." So the note is required on one
// path and optional on the other, and the hint says which is which as the
// buttons are pressed.

import { use, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  REQUISITION_STATUS,
  awaitingDecision,
  type Requisition,
  type RequisitionLine,
} from '@/lib/purchasing/sourcing';

function RequisitionScreen({ requisitionID }: { requisitionID: string }) {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApi<Requisition>(
    scope ? `/purchasing/requisitions/${requisitionID}` : null,
    scope ?? undefined,
  );

  const [note, setNote] = useState('');
  const [busy, setBusy] = useState<'approve' | 'reject' | null>(null);
  const [decideError, setDecideError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  async function decide(approve: boolean) {
    if (!scope) return;
    setBusy(approve ? 'approve' : 'reject');
    setDecideError(null);
    setFieldErrors({});
    try {
      await api.post(
        `/purchasing/requisitions/${requisitionID}/decision?company_id=${scope.company_id}`,
        { approve, note },
      );
      setNote('');
      await refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setDecideError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.req.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.req.title')} />
        <TableSkeleton columns={4} />
      </>
    );
  }

  const status = REQUISITION_STATUS[data.status];
  const lines = data.lines ?? [];
  // Absent rather than disabled: the requester should not see a control that
  // is not theirs and wonder why it will not move.
  const mayDecide =
    awaitingDecision(data.status) && grants.can('purchasing.approve_request');

  const columns: Column<RequisitionLine>[] = [
    {
      key: 'what',
      header: t('nx.npo.colProduct'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col gap-0.5">
          <span>{l.description}</span>
          {l.sku ? <span className="num text-caption text-muted">{l.sku}</span> : null}
        </span>
      ),
    },
    {
      key: 'qty',
      header: t('nx.req.colQty'),
      numeric: true,
      width: 'w-28',
      // Quantity and nothing else. A cost column here would hand
      // purchasing.request a permission it deliberately does not carry.
      cell: (l) => <span className="num">{formatQuantity(l.qty_requested)}</span>,
    },
    {
      key: 'note',
      header: t('nx.req.colNote'),
      secondary: true,
      cell: (l) => <span className="text-muted">{l.note || '—'}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{data.requisition_no}</span>
            {status ? <Badge tone={status.tone}>{t(status.key)}</Badge> : null}
          </span>
        }
        description={[
          t('nx.req.askedOn', {
            who: data.requested_by || '—',
            date: data.requested_at.slice(0, 10),
          }),
          data.needed_by ? t('nx.po.expectedOn', { date: data.needed_by }) : '',
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <Link
            href="/buying/requisitions"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.req.back')}
          </Link>
        }
      />

      <FormError message={decideError} className="mb-4" />

      {data.justification ? (
        <Panel className="mb-6" title={t('nx.req.why')}>
          <p className="max-w-prose text-body text-fg">{data.justification}</p>
        </Panel>
      ) : null}

      <Panel title={t('nx.req.what')} flush>
        <DataTable
          caption={t('nx.req.linesCaption')}
          columns={columns}
          rows={lines}
          rowKey={(l) => l.id}
          className="rounded-none border-0"
        />
      </Panel>

      {data.decided_at ? (
        <Panel className="mt-6">
          <p className="text-body text-muted">
            {t('nx.req.decidedBy', {
              who: data.decided_by || '—',
              date: data.decided_at.slice(0, 10),
            })}
          </p>
          {data.decision_note ? (
            <p className="mt-1 max-w-prose text-body text-fg">{data.decision_note}</p>
          ) : null}
        </Panel>
      ) : null}

      {mayDecide ? (
        <Panel className="mt-6" title={t('nx.req.decide')}>
          <Field
            label={t('nx.req.note')}
            hint={
              // The same box means two different things depending on which
              // button follows it, so the hint says which.
              busy === 'reject'
                ? t('nx.req.rejectNoteHint')
                : t('nx.req.approveNoteHint')
            }
            error={fieldErrors.decision_note}
          >
            <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={3} />
          </Field>
          <p className="mt-2 text-caption text-muted">{t('nx.req.rejectNoteHint')}</p>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button
              variant="primary"
              busy={busy === 'approve'}
              disabled={busy !== null}
              onClick={() => void decide(true)}
            >
              {t('nx.req.approve')}
            </Button>
            <Button
              variant="secondary"
              busy={busy === 'reject'}
              disabled={busy !== null || note.trim() === ''}
              onClick={() => void decide(false)}
            >
              {t('nx.req.reject')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {data.status === 'approved' && grants.can('purchasing.manage_rfq') ? (
        <div className="mt-6">
          <Button
            variant="primary"
            onClick={() =>
              router.push(`/buying/quotes/new?requisition=${data.id}`)
            }
          >
            {t('nx.req.askSuppliers')}
          </Button>
        </div>
      ) : null}

      {data.status === 'ordered' ? (
        <p className="mt-6 text-body text-muted">{t('nx.req.alreadyOrdered')}</p>
      ) : null}
    </>
  );
}

export default function RequisitionPage({
  params,
}: {
  params: Promise<{ requisitionID: string }>;
}) {
  const { requisitionID } = use(params);
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <RequisitionScreen requisitionID={requisitionID} />
    </RequirePermission>
  );
}
