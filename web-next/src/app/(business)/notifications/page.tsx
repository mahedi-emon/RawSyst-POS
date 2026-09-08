'use client';

// D3's notification centre: what the product has told this person.
//
// # Six routes and nothing calling them
//
// List, unread count, mark one read, mark all read, read preferences and write
// them were all live and reachable by nothing. A notification the product
// raises and never shows is a notification it did not raise.
//
// # Read is a fact about this person, not about the notice
//
// The same announcement goes to several people and each reads it separately, so
// marking one read here changes nothing for anybody else. That is why the
// unread count is per person and why "mark all read" is offered — a list nobody
// can clear stops being looked at.
//
// # Severity carries a word as well as a colour
//
// info, warning and critical. A critical notice on a shared till is read by
// whoever is on shift, and roughly one man in twelve cannot separate the red
// from the amber, so the badge says which it is.
//
// # A channel that is off is not a channel that does not exist
//
// Preferences carry in-app, email, SMS and push per kind. Whether the platform
// actually delivers on a channel is its own question — this screen records the
// person's preference and does not claim delivery.

import { BellOff } from 'lucide-react';
import { Suspense, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Can } from '@/components/auth/guard';
import { Checkbox, Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { Tabs } from '@/components/ui/tabs';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

interface Notification {
  id: string;
  kind: string;
  severity: string;
  title: string;
  body?: string;
  subject?: string;
  subject_id?: string;
  is_read: boolean;
  read_at?: string;
  created_at: string;
}

interface Preference {
  kind: string;
  in_app: boolean;
  email: boolean;
  sms: boolean;
  push: boolean;
}

const SEVERITY_TONE: Record<string, 'info' | 'caution' | 'critical'> = {
  info: 'info',
  warning: 'caution',
  critical: 'critical',
};

type TabId = 'all' | 'settings';

function NotificationsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  const [tab, setTab] = useUrlState('tab');
  const active: TabId = tab === 'settings' ? 'settings' : 'all';

  // Company-scoped: notifyScope resolves a company from the request, so a
  // notification belongs to a person IN a business rather than to the person
  // across all of them. Without it the route answers 400.
  const { data, isLoading, error, refetch } = useApiList<Notification>(
    scope ? '/notifications' : null,
    { company_id: scope?.company_id },
  );
  const prefs = useApiList<Preference>(
    scope && active === 'settings' ? '/notifications/preferences' : null,
    { company_id: scope?.company_id },
  );

  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  // Sending a notice. The one write on this screen that reaches somebody
  // else's inbox, which is why it is the one carrying a permission -- and why
  // it is a deliberate panel rather than a box always sitting open.
  const [composing, setComposing] = useState(false);
  const [subject, setSubject] = useState('');
  const [message, setMessage] = useState('');
  const [severity, setSeverity] = useState('info');
  const [sent, setSent] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string> | null>(null);

  const rows = data?.data ?? [];
  const unread = rows.filter((n) => !n.is_read).length;

  async function markRead(n: Notification) {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(
        `/notifications/${n.id}/read?company_id=${scope.company_id}`,
        {},
      );
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function markAll() {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post(`/notifications/read?company_id=${scope.company_id}`, {});
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function setChannel(p: Preference, channel: keyof Preference, on: boolean) {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    try {
      // The route takes the whole preference for a kind, so the other three
      // channels are sent as they stand rather than left to default.
      await api.put(`/notifications/preferences?company_id=${scope.company_id}`, {
        kind: p.kind,
        in_app: channel === 'in_app' ? on : p.in_app,
        email: channel === 'email' ? on : p.email,
        sms: channel === 'sms' ? on : p.sms,
        push: channel === 'push' ? on : p.push,
      });
      void prefs.refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Notification>[] = [
    {
      key: 'title',
      header: t('nx.ntf.what'),
      primary: true,
      cell: (n) => (
        <span className="flex flex-col">
          <span className={n.is_read ? 'text-muted' : 'font-medium text-fg'}>
            {n.title}
          </span>
          {n.body ? <span className="text-caption text-muted">{n.body}</span> : null}
        </span>
      ),
    },
    {
      key: 'severity',
      header: t('nx.ntf.severity'),
      width: 'w-28',
      cell: (n) => (
        <Badge tone={SEVERITY_TONE[n.severity] ?? 'info'}>
          {t(`nx.ntf.sev.${n.severity}` as 'nx.ntf.sev.info')}
        </Badge>
      ),
    },
    {
      key: 'when',
      header: t('nx.ntf.when'),
      width: 'w-32',
      cell: (n) => <time dateTime={n.created_at}>{n.created_at.slice(0, 10)}</time>,
    },
    {
      key: 'read',
      header: t('nx.ntf.readHeader'),
      width: 'w-28',
      cell: (n) =>
        n.is_read ? (
          <span className="text-muted">{t('nx.ntf.read')}</span>
        ) : (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void markRead(n)}>
            {t('nx.ntf.markRead')}
          </Button>
        ),
    },
  ];

  const prefColumns: Column<Preference>[] = [
    {
      key: 'kind',
      header: t('nx.ntf.kind'),
      primary: true,
      cell: (p) => <span className="num">{p.kind}</span>,
    },
    ...(['in_app', 'email', 'sms', 'push'] as const).map((channel) => ({
      key: channel,
      header: t(`nx.ntf.channel.${channel}` as 'nx.ntf.channel.in_app'),
      width: 'w-28',
      cell: (p: Preference) => (
        <Checkbox
          checked={p[channel]}
          disabled={busy}
          onChange={(e) => void setChannel(p, channel, e.target.checked)}
          label={t(`nx.ntf.channel.${channel}` as 'nx.ntf.channel.in_app')}
          className="[&>label]:sr-only"
        />
      ),
    })),
  ];

  async function announce() {
    if (!scope) return;
    setBusy(true);
    setActionError(null);
    setFieldErrors(null);
    setSent(null);
    try {
      await api.post(`/notifications/announce?company_id=${scope.company_id}`, {
        title: subject,
        body: message,
        severity,
      });
      setComposing(false);
      setSubject('');
      setMessage('');
      setSeverity('info');
      setSent(t('nx.ntf.announceSent'));
      // The sender is in this company too, so their own inbox now has it.
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader
        title={t('nx.ntf.title')}
        description={t('nx.ntf.subtitle')}
        actions={
          <span className="flex flex-wrap items-center gap-2">
            {active === 'all' && unread > 0 ? (
              <Button variant="secondary" busy={busy} onClick={() => void markAll()}>
                {t('nx.ntf.markAll', { count: String(unread) })}
              </Button>
            ) : null}
            {!composing ? (
              <Can permission="notification.manage">
                <Button
                  onClick={() => {
                    setSent(null);
                    setActionError(null);
                    setComposing(true);
                  }}
                >
                  {t('nx.ntf.announce')}
                </Button>
              </Can>
            ) : null}
          </span>
        }
      />

      <Tabs<TabId>
        label={t('nx.ntf.tabsLabel')}
        items={[
          { id: 'all', label: t('nx.ntf.tabAll') },
          { id: 'settings', label: t('nx.ntf.tabSettings') },
        ]}
        value={active}
        onChange={setTab}
      />

      <FormError message={actionError} className="my-4" />

      {sent ? (
        <p className="my-4 text-body text-positive-fg">{sent}</p>
      ) : null}

      {composing ? (
        <Panel
          title={t('nx.ntf.announceTitle')}
          description={t('nx.ntf.announceHint')}
          className="mb-4"
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="title"
              label={t('nx.ntf.announceSubject')}
              error={fieldErrors?.title}
            >
              <Input
                value={subject}
                onChange={(e) => setSubject(e.target.value)}
                required
              />
            </Field>
            <Field name="severity" label={t('nx.ntf.announceSeverity')}>
              <Select
                value={severity}
                onChange={(e) => setSeverity(e.target.value)}
              >
                <option value="info">{t('nx.ntf.sevInfo')}</option>
                <option value="warning">{t('nx.ntf.sevWarning')}</option>
                <option value="critical">{t('nx.ntf.sevCritical')}</option>
              </Select>
            </Field>
            <div className="sm:col-span-2">
              <Field name="body" label={t('nx.ntf.announceBody')}>
                <Textarea
                  rows={3}
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                />
              </Field>
            </div>
          </div>

          <div className="mt-4 flex flex-wrap gap-2">
            <Button
              variant="primary"
              disabled={busy || subject.trim() === ''}
              onClick={() => void announce()}
            >
              {t('nx.ntf.announceSend')}
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => setComposing(false)}>
              {t('nx.ntf.announceCancel')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {active === 'settings' ? (
        <Panel description={t('nx.ntf.prefsHint')} flush>
          {prefs.isLoading && !prefs.data ? (
            <TableSkeleton columns={5} />
          ) : (prefs.data?.data ?? []).length === 0 ? (
            <div className="p-6">
              <EmptyState
                icon={BellOff}
                title={t('nx.ntf.noPrefsTitle')}
                description={t('nx.ntf.noPrefsDesc')}
              />
            </div>
          ) : (
            <DataTable<Preference>
              rows={prefs.data?.data ?? []}
              columns={prefColumns}
              rowKey={(p) => p.kind}
              caption={t('nx.ntf.prefsCaption')}
            />
          )}
        </Panel>
      ) : (
        <>
          {isLoading && !data ? <TableSkeleton columns={4} /> : null}

          {!isLoading && rows.length === 0 ? (
            <EmptyState
              icon={BellOff}
              title={t('nx.ntf.emptyTitle')}
              description={t('nx.ntf.emptyDesc')}
            />
          ) : null}

          {rows.length > 0 ? (
            <DataTable<Notification>
              rows={rows}
              columns={columns}
              rowKey={(n) => n.id}
              caption={t('nx.ntf.caption')}
            />
          ) : null}
        </>
      )}
    </>
  );
}

export default function NotificationsPage() {
  return (
    <Suspense fallback={<div className="h-64" aria-busy="true" />}>
      <NotificationsScreen />
    </Suspense>
  );
}
