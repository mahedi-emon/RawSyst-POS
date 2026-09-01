// Support tickets (blueprint H10).
//
// # The conversation is the screen
//
// A ticket is a thread. Opening one shows what was asked and every answer since,
// in order, with a box at the bottom — because that is what a person expects
// from anything that is a conversation, and a table of "messages" with a modal
// is not.
//
// # Nobody has to set a status
//
// Replying moves it. A status somebody maintains by hand is a status that is
// wrong within a week, and the whole value of the field is a queue that shows
// what is actually waiting on the platform.

import { useCallback, useState } from 'react';

import {
  closeTicket,
  listTickets,
  raiseTicket,
  readTicket,
  replyToTicket,
  type Ticket,
} from '../api/admin';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { Field, FormActions, FormError, SelectInput, TextInput } from '../ui/Form';
import { shortDate } from '../ui/format';

const KINDS = ['question', 'bug', 'feature_request', 'billing', 'outage'] as const;
const PRIORITIES = ['low', 'normal', 'high', 'urgent'] as const;

export function SupportPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [open, setOpen] = useState<string | null>(null);
  const [raising, setRaising] = useState(false);
  const [includeClosed, setIncludeClosed] = useState(false);

  const load = useCallback(
    () => listTickets(client, companyId, includeClosed),
    [client, companyId, includeClosed],
  );
  const { remote, reload } = useRemote(load);

  if (open) {
    return (
      <TicketThread
        companyId={companyId}
        ticketId={open}
        onBack={() => {
          setOpen(null);
          reload();
        }}
      />
    );
  }

  return (
    <>
      {raising && (
        <TicketForm
          companyId={companyId}
          onCancel={() => setRaising(false)}
          onRaised={(id) => {
            setRaising(false);
            reload();
            setOpen(id);
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('adm.support')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('adm.yourTickets')}</h2>
            <p className="ds-caption">{t('adm.supportHint')}</p>
          </div>
          <div className="adm__rowactions">
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => setIncludeClosed(!includeClosed)}
            >
              {t(includeClosed ? 'adm.showOpenTickets' : 'adm.showAllTickets')}
            </button>
            {!raising && (
              <button
                className="ds-btn ds-btn--primary"
                onClick={() => setRaising(true)}
              >
                {t('adm.askForHelp')}
              </button>
            )}
          </div>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: Ticket[] }) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('adm.noTicketsTitle')}
                  body={t('adm.noTicketsBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('adm.ticket')}</th>
                      <th scope="col">{t('adm.kind')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">{t('adm.lastActivity')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((tk) => (
                      <tr key={tk.id}>
                        <td>
                          <span className="detail__strong">{tk.subject}</span>
                          <span className="ds-caption">
                            {tk.ticket_no}
                            {tk.raised_by ? ` · ${tk.raised_by}` : ''}
                          </span>
                        </td>
                        <td>
                          {t(`adm.ticketKind.${tk.kind}` as Key)}
                          <span className="ds-caption">
                            {t(`adm.priority.${tk.priority}` as Key)}
                          </span>
                        </td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${ticketBadge(tk.status)}`}
                          >
                            {t(`adm.ticketStatus.${tk.status}` as Key)}
                          </span>
                        </td>
                        <td>{shortDate(tk.updated_at, locale)}</td>
                        <td>
                          <button
                            className="ds-btn ds-btn--quiet"
                            onClick={() => setOpen(tk.id)}
                          >
                            {t('adm.openThread')}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          }
        </RemoteBody>
      </section>
    </>
  );
}

function ticketBadge(status: string): string {
  switch (status) {
    case 'resolved':
    case 'closed':
      return 'neutral';
    case 'waiting_on_customer':
      return 'warning';
    case 'waiting_on_support':
      return 'info';
    default:
      return 'info';
  }
}

function TicketThread({
  companyId,
  ticketId,
  onBack,
}: {
  companyId: string;
  ticketId: string;
  onBack: () => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(
    () => readTicket(client, companyId, ticketId),
    [client, companyId, ticketId],
  );
  const { remote, reload } = useRemote(load);

  const [reply, setReply] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function send(tk: Ticket) {
    setBusy(true);
    setFailure(null);
    try {
      await replyToTicket(client, companyId, tk.id, reply);
      setReply('');
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(tk: Ticket) => (
        <section className="ds-panel">
          <div className="ds-panel__head">
            <div>
              <button className="ds-btn ds-btn--quiet" onClick={onBack}>
                {t('action.back')}
              </button>
              <h2 className="ds-h3">{tk.subject}</h2>
              <p className="ds-caption">
                {tk.ticket_no} · {t(`adm.ticketStatus.${tk.status}` as Key)} ·{' '}
                {t(`adm.priority.${tk.priority}` as Key)}
              </p>
            </div>
            {tk.status !== 'closed' && (
              <button
                className="ds-btn ds-btn--quiet"
                disabled={busy}
                onClick={() =>
                  void closeTicket(client, companyId, tk.id).then(reload)
                }
              >
                {t('adm.closeTicket')}
              </button>
            )}
          </div>

          <div className="ds-panel__body">
            <FormError message={failure} />

            <ol className="adm__thread">
              <li className="adm__message">
                <span className="adm__author">
                  {tk.raised_by ?? t('adm.you')}
                </span>
                <p>{tk.body}</p>
                <span className="ds-caption">
                  {shortDate(tk.created_at, locale)}
                </span>
              </li>
              {(tk.messages ?? []).map((m) => (
                <li
                  key={m.id}
                  className={`adm__message${m.from_platform ? ' adm__message--support' : ''}`}
                >
                  {/* A tenant has to be able to tell an answer from their own
                      question, which is why the side is on the message. */}
                  <span className="adm__author">
                    {m.author ?? (m.from_platform ? t('adm.support') : t('adm.you'))}
                  </span>
                  <p>{m.body}</p>
                  <span className="ds-caption">
                    {shortDate(m.created_at, locale)}
                  </span>
                </li>
              ))}
            </ol>

            {tk.status !== 'closed' && (
              <div className="adm__replybox">
                <TextInput
                  id="tk-reply"
                  value={reply}
                  onChange={setReply}
                  placeholder={t('adm.writeAReply')}
                />
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy || reply.trim() === ''}
                  onClick={() => void send(tk)}
                >
                  {t('adm.send')}
                </button>
              </div>
            )}
          </div>
        </section>
      )}
    </RemoteBody>
  );
}

function TicketForm({
  companyId,
  onCancel,
  onRaised,
}: {
  companyId: string;
  onCancel: () => void;
  onRaised: (id: string) => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [subject, setSubject] = useState('');
  const [body, setBody] = useState('');
  const [kind, setKind] = useState<string>('question');
  const [priority, setPriority] = useState<string>('normal');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      const tk = await raiseTicket(client, companyId, {
        subject,
        body,
        kind,
        priority,
      });
      onRaised(tk.id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel adm__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('adm.askForHelp')}</h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field label={t('adm.subject')} htmlFor="tk-subject" required>
            <TextInput id="tk-subject" value={subject} onChange={setSubject} />
          </Field>
          <Field label={t('adm.kind')} htmlFor="tk-kind" required>
            <SelectInput
              id="tk-kind"
              value={kind}
              onChange={setKind}
              options={KINDS.map((k) => ({ id: k }))}
              label={(k) => t(`adm.ticketKind.${k.id}` as Key)}
            />
          </Field>
          <Field label={t('adm.howUrgent')} htmlFor="tk-priority" required>
            <SelectInput
              id="tk-priority"
              value={priority}
              onChange={setPriority}
              options={PRIORITIES.map((p) => ({ id: p }))}
              label={(p) => t(`adm.priority.${p.id}` as Key)}
            />
          </Field>
        </div>

        <Field
          label={t('adm.whatHappened')}
          hint={t('adm.whatHappenedHint')}
          htmlFor="tk-body"
          required
        >
          <textarea
            id="tk-body"
            className="field__input"
            rows={5}
            value={body}
            onChange={(e) => setBody(e.target.value)}
          />
        </Field>

        <FormActions
          submitLabel={t('adm.askForHelp')}
          busy={busy}
          disabled={subject.trim() === '' || body.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
