'use client';

// F1's "approvers can delegate while on leave", given the screen it never had.
//
// `GET /approval-delegations` and `POST /approval-delegations` were live and
// uncalled. The engine reads the table on every decision — a step naming a
// person is satisfied by whoever is covering for them today — so cover was
// enforced by software that nothing could configure.
//
// # Reading and arranging are different authorities
//
// The list is `approval.view`; arranging cover is `approval.decide`. That is
// the route's own split and it is the right one: handing your approvals on
// requires being able to give them. Somebody who may see who is covering for
// whom gets the table without the form, and the server refuses regardless.
//
// # Absent `from_user_id` means the caller
//
// The route defaults it, because the common case is somebody arranging their
// own cover before going away. The form defaults the picker to the signed-in
// person for the same reason, and lets it be changed — an owner arranging
// cover for somebody who has already left for the airport is the other real
// case.
//
// # A closed arrangement is not deleted
//
// There is no route to end one, and the list is already limited to what is
// still relevant: the query keeps anything ending within the last thirty days.
// So the table shows what is live, what is coming, and what has just finished,
// and `live` is computed by the server rather than by comparing dates here —
// the screen and the engine cannot then disagree about whether cover is on.

import { UserRoundCheck } from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useGrants, useSession } from '@/lib/auth/session';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import type { Person } from '@/lib/people/roles';
import type { Cover } from '@/lib/workflow/rules';
import { useUrlFlag } from '@/lib/url-state';

export function CoverTab() {
  const t = useT();
  const scope = useCompanyScope();
  const grants = useGrants();
  const mayArrange = grants.can('approval.decide');

  const [arranging, setArranging] = useUrlFlag('newCover');

  const { data, isLoading, error, refetch } = useApiList<Cover>(
    scope ? '/approval-delegations' : null,
    scope ?? undefined,
  );
  const people = useApiList<Person>(scope ? '/people' : null, scope ?? undefined);

  const rows = data?.data ?? [];

  const columns: Column<Cover>[] = [
    {
      key: 'from',
      header: t('nx.aprc.colAway'),
      primary: true,
      cell: (c) => (
        <span className="flex items-center gap-2">
          {c.from === '' ? t('nx.aprc.someoneRemoved') : c.from}
          {c.live ? <Badge tone="positive">{t('nx.aprc.live')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'to',
      header: t('nx.aprc.colDeciding'),
      cell: (c) => (c.to === '' ? t('nx.aprc.someoneRemoved') : c.to),
    },
    {
      key: 'when',
      header: t('nx.aprc.colBetween'),
      cell: (c) => (
        <span className="num text-muted">
          {t('nx.aprc.dateRange', { from: c.starts_on, to: c.ends_on })}
        </span>
      ),
    },
    {
      key: 'note',
      header: t('nx.aprc.colNote'),
      secondary: true,
      cell: (c) =>
        c.note !== undefined && c.note !== '' ? (
          <span className="text-muted">{c.note}</span>
        ) : (
          <span className="text-subtle">—</span>
        ),
    },
  ];

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_23rem]">
      <div className="min-w-0">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <p className="text-caption text-muted">{t('nx.aprc.coverLead')}</p>
          {mayArrange ? (
            <Button variant="primary" size="sm" onClick={() => setArranging(true)}>
              {t('nx.aprc.newCover')}
            </Button>
          ) : null}
        </div>

        {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
        {isLoading && !data ? <TableSkeleton columns={4} /> : null}

        {!isLoading && !error && rows.length === 0 ? (
          <EmptyState
            icon={UserRoundCheck}
            title={t('nx.aprc.coverEmptyTitle')}
            description={t('nx.aprc.coverEmptyDesc')}
          />
        ) : null}

        {rows.length > 0 ? (
          <DataTable
            caption={t('nx.aprc.coverCaption')}
            columns={columns}
            rows={rows}
            rowKey={(c) => c.id}
          />
        ) : null}
      </div>

      {mayArrange && arranging ? (
        <CoverForm
          people={people.data?.data ?? []}
          onDone={() => {
            void refetch();
            setArranging(false);
          }}
          onCancel={() => setArranging(false)}
        />
      ) : null}
    </div>
  );
}

function CoverForm({
  people,
  onDone,
  onCancel,
}: {
  people: Person[];
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();
  const { identity } = useSession();

  // Defaulted to the signed-in person, because arranging your own cover is the
  // common case. Left changeable, because an owner arranging it for somebody
  // who has already gone is the other one.
  const [from, setFrom] = useState(identity?.userId ?? '');
  const [to, setTo] = useState('');
  const [starts, setStarts] = useState('');
  const [ends, setEnds] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  // Cover that hands approvals back to the same person is not cover. Caught
  // here because the message can name the mistake, rather than as a database
  // constraint violation the caller has to interpret.
  const toSelf = to !== '' && to === from;

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});
    try {
      await api.post(`/approval-delegations?company_id=${scope.company_id}`, {
        from_user_id: from,
        to_user_id: to,
        starts_on: starts,
        ends_on: ends,
        note,
      });
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <Panel title={t('nx.aprc.newCover')} description={t('nx.aprc.coverFormLead')}>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <FormError message={error} fields={fieldErrors} />

        <Field
          name="from_user_id"
          label={t('nx.aprc.fAway')}
          hint={t('nx.aprc.fAwayHint')}
          error={fieldErrors.from_user_id}
          required
        >
          <Select value={from} onChange={(e) => setFrom(e.target.value)}>
            {people.map((p) => (
              <option key={p.id} value={p.id}>
                {p.full_name}
              </option>
            ))}
          </Select>
        </Field>

        <Field
          name="to_user_id"
          label={t('nx.aprc.fDeciding')}
          error={toSelf ? t('nx.aprc.errSelfCover') : fieldErrors.to_user_id}
          required
        >
          <Select value={to} onChange={(e) => setTo(e.target.value)}>
            <option value="">{t('nx.aprc.choosePerson')}</option>
            {people.map((p) => (
              <option key={p.id} value={p.id}>
                {p.full_name}
              </option>
            ))}
          </Select>
        </Field>

        <div className="grid gap-3 sm:grid-cols-2">
          <Field
            name="starts_on"
            label={t('nx.aprc.fStarts')}
            error={fieldErrors.starts_on}
            required
          >
            <Input
              type="date"
              className="num"
              value={starts}
              onChange={(e) => setStarts(e.target.value)}
            />
          </Field>
          <Field
            name="ends_on"
            label={t('nx.aprc.fEnds')}
            error={fieldErrors.ends_on}
            required
          >
            <Input
              type="date"
              className="num"
              value={ends}
              onChange={(e) => setEnds(e.target.value)}
            />
          </Field>
        </div>

        <Field name="note" label={t('nx.aprc.fNote')} hint={t('nx.aprc.fNoteHint')}>
          <Textarea rows={2} value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>

        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <Button
            type="submit"
            variant="primary"
            busy={busy}
            disabled={toSelf || to === '' || starts === '' || ends === ''}
          >
            {t('nx.aprc.saveCover')}
          </Button>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.aprc.cancel')}
          </Button>
        </div>
      </form>
    </Panel>
  );
}
