'use client';

// Your own second factor, and the devices you are signed in on.
//
// # Signing in with MFA already worked. Turning it on did not.
//
// The login screen has always handled the MFA challenge, so an account with a
// second factor could sign in — but nothing in the product could enrol one,
// show recovery codes, or turn it off again. Five routes were live and
// unreachable.
//
// # The secret and the recovery codes are shown once each
//
// `POST /auth/mfa/begin` answers a secret and an otpauth URI; the codes come
// back from `complete` and from `recovery-codes`, and neither is stored
// anywhere readable. So each is shown once, in a step of its own, with the
// sentence saying it will not be shown again — and never logged or put in a URL.
//
// # This screen is about the person reading it, and nobody else
//
// Every route here resolves the caller from their own token. There is no user
// parameter, so an administrator cannot enrol somebody else's phone or read
// their sessions from here — resetting another person's access is the people
// screen's job, and it works by forcing a password change rather than by
// reaching into their second factor.
//
// # Ending a session ends it now
//
// A session in the list is a signed-in browser or till somewhere. Revoking one
// is how somebody responds to a laptop left at a customer's counter, so the
// current session is marked and cannot be revoked by accident from here —
// signing out is what ends that one.

import { Check, Copy, ShieldCheck } from 'lucide-react';
import { Suspense, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

interface MFAStatus {
  enabled: boolean;
  enrolled_at?: string;
  recovery_remaining: number;
}

interface Enrolment {
  secret: string;
  uri: string;
}

interface ActiveSession {
  id: string;
  device_label?: string;
  ip?: string;
  user_agent?: string;
  created_at: string;
  last_seen_at?: string;
  expires_at: string;
  current: boolean;
}

/** A one-time secret, shown once and copyable. */
function OnceOnly({ label, value }: { label: string; value: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  return (
    <div>
      <p className="text-caption font-medium uppercase tracking-wide text-muted">
        {label}
      </p>
      <div className="mt-1 flex flex-wrap items-center gap-2">
        {/* A generated credential is a sequence of characters, not prose: LTR
            and monospaced in every language. */}
        <code
          dir="ltr"
          className="rounded border border-line bg-surface-sunken px-2 py-1 font-mono text-body text-fg"
        >
          {value}
        </code>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => {
            navigator.clipboard
              .writeText(value)
              .then(() => {
                setCopied(true);
                setTimeout(() => setCopied(false), 2000);
              })
              // A denied clipboard is not worth an error panel: the value is on
              // screen and can be selected. Claiming a copy that did not happen
              // would be worse.
              .catch(() => setCopied(false));
          }}
        >
          {copied ? <Check className="size-4" aria-hidden /> : <Copy className="size-4" aria-hidden />}
          {copied ? t('nx.sec.copied') : t('nx.sec.copy')}
        </Button>
      </div>
    </div>
  );
}

function SecurityScreen() {
  const t = useT();
  const status = useApi<{ mfa: MFAStatus }>('/auth/mfa');
  const sessions = useApiList<ActiveSession>('/auth/sessions');

  const [enrolment, setEnrolment] = useState<Enrolment | null>(null);
  const [codes, setCodes] = useState<string[] | null>(null);
  const [code, setCode] = useState('');
  const [confirming, setConfirming] = useState<'enable' | 'disable' | 'regenerate' | null>(
    null,
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  const mfa = status.data?.mfa;

  function clear() {
    setCode('');
    setError(null);
    setFieldErrors(null);
  }

  async function begin() {
    setBusy(true);
    clear();
    try {
      const out = await api.post<{ enrolment: Enrolment }>('/auth/mfa/begin', {});
      setEnrolment(out.enrolment);
      setConfirming('enable');
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function act(path: string, then: (codes?: string[]) => void) {
    setBusy(true);
    setError(null);
    setFieldErrors(null);
    try {
      const out = await api.post<{ recovery_codes?: string[] }>(path, {
        code: code.trim(),
      });
      then(out?.recovery_codes);
      setCode('');
      void status.refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function revoke(session: ActiveSession) {
    setBusy(true);
    setError(null);
    try {
      await api.delete(`/auth/sessions/${session.id}`);
      void sessions.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const sessionColumns: Column<ActiveSession>[] = [
    {
      key: 'device',
      header: t('nx.sec.device'),
      primary: true,
      cell: (s) => (
        <span className="flex flex-col">
          <span className="flex items-center gap-2">
            {s.device_label ?? t('nx.sec.unnamedDevice')}
            {s.current ? <Badge tone="primary">{t('nx.sec.thisOne')}</Badge> : null}
          </span>
          {s.user_agent ? (
            <span className="text-caption text-muted">{s.user_agent}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'ip',
      header: t('nx.sec.ip'),
      secondary: true,
      width: 'w-40',
      cell: (s) =>
        s.ip ? (
          <span className="num" dir="ltr">
            {s.ip}
          </span>
        ) : (
          <span className="text-muted">—</span>
        ),
    },
    {
      key: 'seen',
      header: t('nx.sec.lastSeen'),
      width: 'w-32',
      cell: (s) =>
        s.last_seen_at ? (
          <time dateTime={s.last_seen_at}>{s.last_seen_at.slice(0, 10)}</time>
        ) : (
          <time dateTime={s.created_at}>{s.created_at.slice(0, 10)}</time>
        ),
    },
    {
      key: 'end',
      header: t('nx.sec.endHeader'),
      width: 'w-28',
      // The current session is not revocable from here: ending it would sign
      // somebody out mid-action with no explanation. Signing out does that.
      cell: (s) =>
        s.current ? (
          <span className="text-muted">—</span>
        ) : (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void revoke(s)}>
            {t('nx.sec.end')}
          </Button>
        ),
    },
  ];

  if (status.error) {
    return <ErrorState error={status.error} onRetry={() => void status.refetch()} />;
  }

  return (
    <>
      <PageHeader title={t('nx.sec.title')} description={t('nx.sec.subtitle')} />

      <FormError message={error} fields={fieldErrors} className="mb-4" />

      {/* The codes, once. Shown above everything else because losing them is
          the failure this screen exists to prevent. */}
      {codes ? (
        <Panel title={t('nx.sec.codesTitle')} className="mb-4">
          <p className="text-body text-fg">{t('nx.sec.codesOnce')}</p>
          <ul className="mt-3 grid gap-2 sm:grid-cols-2">
            {codes.map((c) => (
              <li key={c}>
                <code
                  dir="ltr"
                  className="block rounded border border-line bg-surface-sunken px-2 py-1 font-mono text-body text-fg"
                >
                  {c}
                </code>
              </li>
            ))}
          </ul>
          <div className="mt-4">
            <Button variant="secondary" onClick={() => setCodes(null)}>
              {t('nx.sec.savedThem')}
            </Button>
          </div>
        </Panel>
      ) : null}

      <Panel title={t('nx.sec.mfaTitle')} className="mb-4">
        {status.isLoading && !mfa ? (
          <div className="h-16" aria-busy="true" />
        ) : mfa?.enabled ? (
          <>
            <p className="flex items-center gap-2 text-body text-fg">
              <ShieldCheck className="size-5 text-positive" aria-hidden />
              {t('nx.sec.mfaOn', { date: (mfa.enrolled_at ?? '').slice(0, 10) })}
            </p>
            <p className="mt-2 text-body text-muted">
              {t('nx.sec.codesLeft', { count: String(mfa.recovery_remaining) })}
            </p>

            {confirming === 'disable' || confirming === 'regenerate' ? (
              <div className="mt-4 border-t border-line pt-4">
                <Field
                  name="code"
                  label={t('nx.sec.confirmCode')}
                  hint={t('nx.sec.confirmCodeHint')}
                >
                  <Input
                    dir="ltr"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                  />
                </Field>
                <div className="mt-4 flex flex-wrap gap-2">
                  <Button
                    variant={confirming === 'disable' ? 'destructive' : 'primary'}
                    busy={busy}
                    disabled={code.trim() === ''}
                    onClick={() =>
                      void act(
                        confirming === 'disable'
                          ? '/auth/mfa/disable'
                          : '/auth/mfa/recovery-codes',
                        (fresh) => {
                          setConfirming(null);
                          if (fresh) setCodes(fresh);
                        },
                      )
                    }
                  >
                    {confirming === 'disable'
                      ? t('nx.sec.confirmDisable')
                      : t('nx.sec.confirmRegenerate')}
                  </Button>
                  <Button
                    variant="ghost"
                    onClick={() => {
                      setConfirming(null);
                      clear();
                    }}
                  >
                    {t('nx.sec.cancel')}
                  </Button>
                </div>
              </div>
            ) : (
              <div className="mt-4 flex flex-wrap gap-2">
                <Button variant="secondary" onClick={() => setConfirming('regenerate')}>
                  {t('nx.sec.newCodes')}
                </Button>
                <Button variant="ghost" onClick={() => setConfirming('disable')}>
                  {t('nx.sec.turnOff')}
                </Button>
              </div>
            )}
          </>
        ) : enrolment ? (
          <>
            <p className="text-body text-fg">{t('nx.sec.scanBody')}</p>
            <div className="mt-4 space-y-4">
              <OnceOnly label={t('nx.sec.secret')} value={enrolment.secret} />
              <OnceOnly label={t('nx.sec.uri')} value={enrolment.uri} />
            </div>
            <div className="mt-4 border-t border-line pt-4">
              <Field
                name="code"
                label={t('nx.sec.confirmCode')}
                hint={t('nx.sec.enrolHint')}
              >
                <Input
                  dir="ltr"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                />
              </Field>
              <div className="mt-4 flex flex-wrap gap-2">
                <Button
                  busy={busy}
                  disabled={code.trim() === ''}
                  onClick={() =>
                    void act('/auth/mfa/complete', (fresh) => {
                      setEnrolment(null);
                      setConfirming(null);
                      if (fresh) setCodes(fresh);
                    })
                  }
                >
                  {t('nx.sec.turnOn')}
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => {
                    setEnrolment(null);
                    setConfirming(null);
                    clear();
                  }}
                >
                  {t('nx.sec.cancel')}
                </Button>
              </div>
            </div>
          </>
        ) : (
          <>
            <p className="text-body text-muted">{t('nx.sec.mfaOff')}</p>
            <div className="mt-4">
              <Button busy={busy} onClick={() => void begin()}>
                {t('nx.sec.setUp')}
              </Button>
            </div>
          </>
        )}
      </Panel>

      <Panel title={t('nx.sec.sessionsTitle')} description={t('nx.sec.sessionsHint')} flush>
        {sessions.isLoading && !sessions.data ? (
          <TableSkeleton columns={4} />
        ) : (sessions.data?.data ?? []).length === 0 ? (
          <div className="p-6">
            <EmptyState
              icon={ShieldCheck}
              title={t('nx.sec.noSessionsTitle')}
              description={t('nx.sec.noSessionsDesc')}
            />
          </div>
        ) : (
          <DataTable<ActiveSession>
            rows={sessions.data?.data ?? []}
            columns={sessionColumns}
            rowKey={(s) => s.id}
            caption={t('nx.sec.sessionsCaption')}
          />
        )}
      </Panel>
    </>
  );
}

export default function SecurityPage() {
  return (
    <Suspense fallback={<div className="h-64" aria-busy="true" />}>
      <SecurityScreen />
    </Suspense>
  );
}
