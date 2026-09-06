'use client';

// F1's configurable approval engine, given the screen it never had.
//
// Three routes were live and uncalled: `GET /approval-rules`,
// `POST /approval-rules`, `POST /approval-rules/{id}/active`. The approval
// inbox at `/approvals` shows what a rule PRODUCED; nothing in the product
// could say what the rules were. A shop wanting "expenses over 5,000 need the
// owner" had to be given it by whoever seeded their database.
//
// # A rule is not edited, it is replaced
//
// `POST /approval-rules` always inserts. There is no PUT, and that is not an
// oversight: `approval_request.rule_id` points at the rule that raised it, so
// editing a rule in place would silently rewrite the reason for every decision
// already taken under it. Changing a threshold therefore means switching the
// old rule off and saving a new one — which the form does in one action, and
// says so.
//
// # The condition offered depends on the subject
//
// See `lib/workflow/rules.ts`. The short version: a clause naming a fact the
// caller does not supply can never match, so offering a store threshold on a
// purchase order would produce a rule that never fires and give the person who
// wrote it no way to find out.
//
// # Steps only exist for one action
//
// `approval_rule_routes_somewhere` is a CHECK constraint: an action of
// `require_approval` with no steps is refused by the database. The form shows
// the step builder only for that action, so the refusal cannot be reached.

import { GitBranch, Plus, X } from 'lucide-react';
import { useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import type { Person, RoleOption } from '@/lib/people/roles';
import {
  ACTIONS,
  CLAUSES_FOR,
  SUBJECTS,
  decodeCondition,
  decodeSteps,
  isSubject,
  type Action,
  type Clause,
  type Condition,
  type RuleRow,
  type Step,
  type Subject,
} from '@/lib/workflow/rules';
import { useUrlFlag, useUrlState } from '@/lib/url-state';

/** Only the two fields a branch condition needs. Declared here as the other
 *  screens reading `/stores` do, rather than importing a shape from one of
 *  them. */
interface Store {
  id: string;
  name: string;
}

/** The translate function, for the sentence builders below the component. */
type T = ReturnType<typeof useT>;

export function Rules() {
  const t = useT();
  const scope = useCompanyScope();

  const [creating, setCreating] = useUrlFlag('newRule');
  const [replacing, setReplacing] = useUrlState('replace');

  const { data, isLoading, error, refetch } = useApiList<RuleRow>(
    scope ? '/approval-rules' : null,
    scope ?? undefined,
  );
  const stores = useApiList<Store>(scope ? '/stores' : null, scope ?? undefined);
  const roles = useApiList<RoleOption>(
    scope ? '/people/roles' : null,
    scope ?? undefined,
  );
  const people = useApiList<Person>(scope ? '/people' : null, scope ?? undefined);

  const rows = data?.data ?? [];
  const from = rows.find((r) => r.id === replacing) ?? null;
  const showForm = creating || from !== null;

  function close() {
    setCreating(false);
    setReplacing('');
  }

  async function setActive(rule: RuleRow, active: boolean) {
    if (!scope) return;
    await api.post(
      `/approval-rules/${rule.id}/active?company_id=${scope.company_id}`,
      { is_active: active },
    );
    void refetch();
  }

  const columns: Column<RuleRow>[] = [
    {
      key: 'name',
      header: t('nx.aprc.colName'),
      primary: true,
      cell: (r) => (
        <span className="flex items-center gap-2">
          {r.name}
          {!r.is_active ? <Badge>{t('nx.aprc.off')}</Badge> : null}
        </span>
      ),
    },
    {
      key: 'subject',
      header: t('nx.aprc.colSubject'),
      cell: (r) => (
        <span className="text-muted">{subjectLabel(t, r.subject)}</span>
      ),
    },
    {
      key: 'when',
      header: t('nx.aprc.colWhen'),
      cell: (r) => (
        <span className="text-muted">
          {conditionSentence(t, r.condition, stores.data?.data ?? [])}
        </span>
      ),
    },
    {
      key: 'then',
      header: t('nx.aprc.colThen'),
      cell: (r) => (
        <span className="flex flex-wrap items-center gap-1">
          <Badge tone={r.action === 'block' ? 'critical' : 'neutral'}>
            {actionLabel(t, r.action)}
          </Badge>
          {r.action === 'require_approval' ? (
            <span className="text-muted">
              {routeSentence(t, r.steps, roles.data?.data ?? [], people.data?.data ?? [])}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'priority',
      header: t('nx.aprc.colPriority'),
      secondary: true,
      // Higher first, which is not obvious from a bare number, so the column
      // header carries the explanation and the cell carries the figure.
      cell: (r) => <span className="num text-muted">{r.priority}</span>,
    },
    {
      key: 'act',
      header: '',
      cell: (r) => (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => void setActive(r, !r.is_active)}
        >
          {r.is_active ? t('nx.aprc.switchOff') : t('nx.aprc.switchOn')}
        </Button>
      ),
    },
  ];

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_26rem]">
      <div className="min-w-0">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <p className="text-caption text-muted">{t('nx.aprc.rulesLead')}</p>
          <Button
            variant="primary"
            size="sm"
            onClick={() => {
              setReplacing('');
              setCreating(true);
            }}
          >
            {t('nx.aprc.newRule')}
          </Button>
        </div>

        {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
        {isLoading && !data ? <TableSkeleton columns={6} /> : null}

        {!isLoading && !error && rows.length === 0 ? (
          <EmptyState
            icon={GitBranch}
            title={t('nx.aprc.rulesEmptyTitle')}
            description={t('nx.aprc.rulesEmptyDesc')}
          />
        ) : null}

        {rows.length > 0 ? (
          <DataTable
            caption={t('nx.aprc.rulesCaption')}
            columns={columns}
            rows={rows}
            rowKey={(r) => r.id}
            isSelected={(r) => r.id === from?.id}
            onOpenRow={(r) => {
              setCreating(false);
              setReplacing(r.id);
            }}
          />
        ) : null}
      </div>

      {showForm ? (
        <RuleForm
          key={from?.id ?? 'new'}
          replacing={from}
          stores={stores.data?.data ?? []}
          roles={roles.data?.data ?? []}
          people={people.data?.data ?? []}
          onDone={() => {
            void refetch();
            close();
          }}
          onCancel={close}
        />
      ) : null}
    </div>
  );
}

// --- the form ------------------------------------------------------------

function RuleForm({
  replacing,
  stores,
  roles,
  people,
  onDone,
  onCancel,
}: {
  replacing: RuleRow | null;
  stores: Store[];
  roles: RoleOption[];
  people: Person[];
  onDone: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();

  const seedCondition = replacing ? decodeCondition(replacing.condition) : {};
  const seedSubject =
    replacing && isSubject(replacing.subject) ? replacing.subject : 'expense';

  const [name, setName] = useState(replacing?.name ?? '');
  const [subject, setSubject] = useState<Subject>(seedSubject);
  const [action, setAction] = useState<Action>(
    (replacing?.action as Action | undefined) ?? 'require_approval',
  );
  const [condition, setCondition] = useState<Condition>(seedCondition);
  const [steps, setSteps] = useState<Step[]>(
    replacing ? decodeSteps(replacing.steps) : [{ role: 'owner' }],
  );
  const [escalate, setEscalate] = useState(
    replacing?.escalate_after_hours === undefined
      ? ''
      : String(replacing.escalate_after_hours),
  );
  const [priority, setPriority] = useState(String(replacing?.priority ?? 0));

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const offered = CLAUSES_FOR[subject];
  const needsSteps = action === 'require_approval';

  // A clause that belonged to the old subject and not the new one is dropped
  // when the subject changes, rather than saved invisibly.
  function changeSubject(next: Subject) {
    setSubject(next);
    const keep = CLAUSES_FOR[next];
    setCondition((current) => {
      const out: Condition = {};
      for (const clause of keep) {
        if (clause === 'after_hour' || clause === 'before_hour') {
          if (current[clause] !== undefined) out[clause] = current[clause];
        } else if (current[clause] !== undefined) {
          out[clause] = current[clause];
        }
      }
      return out;
    });
  }

  function setClause(clause: Clause, raw: string) {
    setCondition((current) => {
      const out = { ...current };
      if (raw.trim() === '') {
        delete out[clause];
        return out;
      }
      if (clause === 'after_hour' || clause === 'before_hour') {
        // `Number('')` is 0, so emptiness is checked above rather than relied
        // on falling out of the parse.
        const hour = Number(raw);
        if (!Number.isFinite(hour)) return current;
        out[clause] = Math.trunc(hour);
      } else {
        out[clause] = raw.trim();
      }
      return out;
    });
  }

  async function save() {
    if (!scope) return;
    setBusy(true);
    setError(null);
    setFieldErrors({});

    const routed = needsSteps
      ? steps.filter((s) => (s.role ?? '') !== '' || (s.user_id ?? '') !== '')
      : [];
    if (needsSteps && routed.length === 0) {
      setFieldErrors({ steps: t('nx.aprc.errNoSteps') });
      setError(t('nx.aprc.errNoStepsLead'));
      setBusy(false);
      return;
    }

    try {
      await api.post(`/approval-rules?company_id=${scope.company_id}`, {
        name,
        subject,
        action,
        condition,
        steps: routed,
        // Empty is "never escalate", which is not the same as zero — and the
        // column refuses a zero, so the distinction has to be made here.
        escalate_after_hours: escalate.trim() === '' ? null : Number(escalate),
        priority: priority.trim() === '' ? 0 : Number(priority),
      });

      // Replacing means the old rule stops applying. Done after the new rule
      // is safely saved, so a failure leaves the old one still working rather
      // than leaving the shop with no rule at all.
      if (replacing && replacing.is_active) {
        await api.post(
          `/approval-rules/${replacing.id}/active?company_id=${scope.company_id}`,
          { is_active: false },
        );
      }
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <Panel title={replacing ? t('nx.aprc.replaceRule') : t('nx.aprc.newRule')}>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <FormError message={error} fields={fieldErrors} />

        {replacing ? (
          <p className="rounded-xs border border-line bg-ground px-3 py-2 text-caption text-muted">
            {t('nx.aprc.replaceExplain')}
          </p>
        ) : null}

        <Field name="name" label={t('nx.aprc.fName')} error={fieldErrors.name} required>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus
            placeholder={t('nx.aprc.fNamePlaceholder')}
          />
        </Field>

        <Field
          name="subject"
          label={t('nx.aprc.fSubject')}
          hint={t('nx.aprc.fSubjectHint')}
          error={fieldErrors.subject}
          required
        >
          <Select
            value={subject}
            onChange={(e) => changeSubject(e.target.value as Subject)}
          >
            {SUBJECTS.map((s) => (
              <option key={s} value={s}>
                {subjectLabel(t, s)}
              </option>
            ))}
          </Select>
        </Field>

        <fieldset className="flex flex-col gap-3 border-t border-line pt-4">
          <legend className="sr-only">{t('nx.aprc.whenLegend')}</legend>
          <p className="text-caption font-medium">{t('nx.aprc.whenLegend')}</p>
          <p className="text-caption text-muted">{t('nx.aprc.whenHint')}</p>

          {offered.includes('amount_over') ? (
            <Field name="amount_over" label={t('nx.aprc.fAmountOver')}>
              <Input
                inputMode="decimal"
                className="num"
                value={condition.amount_over ?? ''}
                onChange={(e) => setClause('amount_over', e.target.value)}
              />
            </Field>
          ) : null}

          {offered.includes('amount_under') ? (
            <Field name="amount_under" label={t('nx.aprc.fAmountUnder')}>
              <Input
                inputMode="decimal"
                className="num"
                value={condition.amount_under ?? ''}
                onChange={(e) => setClause('amount_under', e.target.value)}
              />
            </Field>
          ) : null}

          {offered.includes('store_id') ? (
            <Field
              name="store_id"
              label={t('nx.aprc.fStore')}
              hint={t('nx.aprc.fStoreHint')}
            >
              <Select
                value={condition.store_id ?? ''}
                onChange={(e) => setClause('store_id', e.target.value)}
              >
                <option value="">{t('nx.aprc.anyStore')}</option>
                {stores.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </Field>
          ) : null}

          {offered.includes('after_hour') ? (
            <div className="grid gap-3 sm:grid-cols-2">
              <Field
                name="after_hour"
                label={t('nx.aprc.fAfterHour')}
                hint={t('nx.aprc.fHourHint')}
              >
                <Input
                  inputMode="numeric"
                  className="num"
                  value={condition.after_hour === undefined ? '' : String(condition.after_hour)}
                  onChange={(e) => setClause('after_hour', e.target.value)}
                />
              </Field>
              <Field name="before_hour" label={t('nx.aprc.fBeforeHour')}>
                <Input
                  inputMode="numeric"
                  className="num"
                  value={
                    condition.before_hour === undefined ? '' : String(condition.before_hour)
                  }
                  onChange={(e) => setClause('before_hour', e.target.value)}
                />
              </Field>
            </div>
          ) : null}
        </fieldset>

        <fieldset className="flex flex-col gap-3 border-t border-line pt-4">
          <legend className="sr-only">{t('nx.aprc.thenLegend')}</legend>
          <p className="text-caption font-medium">{t('nx.aprc.thenLegend')}</p>

          <Field name="action" label={t('nx.aprc.fAction')} error={fieldErrors.action}>
            <Select
              value={action}
              onChange={(e) => setAction(e.target.value as Action)}
            >
              {ACTIONS.map((a) => (
                <option key={a} value={a}>
                  {actionLabel(t, a)}
                </option>
              ))}
            </Select>
          </Field>
          <p className="text-caption text-muted">{actionExplain(t, action)}</p>

          {needsSteps ? (
            <StepBuilder
              steps={steps}
              roles={roles}
              people={people}
              error={fieldErrors.steps}
              onChange={setSteps}
            />
          ) : null}
        </fieldset>

        <div className="grid gap-3 border-t border-line pt-4 sm:grid-cols-2">
          <Field
            name="escalate_after_hours"
            label={t('nx.aprc.fEscalate')}
            hint={t('nx.aprc.fEscalateHint')}
          >
            <Input
              inputMode="numeric"
              className="num"
              value={escalate}
              onChange={(e) => setEscalate(e.target.value)}
            />
          </Field>
          <Field
            name="priority"
            label={t('nx.aprc.fPriority')}
            hint={t('nx.aprc.fPriorityHint')}
          >
            <Input
              inputMode="numeric"
              className="num"
              value={priority}
              onChange={(e) => setPriority(e.target.value)}
            />
          </Field>
        </div>

        <div className="flex flex-wrap items-center gap-2 border-t border-line pt-4">
          <Button type="submit" variant="primary" busy={busy}>
            {replacing ? t('nx.aprc.saveReplacement') : t('nx.aprc.save')}
          </Button>
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('nx.aprc.cancel')}
          </Button>
        </div>
      </form>
    </Panel>
  );
}

// --- the step builder ----------------------------------------------------

/**
 * The signatures a request collects, in order.
 *
 * Order is the whole point: the engine grants at the LAST step, so "manager,
 * then accountant, then owner" is three different products from any other
 * arrangement of the same three. Moving a step is therefore a first-class
 * action rather than something achieved by deleting and re-adding.
 *
 * Only system roles are offered. The engine matches a role through
 * `coalesce(cloned_from, id)`, so a step naming a COPY of a system role can
 * never match anybody — the copy resolves to its original, and the original's
 * key is not what the step said. Offering the copies would produce rules that
 * silently approve nothing.
 */
function StepBuilder({
  steps,
  roles,
  people,
  error,
  onChange,
}: {
  steps: Step[];
  roles: RoleOption[];
  people: Person[];
  error?: string;
  onChange: (next: Step[]) => void;
}) {
  const t = useT();
  const systemRoles = roles.filter((r) => r.is_system);

  function update(index: number, next: Step) {
    onChange(steps.map((s, i) => (i === index ? next : s)));
  }

  function move(index: number, by: number) {
    const target = index + by;
    if (target < 0 || target >= steps.length) return;
    const next = [...steps];
    const moved = next[index];
    const displaced = next[target];
    if (moved === undefined || displaced === undefined) return;
    next[index] = displaced;
    next[target] = moved;
    onChange(next);
  }

  return (
    <div className="flex flex-col gap-2">
      <p className="text-caption font-medium">{t('nx.aprc.stepsLabel')}</p>
      <p className="text-caption text-muted">{t('nx.aprc.stepsHint')}</p>
      {error ? (
        <p className="text-caption text-critical-fg" role="alert">
          {error}
        </p>
      ) : null}

      <ol className="flex flex-col gap-2">
        {steps.map((step, index) => {
          const kind = step.user_id !== undefined && step.user_id !== '' ? 'user' : 'role';
          return (
            <li
              key={index}
              className="flex flex-wrap items-end gap-2 rounded-xs border border-line p-2"
            >
              <span className="num flex h-10 w-6 items-center justify-center text-caption text-muted">
                {index + 1}
              </span>

              <label className="min-w-0 flex-1">
                <span className="sr-only">{t('nx.aprc.stepWho')}</span>
                <Select
                  className="w-full"
                  value={kind}
                  onChange={(e) =>
                    update(
                      index,
                      e.target.value === 'user'
                        ? { user_id: people[0]?.id ?? '' }
                        : { role: systemRoles[0]?.key ?? 'owner' },
                    )
                  }
                >
                  <option value="role">{t('nx.aprc.stepByRole')}</option>
                  <option value="user">{t('nx.aprc.stepByPerson')}</option>
                </Select>
              </label>

              <label className="min-w-0 flex-[2]">
                <span className="sr-only">{t('nx.aprc.stepWhich')}</span>
                {kind === 'role' ? (
                  <Select
                    className="w-full"
                    value={step.role ?? ''}
                    onChange={(e) => update(index, { role: e.target.value })}
                  >
                    {systemRoles.map((r) => (
                      <option key={r.id} value={r.key}>
                        {r.name}
                      </option>
                    ))}
                  </Select>
                ) : (
                  <Select
                    className="w-full"
                    value={step.user_id ?? ''}
                    onChange={(e) => update(index, { user_id: e.target.value })}
                  >
                    {people.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.full_name}
                      </option>
                    ))}
                  </Select>
                )}
              </label>

              <div className="flex items-center gap-1">
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t('nx.aprc.stepUp')}
                  disabled={index === 0}
                  onClick={() => move(index, -1)}
                >
                  ↑
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t('nx.aprc.stepDown')}
                  disabled={index === steps.length - 1}
                  onClick={() => move(index, 1)}
                >
                  ↓
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t('nx.aprc.stepRemove')}
                  disabled={steps.length === 1}
                  onClick={() => onChange(steps.filter((_, i) => i !== index))}
                >
                  <X aria-hidden className="size-4" />
                </Button>
              </div>
            </li>
          );
        })}
      </ol>

      <Button
        variant="ghost"
        size="sm"
        className="self-start"
        onClick={() => onChange([...steps, { role: systemRoles[0]?.key ?? 'owner' }])}
      >
        <Plus aria-hidden className="size-4" />
        {t('nx.aprc.stepAdd')}
      </Button>
    </div>
  );
}

// --- saying what a rule does, in a sentence ------------------------------

function subjectLabel(t: T, subject: string): string {
  if (subject === 'expense') return t('nx.aprc.subjectExpense');
  if (subject === 'purchase_order') return t('nx.aprc.subjectPurchaseOrder');
  // A subject configured before this screen existed, or by hand. Shown as it
  // is rather than hidden: a rule nobody can read is worse than an untidy one.
  return subject;
}

function actionLabel(t: T, action: string): string {
  switch (action) {
    case 'require_approval':
      return t('nx.aprc.actionApproval');
    case 'require_pin':
      return t('nx.aprc.actionPin');
    case 'block':
      return t('nx.aprc.actionBlock');
    case 'warn':
      return t('nx.aprc.actionWarn');
    case 'notify':
      return t('nx.aprc.actionNotify');
    default:
      return action;
  }
}

function actionExplain(t: T, action: Action): string {
  switch (action) {
    case 'require_approval':
      return t('nx.aprc.explainApproval');
    case 'require_pin':
      return t('nx.aprc.explainPin');
    case 'block':
      return t('nx.aprc.explainBlock');
    case 'warn':
      return t('nx.aprc.explainWarn');
    default:
      return t('nx.aprc.explainNotify');
  }
}

/** The condition as something a person can read. */
function conditionSentence(t: T, raw: string, stores: Store[]): string {
  const c = decodeCondition(raw);
  const parts: string[] = [];
  if (c.amount_over !== undefined) {
    parts.push(t('nx.aprc.sentOver', { amount: c.amount_over }));
  }
  if (c.amount_under !== undefined) {
    parts.push(t('nx.aprc.sentUnder', { amount: c.amount_under }));
  }
  if (c.store_id !== undefined) {
    const store = stores.find((s) => s.id === c.store_id);
    parts.push(t('nx.aprc.sentAt', { store: store?.name ?? c.store_id }));
  }
  if (c.after_hour !== undefined) {
    parts.push(t('nx.aprc.sentAfter', { hour: String(c.after_hour) }));
  }
  if (c.before_hour !== undefined) {
    parts.push(t('nx.aprc.sentBefore', { hour: String(c.before_hour) }));
  }
  return parts.length === 0 ? t('nx.aprc.sentAlways') : parts.join(t('nx.aprc.sentAnd'));
}

/** Who signs, in order. */
function routeSentence(t: T, raw: string, roles: RoleOption[], people: Person[]): string {
  const steps = decodeSteps(raw);
  if (steps.length === 0) return '';
  return steps
    .map((s) => {
      if (s.user_id !== undefined && s.user_id !== '') {
        return people.find((p) => p.id === s.user_id)?.full_name ?? s.user_id;
      }
      return roles.find((r) => r.key === s.role)?.name ?? (s.role ?? '');
    })
    .join(t('nx.aprc.sentThen'));
}

export function RulesTab() {
  // Reading the rules is `approval.manage_rules` on the server, not
  // `approval.view` — an approver may see their queue without seeing the
  // thresholds that fill it — so the tab is guarded on what the route asks
  // rather than on what the page asks.
  return (
    <RequirePermission anyOf={['approval.manage_rules']}>
      <Rules />
    </RequirePermission>
  );
}
