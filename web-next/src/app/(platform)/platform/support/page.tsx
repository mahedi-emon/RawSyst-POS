'use client';

// The tickets businesses have raised.
//
// # Urgent first, and the platform does the ordering
//
// The route returns them in priority order, then by what moved most recently.
// Re-sorting here would put a second opinion about urgency in front of the
// operator, and the one that matters is the one the ticket itself carries.
//
// # The author of a reply comes from the session
//
// Never from the message body. A reply that could name its own author is one
// anybody could sign as support, and the route is explicit about it -- so this
// screen sends only the words.
//
// # Replying is a state change, and it is on the button
//
// `POST /platform/support/{id}/reply` moves the ticket to `waiting_on_customer`
// unless the body names another status, and `resolved` also stamps the closing
// time. That is not a side effect to discover afterwards: there are two
// buttons, they say which one closes the ticket, and the hint above them says
// what each does.
//
// # The queue carries no messages
//
// `Queue` selects the ticket columns and nothing else -- the thread is read one
// ticket at a time, and the platform side has no route for that. The reply
// response IS the updated ticket, messages included, so the thread appears
// after replying and is honest about why it was not there before.

import { LifeBuoy, MessageSquare } from 'lucide-react';
import { useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel, type Tone } from '@/components/ui/panel';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useT, type Key } from '@/lib/i18n/locale';
import { cn } from '@/lib/utils';

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
  tenant?: string;
  /** Filled by the reply response only; the queue does not carry it. */
  messages?: Message[];
}

/**
 * The four priorities the table allows, and their tone.
 *
 * `support_ticket_priority_valid` is `low | normal | high | urgent`, so there
 * is no fifth case to guess at -- anything else is the backend having changed
 * under this screen, and neutral is the honest answer to that.
 */
const PRIORITY: Record<string, { key: Key; tone: Tone }> = {
  urgent: { key: 'nx.plat.prUrgent', tone: 'critical' },
  high: { key: 'nx.plat.prHigh', tone: 'caution' },
  normal: { key: 'nx.plat.prNormal', tone: 'info' },
  low: { key: 'nx.plat.prLow', tone: 'neutral' },
};

/** The five the status constraint allows. */
const STATUS: Record<string, { key: Key; tone: Tone }> = {
  open: { key: 'nx.plat.stOpen', tone: 'info' },
  waiting_on_support: { key: 'nx.plat.stWaitingSupport', tone: 'caution' },
  waiting_on_customer: { key: 'nx.plat.stWaitingCustomer', tone: 'neutral' },
  resolved: { key: 'nx.plat.stResolved', tone: 'positive' },
  closed: { key: 'nx.plat.stClosed', tone: 'neutral' },
};

/** What the business is asking about. Five kinds, from the same constraint. */
const KIND: Record<string, Key> = {
  question: 'nx.plat.kQuestion',
  bug: 'nx.plat.kBug',
  feature_request: 'nx.plat.kFeature',
  billing: 'nx.plat.kBilling',
  outage: 'nx.plat.kOutage',
};

/** A ticket that is done is one the open queue should stop showing. */
function isSettled(status: string): boolean {
  return status === 'resolved' || status === 'closed';
}

function SupportScreen() {
  const t = useT();
  const [includeClosed, setIncludeClosed] = useState(false);

  const { data, isLoading, error, refetch } = useApiList<Ticket>(
    '/platform/support',
    // The route reads the literal string, so the flag is only ever sent when
    // it is on -- an absent parameter and `false` mean the same thing to it.
    includeClosed ? { include_closed: 'true' } : undefined,
    { refetchInterval: 60_000, staleTime: 0 },
  );

  const [openId, setOpenId] = useState<string | null>(null);
  const [reply, setReply] = useState('');
  const [busy, setBusy] = useState<'reply' | 'resolve' | null>(null);
  const [sendError, setSendError] = useState<string | null>(null);
  // The reply response is the updated ticket. Keeping it lets the row show the
  // new status and the thread without a refetch that would reorder the queue
  // under the operator's cursor.
  const [answered, setAnswered] = useState<Record<string, Ticket>>({});
  const [dropped, setDropped] = useState<Set<string>>(new Set());

  const fetched = data?.data ?? [];
  const tickets = fetched
    .map((ticket) => answered[ticket.id] ?? ticket)
    .filter((ticket) => !dropped.has(ticket.id));

  async function send(id: string, status?: 'resolved') {
    const body = reply.trim();
    if (!body) return;
    setBusy(status ? 'resolve' : 'reply');
    setSendError(null);
    try {
      const updated = await api.post<Ticket>(
        `/platform/support/${id}/reply`,
        status ? { body, status } : { body },
      );
      setAnswered((current) => ({ ...current, [id]: updated }));
      // Resolved leaves the open queue, which is what the operator just asked
      // for. Hidden here rather than by refetching, so the rest of the list
      // stays where they left it.
      if (isSettled(updated.status) && !includeClosed) {
        setDropped((current) => new Set(current).add(id));
        setOpenId(null);
      }
      setReply('');
    } catch (e) {
      setSendError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <PageHeader
        title={t('nx.plat.supportTitle')}
        description={t('nx.plat.supportSubtitle')}
      />

      <div className="mb-4">
        <Checkbox
          label={t('nx.plat.showResolved')}
          checked={includeClosed}
          onChange={(e) => {
            setIncludeClosed(e.target.checked);
            // The hidden rows are back in the answer now; a stale drop list
            // would keep them out of a list that was asked to include them.
            setDropped(new Set());
          }}
        />
      </div>

      {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}

      {isLoading && tickets.length === 0 ? (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      ) : null}

      {!isLoading && !error && tickets.length === 0 ? (
        <EmptyState
          icon={LifeBuoy}
          title={t('nx.plat.supportEmptyTitle')}
          description={t('nx.plat.supportEmptyDesc')}
        />
      ) : null}

      {tickets.length > 0 ? (
        <ul className="flex flex-col gap-3">
          {tickets.map((ticket) => {
            const open = openId === ticket.id;
            const priority = PRIORITY[ticket.priority];
            const status = STATUS[ticket.status];
            const kind = KIND[ticket.kind];
            const thread = ticket.messages ?? [];

            return (
              <li key={ticket.id}>
                <Panel
                  title={
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="num text-label text-muted">
                        {ticket.ticket_no}
                      </span>
                      {ticket.subject}
                      {priority ? (
                        <Badge tone={priority.tone}>{t(priority.key)}</Badge>
                      ) : null}
                      {status ? (
                        <Badge tone={status.tone}>{t(status.key)}</Badge>
                      ) : null}
                    </span>
                  }
                  description={[
                    kind ? t(kind) : ticket.kind,
                    ticket.tenant,
                    ticket.raised_by,
                    ticket.created_at.slice(0, 10),
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                  actions={
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => {
                        setOpenId(open ? null : ticket.id);
                        setReply('');
                        setSendError(null);
                      }}
                      aria-expanded={open}
                    >
                      <MessageSquare aria-hidden="true" />
                      {open ? t('nx.common.close') : t('nx.plat.reply')}
                    </Button>
                  }
                >
                  <p className={cn('text-body text-fg', !open && 'line-clamp-3')}>
                    {ticket.body}
                  </p>

                  {thread.length > 0 ? (
                    <div className="mt-4 border-t border-line pt-3">
                      <p className="text-label text-muted">
                        {t('nx.plat.threadSoFar')}
                      </p>
                      <p className="mt-1 text-caption text-subtle">
                        {t('nx.plat.threadAfterReply')}
                      </p>
                      <ul className="mt-3 flex flex-col gap-3">
                        {thread.map((message) => (
                          <li
                            key={message.id}
                            className={cn(
                              'rounded-sm border border-line px-3 py-2',
                              message.from_platform
                                ? 'bg-primary-subtle/40'
                                : 'bg-surface-sunken',
                            )}
                          >
                            <p className="text-caption text-muted">
                              {message.author ||
                                t(
                                  message.from_platform
                                    ? 'nx.plat.fromSupport'
                                    : 'nx.plat.fromBusiness',
                                )}
                              {' · '}
                              <time dateTime={message.created_at} className="num">
                                {message.created_at.slice(0, 16).replace('T', ' ')}
                              </time>
                            </p>
                            <p className="mt-1 text-body text-fg">{message.body}</p>
                          </li>
                        ))}
                      </ul>
                    </div>
                  ) : null}

                  {open ? (
                    <div className="mt-4 border-t border-line pt-4">
                      <FormError message={sendError} className="mb-3" />
                      <Field
                        label={t('nx.plat.reply')}
                        hint={t('nx.plat.replyAuthor')}
                      >
                        <Textarea
                          value={reply}
                          onChange={(e) => setReply(e.target.value)}
                          placeholder={t('nx.plat.replyPlaceholder')}
                          autoFocus
                        />
                      </Field>

                      <p className="mt-2 text-caption text-muted">
                        {t('nx.plat.replyEffect')}
                      </p>

                      <div className="mt-3 flex flex-wrap gap-2">
                        <Button
                          variant="primary"
                          busy={busy === 'reply'}
                          disabled={reply.trim() === '' || busy !== null}
                          onClick={() => void send(ticket.id)}
                        >
                          {t('nx.plat.sendReply')}
                        </Button>
                        <Button
                          variant="secondary"
                          busy={busy === 'resolve'}
                          disabled={reply.trim() === '' || busy !== null}
                          onClick={() => void send(ticket.id, 'resolved')}
                        >
                          {t('nx.plat.replyResolve')}
                        </Button>
                      </div>
                    </div>
                  ) : null}
                </Panel>
              </li>
            );
          })}
        </ul>
      ) : null}
    </>
  );
}

export default function SupportPage() {
  return (
    <RequireWorkspace workspace="platform">
      <SupportScreen />
    </RequireWorkspace>
  );
}
