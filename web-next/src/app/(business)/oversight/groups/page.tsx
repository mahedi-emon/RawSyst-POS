'use client';

// Group companies, and the one refusal on this screen that is not about
// permission.
//
// # 402 is not 403, and saying so is the whole point
//
// This tenant's plan does not sell group consolidation, so `GET /groups`
// answers 402 `feature_not_in_plan` — with the caller holding `group.view`
// perfectly well. The backend draws that distinction deliberately: what is
// missing is commercial, and the two have different remedies. A screen that
// showed "you may not do that" would send somebody to their manager to ask for
// a permission they already have, and the manager could not grant what was
// never sold.
//
// So the plan refusal gets its own state, in the server's own words, naming
// what would have to change. It is not an error, and it is not styled as one.
//
// # Ownership is a string
//
// `ownership_pct` is decimal on the wire and stays decimal here. A group where
// three subsidiaries are 33.33% each is exactly where a float starts producing
// 99.99000000000001.

import { Building2 } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Checkbox, Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi, useApiList } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';

import { IntercompanyPanel } from './intercompany';
import { useUrlState } from '@/lib/url-state';

interface Member {
  company_id: string;
  name: string;
  base_currency: string;
  /** Decimal on the wire. Never a number here. */
  ownership_pct: string;
  is_parent: boolean;
}

/** A company this tenant holds, as the member picker names one. */
interface Company {
  id: string;
  legal_name: string;
  trade_name?: string;
}

interface Group {
  id: string;
  name: string;
  name_ar?: string;
  presentation_currency: string;
  members: Member[];
}

interface StatementLine {
  account: string;
  label: string;
  amount: string;
}

interface Statement {
  from?: string;
  to?: string;
  currency?: string;
  lines?: StatementLine[];
}

function GroupsScreen() {
  const t = useT();
  const scope = useCompanyScope();
  // Named apart from the `currency` state below, which is the box on the
  // new-group form rather than the company's own.
  const { currency: baseCurrency, market } = useCompany();
  const grants = useGrants();
  const mayManage = grants.can('group.manage');

  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [naming, setNaming] = useState(false);
  const [name, setName] = useState('');
  const [currency, setCurrency] = useState('');
  const [openID, setOpenID] = useUrlState('group', '');

  const groups = useApiList<Group>(scope ? '/groups' : null, scope ?? undefined);

  // The companies that could join a group. Read only when somebody may manage
  // one: a reader has no picker to fill.
  const companies = useApiList<Company>(mayManage ? '/companies' : null);

  // Adding a member. Held here rather than in a sub-component because the form
  // is three boxes and the group it belongs to is already open on this screen.
  const [memberID, setMemberID] = useState('');
  const [ownership, setOwnership] = useState('');
  const [isParent, setIsParent] = useState(false);

  // The plan refusal, told apart from every other failure. `isPlanLimited`
  // reads the 402 rather than the message, so a reworded refusal still lands
  // in the right state.
  const refusal = groups.error;
  const notSold = refusal instanceof ApiError && refusal.isPlanLimited;

  const rows = groups.data?.data ?? [];
  const open = rows.find((g) => g.id === openID) ?? null;

  const statement = useApi<Statement>(
    scope && open ? `/groups/${open.id}/statement` : null,
    scope ?? undefined,
  );

  // Year to date, which is the window somebody reviewing eliminations is
  // working in. The statement above takes its own default; this is stated
  // explicitly because the list has to say WHICH entries were left out, and
  // "the ones in the period the server chose" is not an answer.
  const today = new Date();
  const icFrom = `${today.getUTCFullYear()}-01-01`;
  const icTo = today.toISOString().slice(0, 10);

  // Joining a company to a group, and taking it out again.
  //
  // `group.manage` could create a group and nothing else: the two member
  // routes had no caller, so every group the product could make was empty, the
  // consolidated statement had nothing to consolidate, and the inter-company
  // elimination built in F4 had no members whose trade it could eliminate.
  async function addMember() {
    if (!scope || !open || !memberID) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(`/groups/${open.id}/members?company_id=${scope.company_id}`, {
        company_id: memberID,
        // Decimal on the wire and a string all the way here. Three
        // subsidiaries at 33.33 each is where a float starts producing
        // 99.99000000000001.
        ownership_pct: ownership.trim(),
        is_parent: isParent,
      });
      setMemberID('');
      setOwnership('');
      setIsParent(false);
      await groups.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function removeMember(companyID: string) {
    if (!scope || !open) return;
    setBusy(true);
    setError(null);
    try {
      await api.delete(
        `/groups/${open.id}/members/${companyID}?company_id=${scope.company_id}`,
      );
      await groups.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function create() {
    if (!scope || !name.trim()) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(`/groups?company_id=${scope.company_id}`, {
        name: name.trim(),
        presentation_currency: currency.trim().toUpperCase(),
      });
      setName('');
      setCurrency('');
      setNaming(false);
      await groups.refetch();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (notSold) {
    return (
      <>
        <PageHeader title={t('nx.grp.title')} description={t('nx.grp.subtitle')} />
        <Panel title={t('nx.grp.notSoldTitle')}>
          {/* The server's own sentence. It says what is missing; rewording it
              would turn a commercial fact into our paraphrase of one. */}
          <p className="max-w-prose text-body text-fg">
            {(refusal as ApiError).message}
          </p>
          <p className="mt-3 max-w-prose text-body text-muted">
            {t('nx.grp.notSoldDesc')}
          </p>
        </Panel>
      </>
    );
  }

  const columns: Column<Group>[] = [
    {
      key: 'name',
      header: t('nx.grp.colGroup'),
      primary: true,
      cell: (g) => (
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{g.name}</span>
          <span className="text-caption text-muted">
            {t('nx.grp.reportedIn', { currency: g.presentation_currency })}
          </span>
        </span>
      ),
    },
    {
      key: 'members',
      header: t('nx.grp.colMembers'),
      width: 'w-32',
      cell: (g) => <span className="num">{g.members.length}</span>,
    },
    {
      key: 'parent',
      header: t('nx.grp.colParent'),
      secondary: true,
      cell: (g) => (
        <span className="text-muted">
          {g.members.find((m) => m.is_parent)?.name ?? t('nx.grp.noParent')}
        </span>
      ),
    },
    {
      key: 'act',
      header: t('nx.grp.colAction'),
      width: 'w-28',
      cell: (g) => (
        <Button variant="ghost" onClick={() => setOpenID(openID === g.id ? '' : g.id)}>
          {t(openID === g.id ? 'nx.grp.hide' : 'nx.grp.look')}
        </Button>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('nx.grp.title')}
        description={t('nx.grp.subtitle')}
        actions={
          mayManage ? (
            <Button variant="primary" onClick={() => setNaming((v) => !v)}>
              {t('nx.grp.add')}
            </Button>
          ) : null
        }
      />

      <FormError message={error} className="mb-4" />

      {naming && mayManage ? (
        <Panel
          className="mb-6"
          title={t('nx.grp.addTitle')}
          description={t('nx.grp.addDesc')}
          actions={
            <Button variant="ghost" onClick={() => setNaming(false)}>
              {t('nx.grp.cancel')}
            </Button>
          }
        >
          <div className="flex flex-wrap items-end gap-3">
            <Field name="name" label={t('nx.grp.name')}>
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field
              name="currency"
              label={t('nx.grp.currency')}
              hint={t('nx.grp.currencyHint')}
            >
              <Input
                value={currency}
                onChange={(e) => setCurrency(e.target.value)}
                maxLength={3}
                className="uppercase"
              />
            </Field>
            <Button
              variant="primary"
              disabled={busy || !name.trim() || currency.trim().length !== 3}
              onClick={() => void create()}
            >
              {t('nx.grp.save')}
            </Button>
          </div>
        </Panel>
      ) : null}

      {groups.error && !notSold ? (
        <ErrorState error={groups.error} onRetry={() => void groups.refetch()} />
      ) : null}
      {groups.isLoading && !groups.data ? <TableSkeleton columns={4} /> : null}

      {!groups.isLoading && !groups.error && rows.length === 0 ? (
        <EmptyState
          icon={Building2}
          title={t('nx.grp.emptyTitle')}
          description={t('nx.grp.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.grp.title')}
          columns={columns}
          rows={rows}
          rowKey={(g) => g.id}
        />
      ) : null}

      {open ? (
        <div className="mt-8 flex flex-col gap-6">
          <section>
            <h2 className="mb-3 text-card-title font-semibold text-fg">
              {t('nx.grp.membersTitle', { group: open.name })}
            </h2>
            {open.members.length === 0 ? (
              <EmptyState
                icon={Building2}
                title={t('nx.grp.noMembersTitle')}
                description={t('nx.grp.noMembersDesc')}
              />
            ) : (
              <DataTable
                caption={t('nx.grp.membersTitle', { group: open.name })}
                columns={[
                  {
                    key: 'name',
                    header: t('nx.grp.colCompany'),
                    primary: true,
                    cell: (m: Member) => (
                      <span className="flex items-center gap-2">
                        <span className="font-medium">{m.name}</span>
                        {m.is_parent ? (
                          <Badge tone="info">{t('nx.grp.parent')}</Badge>
                        ) : null}
                      </span>
                    ),
                  },
                  {
                    key: 'own',
                    header: t('nx.grp.colOwnership'),
                    width: 'w-32',
                    // A string all the way. Three subsidiaries at 33.33 each
                    // is exactly where a float starts producing 99.99000001.
                    cell: (m: Member) => <span className="num">{m.ownership_pct}%</span>,
                  },
                  {
                    key: 'currency',
                    header: t('nx.grp.colBaseCurrency'),
                    secondary: true,
                    width: 'w-36',
                    cell: (m: Member) => <span className="num">{m.base_currency}</span>,
                  },
                  ...(mayManage
                    ? [
                        {
                          key: 'actions',
                          header: t('nx.grp.colActions'),
                          width: 'w-32',
                          cell: (m: Member) => (
                            <Button
                              size="sm"
                              variant="ghost"
                              disabled={busy}
                              onClick={() => void removeMember(m.company_id)}
                            >
                              {t('nx.grp.removeMember')}
                            </Button>
                          ),
                        } as Column<Member>,
                      ]
                    : []),
                ]}
                rows={open.members}
                rowKey={(m) => m.company_id}
              />
            )}

            {mayManage ? (
              <Panel
                className="mt-4"
                title={t('nx.grp.addMemberTitle')}
                description={t('nx.grp.addMemberDesc')}
              >
                <div className="grid items-end gap-4 sm:grid-cols-2 lg:grid-cols-4">
                  <Field name="company_id" label={t('nx.grp.colCompany')} required>
                    <Select
                      value={memberID}
                      onChange={(e) => setMemberID(e.target.value)}
                    >
                      <option value="">{t('nx.grp.chooseCompany')}</option>
                      {(companies.data?.data ?? [])
                        // Already in this group is not a choice; the server
                        // refuses it and offering it collects the refusal.
                        .filter(
                          (c) => !open.members.some((m) => m.company_id === c.id),
                        )
                        .map((c) => (
                          <option key={c.id} value={c.id}>
                            {c.trade_name || c.legal_name}
                          </option>
                        ))}
                    </Select>
                  </Field>
                  <Field
                    name="ownership_pct"
                    label={t('nx.grp.colOwnership')}
                    hint={t('nx.grp.ownershipHint')}
                  >
                    <Input
                      className="num"
                      inputMode="decimal"
                      value={ownership}
                      onChange={(e) => setOwnership(e.target.value)}
                    />
                  </Field>
                  <Checkbox
                    name="is_parent"
                    label={t('nx.grp.isParent')}
                    hint={t('nx.grp.isParentHint')}
                    checked={isParent}
                    onChange={(e) => setIsParent(e.target.checked)}
                  />
                  <Button
                    variant="primary"
                    disabled={busy || !memberID}
                    onClick={() => void addMember()}
                  >
                    {t('nx.grp.addMember')}
                  </Button>
                </div>
              </Panel>
            ) : null}
          </section>

          <section>
            <h2 className="mb-1 text-card-title font-semibold text-fg">
              {t('nx.grp.statementTitle')}
            </h2>
            <p className="mb-3 max-w-prose text-caption text-muted">
              {t('nx.grp.statementDesc')}
            </p>
            {statement.isLoading ? <TableSkeleton columns={2} /> : null}
            {statement.error ? (
              <ErrorState
                error={statement.error}
                onRetry={() => void statement.refetch()}
              />
            ) : null}
            {(statement.data?.lines ?? []).length === 0 && !statement.isLoading ? (
              <EmptyState
                icon={Building2}
                title={t('nx.grp.noStatementTitle')}
                description={t('nx.grp.noStatementDesc')}
              />
            ) : (
              <DataTable
                caption={t('nx.grp.statementTitle')}
                columns={[
                  {
                    key: 'label',
                    header: t('nx.grp.colLine'),
                    primary: true,
                    cell: (l: StatementLine) => (
                      <span className="flex flex-col gap-0.5">
                        <span>{l.label}</span>
                        <span className="num text-caption text-muted">{l.account}</span>
                      </span>
                    ),
                  },
                  {
                    key: 'amount',
                    header: t('nx.grp.colAmount'),
                    width: 'w-44',
                    cell: (l: StatementLine) => (
                      <span className="num">{l.amount}</span>
                    ),
                  },
                ]}
                rows={statement.data?.lines ?? []}
                rowKey={(l) => l.account}
              />
            )}
          </section>

          {scope && open ? (
            <IntercompanyPanel
              groupId={open.id}
              members={open.members ?? []}
              companyId={scope.company_id}
              from={icFrom}
              to={icTo}
              money={(v) =>
                formatMoney(v, {
                  currency: statement.data?.currency ?? baseCurrency,
                  market,
                })
              }
            />
          ) : null}

        </div>
      ) : null}
    </>
  );
}

export default function GroupsPage() {
  return (
    <RequirePermission anyOf={['group.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <GroupsScreen />
      </Suspense>
    </RequirePermission>
  );
}
