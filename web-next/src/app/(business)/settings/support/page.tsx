'use client';

// Asking RawSyst for help, and reading what came back.
//
// # Reading and raising are one permission, on purpose
//
// The route says it: "reading your own tickets and raising one are the same
// act: there is nothing to see that you did not write." A business only ever
// sees its own tickets, so there is no narrower read to grant and no wider one
// to withhold. `support.raise` is the whole of it.
//
// # The status moves itself
//
// Replying changes the status, because "the status follows the conversation
// rather than waiting for somebody to move it, and a hand-maintained status is
// wrong within a week." So this screen has no status dropdown. It shows what
// the conversation has made true, and the one status a business genuinely
// decides — closed — has its own button.
//
// # Urgent is a claim about the shop, not about the mood
//
// An outage means tills are down and customers are being turned away. Marking
// a question urgent gets it triaged like one, so the priorities carry what they
// actually mean rather than four adjectives of increasing volume.

import { LifeBuoy } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

interface Message {
  id: string;
  body: string;
  from_platform: boolean;
  author?: string;
  created_at: string;
}

interface Ticket {
  id: string;
  ticket_no: string;
  subject: string;
  body: string;
  kind: string;
  priority: string;
  status: string;
  raised_by?: string;
  created_at: string;
  updated_at: string;
  resolved_at?: string;
  messages?: Message[];
}

const KINDS = ['question', 'bug', 'feature_request', 'billing', 'outage'] as const;
const PRIORITIES = ['low', 'normal', 'high', 'urgent'] as const;

const STATUS_TONE: Record<string, 'caution' | 'info' | 'positive' | 'neutral'> = {
  open: 'caution',
  waiting_on_support: 'info',
  waiting_on_customer: 'caution',
  resolved: 'positive',
  closed: 'neutral',
};

const PRIORITY_TONE: Record<string, 'neutral' | 'info' | 'caution' | 'critical'> = {
  low: 'neutral',
  normal: 'info',
  high: 'caution',
  urgent: 'critical',
};

function SupportScreen() {
  const t = useT();
  const { data, isLoading, error, refetch } = useApiList<Ticket>('/support/tickets');

  const [openId, setOpenId] = useState<string | null>(null);
  const [raising, setRaising] = useState(false);
  const [subject, setSubject] = useState('');
  const [body, setBody] = useState('');
  const [kind, setKind] = useState('question');
  const [priority, setPriority] = useState('normal');
  const [reply, setReply] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  // The thread is only on the detail route; the list carries no messages.
  const thread = useApi<Ticket>(openId ? `/support/tickets/${openId}` : null);

  const rows = data?.data ?? [];

  async function raise() {
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    try {
      await api.post('/support/tickets', { subject, body, kind, priority });
      setRaising(false);
      setSubject('');
      setBody('');
      setKind('question');
      setPriority('normal');
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function send() {
    if (!openId || reply.trim() === '') return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(`/support/tickets/${openId}/reply`, { body: reply.trim() });
      setReply('');
      void thread.refetch();
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function close() {
    if (!openId) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(`/support/tickets/${openId}/close`, {});
      void thread.refetch();
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Ticket>[] = [
    {
      key: 'subject',
      header: t('nx.sup.subject'),
      primary: true,
      cell: (x) => (
        <span className="flex flex-col">
          <span>{x.subject}</span>
          <span className="text-caption text-muted">
            <span className="num">{x.ticket_no}</span> ·{' '}
            {t(`nx.sup.kind.${x.kind}` as 'nx.sup.kind.question')}
          </span>
        </span>
      ),
    },
    {
      key: 'priority',
      header: t('nx.sup.priority'),
      width: 'w-28',
      cell: (x) => (
        <Badge tone={PRIORITY_TONE[x.priority] ?? 'neutral'}>
          {t(`nx.sup.priority.${x.priority}` as 'nx.sup.priority.normal')}
        </Badge>
      ),
    },
    {
      key: 'raised',
      header: t('nx.sup.raised'),
      secondary: true,
      width: 'w-28',
      cell: (x) => <time dateTime={x.created_at}>{x.created_at.slice(0, 10)}</time>,
    },
    {
      key: 'status',
      header: t('nx.sup.status'),
      width: 'w-44',
      cell: (x) => (
        <Badge tone={STATUS_TONE[x.status] ?? 'neutral'}>
          {t(`nx.sup.state.${x.status}` as 'nx.sup.state.open')}
        </Badge>
      ),
    },
    {
      key: 'open',
      header: t('nx.sup.openHeader'),
      width: 'w-24',
      cell: (x) => (
        <Button size="sm" variant="ghost" onClick={() => setOpenId(x.id)}>
          {t('nx.sup.open')}
        </Button>
      ),
    },
  ];

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  const open = thread.data;
  const settled = open?.status === 'closed' || open?.status === 'resolved';

  return (
    <>
      <PageHeader
        title={t('nx.sup.title')}
        description={t('nx.sup.subtitle')}
        actions={
          !raising ? (
            <Button onClick={() => setRaising(true)}>{t('nx.sup.raise')}</Button>
          ) : null
        }
      />

      <FormError message={actionError} fields={fieldErrors} className="mb-4" />

      {raising ? (
        <Panel title={t('nx.sup.raiseTitle')} className="mb-4">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void raise();
            }}
          >
            <div className="grid gap-4 sm:grid-cols-2">
              <Field name="subject" label={t('nx.sup.subject')}>
                <Input
                  value={subject}
                  onChange={(e) => setSubject(e.target.value)}
                  required
                />
              </Field>
              <Field name="kind" label={t('nx.sup.kindLabel')}>
                <Select value={kind} onChange={(e) => setKind(e.target.value)}>
                  {KINDS.map((k) => (
                    <option key={k} value={k}>
                      {t(`nx.sup.kind.${k}` as 'nx.sup.kind.question')}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field
                name="priority"
                label={t('nx.sup.priority')}
                hint={t('nx.sup.priorityHint')}
              >
                <Select value={priority} onChange={(e) => setPriority(e.target.value)}>
                  {PRIORITIES.map((p) => (
                    <option key={p} value={p}>
                      {t(`nx.sup.priority.${p}` as 'nx.sup.priority.normal')}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>
            <div className="mt-4">
              <Field
                name="body"
                label={t('nx.sup.body')}
                hint={t('nx.sup.bodyHint')}
              >
                <Textarea
                  rows={4}
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  required
                />
              </Field>
            </div>
            <div className="mt-6 flex flex-wrap gap-2">
              <Button type="submit" busy={busy} busyLabel={t('nx.sup.sending')}>
                {t('nx.sup.send')}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setRaising(false)}>
                {t('nx.sup.cancel')}
              </Button>
            </div>
          </form>
        </Panel>
      ) : null}

      {open ? (
        <Panel
          title={open.subject}
          description={`${open.ticket_no} · ${t(
            `nx.sup.state.${open.status}` as 'nx.sup.state.open',
          )}`}
          className="mb-4"
          actions={
            <Button variant="ghost" onClick={() => setOpenId(null)}>
              {t('nx.sup.closePanel')}
            </Button>
          }
        >
          <ol className="space-y-3">
            {/* The opening message is the ticket body; the thread is what came
                after it. Showing only `messages` would drop the question. */}
            <li className="border-s-2 border-line ps-3">
              <p className="text-caption text-muted">
                {open.raised_by ?? t('nx.sup.us')} · {open.created_at.slice(0, 10)}
              </p>
              <p className="text-body text-fg">{open.body}</p>
            </li>
            {(open.messages ?? []).map((m) => (
              <li
                key={m.id}
                className={`border-s-2 ps-3 ${
                  m.from_platform ? 'border-primary' : 'border-line'
                }`}
              >
                <p className="text-caption text-muted">
                  {m.from_platform
                    ? (m.author ?? t('nx.sup.rawsyst'))
                    : (m.author ?? t('nx.sup.us'))}{' '}
                  · {m.created_at.slice(0, 10)}
                </p>
                <p className="text-body text-fg">{m.body}</p>
              </li>
            ))}
          </ol>

          {!settled ? (
            <div className="mt-4 border-t border-line pt-4">
              <Field name="reply" label={t('nx.sup.reply')}>
                <Textarea
                  rows={3}
                  value={reply}
                  onChange={(e) => setReply(e.target.value)}
                />
              </Field>
              <div className="mt-4 flex flex-wrap gap-2">
                <Button
                  busy={busy}
                  busyLabel={t('nx.sup.sending')}
                  disabled={reply.trim() === ''}
                  onClick={() => void send()}
                >
                  {t('nx.sup.sendReply')}
                </Button>
                <Button variant="secondary" busy={busy} onClick={() => void close()}>
                  {t('nx.sup.markClosed')}
                </Button>
              </div>
            </div>
          ) : (
            <p className="mt-4 border-t border-line pt-4 text-body text-muted">
              {t('nx.sup.settled')}
            </p>
          )}
        </Panel>
      ) : null}

      {isLoading && !data ? <TableSkeleton columns={5} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={LifeBuoy}
          title={t('nx.sup.emptyTitle')}
          description={t('nx.sup.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<Ticket>
          rows={rows}
          columns={columns}
          rowKey={(x) => x.id}
          caption={t('nx.sup.caption')}
        />
      ) : null}
    </>
  );
}

export default function SupportPage() {
  return (
    <RequirePermission anyOf={['support.raise']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <SupportScreen />
      </Suspense>
    </RequirePermission>
  );
}
