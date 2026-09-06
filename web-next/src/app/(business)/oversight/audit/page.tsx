'use client';

// The audit trail.
//
// # The filter offers what is in the trail, not what we imagined
//
// `/audit` returns the verbs actually present in this tenant's log alongside
// the rows. The filter is built from that list, so it never offers a verb the
// trail cannot contain and never omits one a new module started writing. A
// hardcoded list goes stale the day a module ships, and a filter that quietly
// stopped offering a verb would hide the entries somebody came here to find.
//
// # The verb is printed as recorded
//
// Not prettified, not translated. This is evidence: an auditor comparing the
// screen against an export needs the same string in both, and a friendlier
// rendering would put a word in the record that nobody wrote.
//
// # A change is shown as the fields that moved
//
// The server hands over `before` and `after` raw, deliberately, so the reader
// sees the record rather than a rendering of it. What the screen adds is which
// fields differ, so nobody has to diff two blobs by eye — and both sides of
// each field are printed exactly as they were recorded.

import { ScrollText } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { Badge, PageHeader, Panel, type Tone } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT, type Key } from '@/lib/i18n/locale';
import {
  changeKind,
  changedFields,
  type AuditRecord,
  type Change,
} from '@/lib/oversight/records';
import { useUrlState } from '@/lib/url-state';

const CHANGE_TONE: Record<Change, Tone> = {
  created: 'positive',
  removed: 'critical',
  changed: 'info',
  happened: 'neutral',
};

const CHANGE_LABEL: Record<Change, Key> = {
  created: 'nx.aud.created',
  removed: 'nx.aud.removed',
  changed: 'nx.aud.changed',
  happened: 'nx.aud.happened',
};

interface Trail {
  data: AuditRecord[];
  /** The verbs present in this tenant's trail. The filter is built from it. */
  actions: string[];
}

/** A recorded value, printed as it was recorded. */
function Value({ value }: { value: unknown }) {
  const t = useT();
  if (value === undefined) {
    return <span className="text-muted">{t('nx.aud.absent')}</span>;
  }
  return (
    <span className="num break-all">
      {typeof value === 'string' ? value : JSON.stringify(value)}
    </span>
  );
}

function AuditScreen() {
  const t = useT();
  const scope = useCompanyScope();

  const [action, setAction] = useUrlState('action', '');
  const [entity, setEntity] = useUrlState('entity', '');
  const [from, setFrom] = useUrlState('from', '');
  const [to, setTo] = useUrlState('to', '');
  const [openRow, setOpenRow] = useState<string | null>(null);

  const trail = useApi<Trail>(
    scope ? '/audit' : null,
    scope
      ? {
          ...scope,
          // Only sent when set: an empty `action` would narrow the trail to
          // entries whose verb is the empty string, which is none of them.
          ...(action ? { action } : {}),
          ...(entity ? { entity_type: entity } : {}),
          ...(from ? { from } : {}),
          ...(to ? { to } : {}),
          limit: 200,
        }
      : undefined,
  );

  const rows = trail.data?.data ?? [];
  const actions = trail.data?.actions ?? [];
  // Built from what came back rather than from a list held here, for the same
  // reason as the verbs.
  const entities = [...new Set(rows.map((r) => r.entity_type))].sort();

  const columns: Column<AuditRecord>[] = [
    {
      key: 'when',
      header: t('nx.aud.colWhen'),
      primary: true,
      width: 'w-52',
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="num">{r.occurred_at.slice(0, 19).replace('T', ' ')}</span>
          <span className="text-caption text-muted">
            {/* Denormalised in the log on purpose, so the trail survives the
                person being deleted rather than becoming a list of blanks. */}
            {r.actor || t('nx.aud.noActor')}
          </span>
        </span>
      ),
    },
    {
      key: 'action',
      header: t('nx.aud.colAction'),
      cell: (r) => (
        <span className="flex flex-col gap-1">
          <span className="num">{r.action}</span>
          <span className="flex items-center gap-2">
            <Badge tone={CHANGE_TONE[changeKind(r)]}>
              {t(CHANGE_LABEL[changeKind(r)])}
            </Badge>
            <span className="text-caption text-muted">{r.entity_type}</span>
          </span>
        </span>
      ),
    },
    {
      key: 'what',
      header: t('nx.aud.colWhat'),
      secondary: true,
      cell: (r) => {
        const changes = changedFields(r);
        if (changes.length === 0) {
          return <span className="text-muted">—</span>;
        }
        return (
          <span className="text-muted">
            {changes.map((c) => c.field).join(', ')}
          </span>
        );
      },
    },
    {
      key: 'where',
      header: t('nx.aud.colWhere'),
      secondary: true,
      width: 'w-40',
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="num text-muted">{r.ip || '—'}</span>
          {r.device ? <span className="text-caption text-muted">{r.device}</span> : null}
        </span>
      ),
    },
    {
      key: 'open',
      header: t('nx.aud.colEvidence'),
      width: 'w-28',
      cell: (r) => {
        const id = `${r.occurred_at}-${r.action}-${r.entity_id ?? ''}`;
        if (r.before == null && r.after == null) {
          return <span className="text-caption text-muted">—</span>;
        }
        return (
          <Button variant="ghost" onClick={() => setOpenRow(openRow === id ? null : id)}>
            {t(openRow === id ? 'nx.aud.hide' : 'nx.aud.show')}
          </Button>
        );
      },
    },
  ];

  const open = rows.find(
    (r) => `${r.occurred_at}-${r.action}-${r.entity_id ?? ''}` === openRow,
  );

  return (
    <>
      <PageHeader title={t('nx.aud.title')} description={t('nx.aud.subtitle')} />

      <Panel className="mb-6" title={t('nx.aud.narrowTitle')}>
        <div className="flex flex-wrap items-end gap-3">
          <Field name="action" label={t('nx.aud.colAction')}>
            <Select value={action} onChange={(e) => setAction(e.target.value)}>
              <option value="">{t('nx.aud.anyAction')}</option>
              {actions.map((a) => (
                // Printed as recorded, for the same reason as the column.
                <option key={a} value={a}>
                  {a}
                </option>
              ))}
            </Select>
          </Field>
          <Field name="entity" label={t('nx.aud.colEntity')}>
            <Select value={entity} onChange={(e) => setEntity(e.target.value)}>
              <option value="">{t('nx.aud.anyEntity')}</option>
              {entities.map((e) => (
                <option key={e} value={e}>
                  {e}
                </option>
              ))}
            </Select>
          </Field>
          <Field name="from" label={t('nx.aud.from')}>
            <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
          </Field>
          <Field name="to" label={t('nx.aud.to')} hint={t('nx.aud.toHint')}>
            <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
          </Field>
          {action || entity || from || to ? (
            <Button
              onClick={() => {
                setAction('');
                setEntity('');
                setFrom('');
                setTo('');
              }}
            >
              {t('nx.aud.clear')}
            </Button>
          ) : null}
        </div>
      </Panel>

      {open ? (
        <Panel
          className="mb-6"
          title={t('nx.aud.evidenceTitle', { action: open.action })}
          description={t('nx.aud.evidenceDesc')}
          actions={
            <Button variant="ghost" onClick={() => setOpenRow(null)}>
              {t('nx.aud.hide')}
            </Button>
          }
        >
          {changedFields(open).length > 0 ? (
            <table className="w-full text-body">
              <caption className="sr-only">{t('nx.aud.evidenceDesc')}</caption>
              <thead>
                <tr className="border-b border-line text-label text-muted">
                  <th scope="col" className="py-2 pe-4 text-start font-medium">
                    {t('nx.aud.colField')}
                  </th>
                  <th scope="col" className="py-2 pe-4 text-start font-medium">
                    {t('nx.aud.wasValue')}
                  </th>
                  <th scope="col" className="py-2 text-start font-medium">
                    {t('nx.aud.nowValue')}
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {changedFields(open).map((c) => (
                  <tr key={c.field}>
                    <th scope="row" className="py-2 pe-4 text-start font-medium align-top">
                      {c.field}
                    </th>
                    <td className="py-2 pe-4 align-top">
                      <Value value={c.before} />
                    </td>
                    <td className="py-2 align-top">
                      <Value value={c.after} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            // Only one side was recorded, so there is nothing to compare. The
            // record itself is still the evidence, and it is shown whole.
            <div className="overflow-x-auto">
              <pre className="num text-caption text-muted">
                {JSON.stringify(open.after ?? open.before, null, 2)}
              </pre>
            </div>
          )}
        </Panel>
      ) : null}

      {trail.error ? (
        <ErrorState error={trail.error} onRetry={() => void trail.refetch()} />
      ) : null}
      {trail.isLoading && !trail.data ? <TableSkeleton columns={5} /> : null}

      {!trail.isLoading && !trail.error && rows.length === 0 ? (
        <EmptyState
          icon={ScrollText}
          title={t('nx.aud.emptyTitle')}
          description={t('nx.aud.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <>
          <DataTable
            caption={t('nx.aud.title')}
            columns={columns}
            rows={rows}
            rowKey={(r) => `${r.occurred_at}-${r.action}-${r.entity_id ?? ''}`}
          />
          {/* The trail is asked for 200 at a time. Saying so is the difference
              between "nothing else happened" and "nothing else was fetched". */}
          {rows.length >= 200 ? (
            <p className="mt-3 max-w-prose text-caption text-muted">
              {t('nx.aud.capped', { n: '200' })}
            </p>
          ) : null}
        </>
      ) : null}
    </>
  );
}

export default function AuditPage() {
  return (
    <RequirePermission anyOf={['accounting.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <AuditScreen />
      </Suspense>
    </RequirePermission>
  );
}
