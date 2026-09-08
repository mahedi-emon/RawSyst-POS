'use client';

// Trade between companies in the same group (blueprint F4).
//
// # Why this is not optional
//
// F4 says inter-company transactions are "tracked and eliminated in
// consolidation". The elimination half was built: `ProfitAndLoss` excludes any
// journal entry present in `intercompany_entry`, and reports how many entries
// and how much it took out. The tracking half had no way in — `POST
// /groups/intercompany` was live and uncalled — so nothing was ever marked,
// nothing was ever eliminated, and the consolidated statement counted a sale
// from one company in the group to another as group revenue.
//
// That is not a cosmetic gap. A group that invoices itself reports turnover it
// does not have, and the figure looks entirely plausible.
//
// # Marking and unmarking are one control
//
// `unmark: true` on the same route, for the same reason `clear` exists on a
// feature flag: one row, two states, and undoing is not a different kind of
// act. Both are audited by the service.
//
// # The entry is picked, not typed
//
// `intercompany_entry` references the LEDGER entry, and the adjustment
// register returns it as `journal_entry_id` beside the journal number a person
// actually reads. So the form offers the register — a route that already
// exists and is company-scoped — rather than asking somebody to copy a uuid
// out of one screen and into another.
//
// It lists this company's journals only. Each company in a group keeps its own
// books, which is the point of F4, and there is no cross-company register to
// offer; marking an entry is done from the company that raised it.

import { useState } from 'react';

import { Can } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge } from '@/components/ui/panel';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';

interface Intercompany {
  entry_id: string;
  entry_no: string;
  entry_date: string;
  memo?: string;
  company_id: string;
  counterparty_id: string;
  kind: string;
  note?: string;
  amount: string;
  marked_by?: string;
}

interface GroupMember {
  company_id: string;
  name: string;
}

/** One row of the adjustment register, as the picker needs it. */
interface JournalRow {
  id: string;
  journal_no: string;
  journal_entry_id: string;
  entry_date: string;
  reason: string;
  total: string;
}

const KINDS = ['sale', 'purchase', 'loan', 'transfer'] as const;

export function IntercompanyPanel({
  groupId,
  members,
  companyId,
  from,
  to,
  money,
}: {
  groupId: string;
  members: GroupMember[];
  companyId: string;
  from: string;
  to: string;
  money: (v: string) => string;
}) {
  const t = useT();

  // The same window as the statement above it, so what is listed here is
  // exactly what was left out of the figures there.
  const { data, isLoading, error, refetch } = useApiList<Intercompany>(
    groupId ? `/groups/${groupId}/intercompany` : null,
    { company_id: companyId, from, to },
  );

  const [marking, setMarking] = useState(false);

  // Only fetched while the form is open: most visits to this screen are to
  // read the statement, and the register is a second query nobody asked for.
  const journals = useApiList<JournalRow>(
    marking ? '/accounting/journals' : null,
    { company_id: companyId, limit: 50 },
  );

  const [entryId, setEntryId] = useState('');
  const [counterparty, setCounterparty] = useState('');
  const [kind, setKind] = useState<string>('sale');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string> | null>(null);

  const rows = data?.data ?? [];
  const nameOf = (id: string) =>
    members.find((m) => m.company_id === id)?.name ?? id.slice(0, 8);

  async function mark() {
    setBusy(true);
    setActionError(null);
    setFields(null);
    try {
      await api.post(`/groups/intercompany?company_id=${companyId}`, {
        entry_id: entryId,
        counterparty_id: counterparty,
        kind,
        note,
      });
      setMarking(false);
      setEntryId('');
      setNote('');
      void refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFields(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function unmark(id: string) {
    setBusy(true);
    setActionError(null);
    try {
      await api.post(`/groups/intercompany?company_id=${companyId}`, {
        entry_id: id,
        unmark: true,
      });
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Intercompany>[] = [
    {
      key: 'entry',
      header: t('nx.grp.icColEntry'),
      primary: true,
      cell: (r) => (
        <span className="flex flex-col gap-0.5">
          <span className="num">{r.entry_no}</span>
          {r.memo ? <span className="text-caption text-muted">{r.memo}</span> : null}
        </span>
      ),
    },
    {
      key: 'date',
      header: t('nx.grp.icColDate'),
      secondary: true,
      width: 'w-32',
      cell: (r) => (
        <time dateTime={r.entry_date} className="num text-muted">
          {r.entry_date}
        </time>
      ),
    },
    {
      key: 'with',
      header: t('nx.grp.icColCounterparty'),
      cell: (r) => nameOf(r.counterparty_id),
    },
    {
      key: 'kind',
      header: t('nx.grp.icColKind'),
      width: 'w-32',
      // A word, not a colour: which direction the trade went decides how it
      // eliminates, and a badge tone alone says nothing to somebody reading
      // the statement beside it.
      cell: (r) => (
        <Badge>
          {t(`nx.grp.icKind${r.kind.charAt(0).toUpperCase()}${r.kind.slice(1)}` as 'nx.grp.icKindSale')}
        </Badge>
      ),
    },
    {
      key: 'amount',
      header: t('nx.grp.icColAmount'),
      numeric: true,
      width: 'w-36',
      cell: (r) => <span className="num">{money(r.amount)}</span>,
    },
    {
      key: 'actions',
      header: '',
      width: 'w-28',
      cell: (r) => (
        <Can permission="group.manage">
          <span className="flex justify-end">
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => void unmark(r.entry_id)}
            >
              {t('nx.grp.icUnmark')}
            </Button>
          </span>
        </Can>
      ),
    },
  ];

  return (
    <section>
      <div className="mb-1 flex flex-wrap items-start justify-between gap-2">
        <h2 className="text-card-title font-semibold text-fg">
          {t('nx.grp.icTitle')}
        </h2>
        {!marking ? (
          <Can permission="group.manage">
            <Button size="sm" onClick={() => setMarking(true)}>
              {t('nx.grp.icMark')}
            </Button>
          </Can>
        ) : null}
      </div>
      <p className="mb-3 max-w-prose text-caption text-muted">
        {t('nx.grp.icDesc')}
      </p>

      {error ? <FormError message={messageFor(error, t)} /> : null}

      {marking ? (
        <div className="mb-4 rounded-md border border-line bg-surface p-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              name="entry_id"
              label={t('nx.grp.icEntryId')}
              hint={t('nx.grp.icEntryIdHint')}
              error={fields?.entry_id}
            >
              <Select value={entryId} onChange={(e) => setEntryId(e.target.value)}>
                <option value="">{t('nx.grp.icChoose')}</option>
                {(journals.data?.data ?? []).map((j) => (
                  <option key={j.id} value={j.journal_entry_id}>
                    {j.journal_no} · {j.entry_date} · {money(j.total)}
                    {j.reason ? ` — ${j.reason}` : ''}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              name="counterparty_id"
              label={t('nx.grp.icCounterparty')}
              hint={t('nx.grp.icCounterpartyHint')}
              error={fields?.counterparty_id}
            >
              <Select
                value={counterparty}
                onChange={(e) => setCounterparty(e.target.value)}
              >
                <option value="">{t('nx.grp.icChoose')}</option>
                {members.map((m) => (
                  <option key={m.company_id} value={m.company_id}>
                    {m.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field name="kind" label={t('nx.grp.icKind')} error={fields?.kind}>
              <Select value={kind} onChange={(e) => setKind(e.target.value)}>
                {KINDS.map((k) => (
                  <option key={k} value={k}>
                    {t(`nx.grp.icKind${k.charAt(0).toUpperCase()}${k.slice(1)}` as 'nx.grp.icKindSale')}
                  </option>
                ))}
              </Select>
            </Field>
            <Field name="note" label={t('nx.grp.icNote')}>
              <Textarea rows={2} value={note} onChange={(e) => setNote(e.target.value)} />
            </Field>
          </div>

          {actionError ? <FormError message={actionError} /> : null}

          <div className="mt-4 flex flex-wrap gap-2">
            <Button
              variant="primary"
              disabled={busy || entryId === '' || counterparty === ''}
              onClick={() => void mark()}
            >
              {t('nx.grp.icSave')}
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => setMarking(false)}>
              {t('nx.grp.icCancel')}
            </Button>
          </div>
        </div>
      ) : null}

      {isLoading && rows.length === 0 ? <TableSkeleton columns={6} rows={3} /> : null}

      {!isLoading && rows.length === 0 ? (
        <p className="text-body text-muted">{t('nx.grp.icEmpty')}</p>
      ) : null}

      {rows.length > 0 ? (
        <>
          {actionError && !marking ? <FormError message={actionError} /> : null}
          <DataTable
            caption={t('nx.grp.icTitle')}
            columns={columns}
            rows={rows}
            rowKey={(r) => r.entry_id}
          />
        </>
      ) : null}
    </section>
  );
}
