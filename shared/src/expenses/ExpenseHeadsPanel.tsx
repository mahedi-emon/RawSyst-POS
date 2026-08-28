// The categories expenses are booked to, and the one tax decision in them.
//
// A category is mostly a label. `input_vat_recoverable` is not: blueprint E2.3
// restricts input VAT recovery on entertainment, some vehicles and fuel, and
// getting the flag wrong overstates every VAT return the shop files afterwards.
// So it is the field this panel is arranged around — stated in words on every
// row rather than shown as a tick somebody has to interpret, and asked as a
// choice rather than a checkbox that defaults to something.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import type { FieldErrors } from '../ui/Form';
import { money } from '../ui/format';
import { useT } from '../i18n/locale';
import {
  createExpenseHead,
  listExpenseAccounts,
  listExpenseHeads,
  setExpenseHeadActive,
  updateExpenseHead,
  type ExpenseAccount,
  type ExpenseHead,
} from '../api/expenses';

export function ExpenseHeadsPanel({
  companyId,
  onChanged,
}: {
  companyId: string;
  onChanged: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const load = useCallback(
    () => listExpenseHeads(client, companyId, true),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const loadAccounts = useCallback(
    () => listExpenseAccounts(client, companyId),
    [client, companyId],
  );
  const accounts = useRemote(loadAccounts);

  const [editing, setEditing] = useState<ExpenseHead | null>(null);
  const [creating, setCreating] = useState(false);

  const refresh = () => {
    setCreating(false);
    setEditing(null);
    reload();
    onChanged();
  };

  return (
    <>
      {(creating || editing) && (
        <HeadForm
          companyId={companyId}
          head={editing}
          accounts={accounts.remote.state === 'ready' ? accounts.remote.data : []}
          onCancel={() => {
            setCreating(false);
            setEditing(null);
          }}
          onSaved={refresh}
        />
      )}

      <section className="ds-panel" aria-label={t('exp.categories')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('exp.categories')}</h2>
          {!creating && !editing && (
            <button className="ds-btn ds-btn--quiet" onClick={() => setCreating(true)}>
              {t('exp.addCategory')}
            </button>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(heads: ExpenseHead[]) =>
            heads.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState title={t('exp.noHeadsTitle')} body={t('exp.noHeadsBody')} />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('exp.category')}</th>
                      <th scope="col">{t('exp.postsTo')}</th>
                      <th scope="col">{t('exp.inputVat')}</th>
                      <th scope="col" className="num">{t('exp.spent')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">{t('common.actions')}</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {heads.map((h) => (
                      <HeadRow
                        key={h.id}
                        companyId={companyId}
                        head={h}
                        onEdit={() => setEditing(h)}
                        onChanged={refresh}
                      />
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

function HeadRow({
  companyId,
  head,
  onEdit,
  onChanged,
}: {
  companyId: string;
  head: ExpenseHead;
  onEdit: () => void;
  onChanged: () => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const [busy, setBusy] = useState(false);

  async function toggle() {
    if (busy) return;
    setBusy(true);
    try {
      await setExpenseHeadActive(client, companyId, head.id, !head.is_active);
      onChanged();
    } finally {
      setBusy(false);
    }
  }

  return (
    <tr className={head.is_active ? undefined : 'detail__row--aside'}>
      <td>
        <span className="detail__strong">{head.name}</span>
        <span className="ds-caption">{head.code}</span>
      </td>
      <td>
        <span className="detail__strong">{head.account_name}</span>
        <span className="ds-caption">{head.account_code}</span>
      </td>
      <td>
        {/* In words, not a tick. "Reclaimable" and "Not reclaimable" are what
            the flag MEANS, and a reader should not have to remember which way
            round a checkbox in this column points. */}
        <span
          className={`ds-badge ds-badge--${head.input_vat_recoverable ? 'success' : 'warning'}`}
        >
          {t(head.input_vat_recoverable ? 'exp.reclaimable' : 'exp.notReclaimable')}
        </span>
      </td>
      <td className="num">{money(head.spent)}</td>
      <td>
        <div className="supplier__actions">
          <button className="ds-btn ds-btn--quiet" onClick={onEdit}>
            {t('action.edit')}
          </button>
          <button
            className={`ds-btn ${head.is_active ? 'ds-btn--warn' : 'ds-btn--quiet'}`}
            onClick={toggle}
            disabled={busy}
          >
            {t(head.is_active ? 'exp.retire' : 'exp.restore')}
          </button>
        </div>
      </td>
    </tr>
  );
}

function HeadForm({
  companyId,
  head,
  accounts,
  onCancel,
  onSaved,
}: {
  companyId: string;
  head: ExpenseHead | null;
  accounts: ExpenseAccount[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [code, setCode] = useState(head?.code ?? '');
  const [name, setName] = useState(head?.name ?? '');
  const [nameAr, setNameAr] = useState(head?.name_ar ?? '');
  const [accountId, setAccountId] = useState(head?.account_id ?? accounts[0]?.id ?? '');
  // Null until chosen on a NEW category, so the form cannot be submitted with
  // a tax position nobody decided. Editing starts from what the head says.
  const [recoverable, setRecoverable] = useState<boolean | null>(
    head ? head.input_vat_recoverable : null,
  );
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<string | null>(null);
  const [fields, setFields] = useState<FieldErrors>({});

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy || recoverable === null) return;
    setBusy(true);
    setFailed(null);
    setFields({});
    const body = {
      code: code.trim(),
      name: name.trim(),
      name_ar: nameAr.trim() || undefined,
      account_id: accountId,
      input_vat_recoverable: recoverable,
    };
    try {
      if (head) await updateExpenseHead(client, companyId, head.id, body);
      else await createExpenseHead(client, companyId, body);
      onSaved();
    } catch (err) {
      const e = err as { message?: string; fields?: FieldErrors };
      setFailed(e.message ?? t('exp.categoryFailed'));
      if (e.fields) setFields(e.fields);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t(head ? 'exp.editCategory' : 'exp.addCategory')}>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t(head ? 'exp.editCategory' : 'exp.addCategory')}</h2>
      </div>
      <form className="ds-panel__body exp__form" onSubmit={submit}>
        <FormError message={failed} />

        <div className="exp__formrow">
          <Field
            label={t('common.code')}
            htmlFor="head-code"
            required
            error={fields.code}
            /* Said once, where it matters: a year of expense reports refer to
               the code, so changing it would rewrite what they mean. */
            hint={head ? t('exp.codeFixed') : undefined}
          >
            <TextInput
              id="head-code"
              value={code}
              onChange={setCode}
              error={fields.code}
            />
          </Field>
          <Field label={t('common.name')} htmlFor="head-name" required error={fields.name}>
            <TextInput id="head-name" value={name} onChange={setName} error={fields.name} />
          </Field>
        </div>

        <Field label={t('exp.nameAr')} htmlFor="head-namear">
          <TextInput id="head-namear" value={nameAr} onChange={setNameAr} />
        </Field>

        <Field
          label={t('exp.postsTo')}
          htmlFor="head-account"
          required
          error={fields.account_id}
        >
          <select
            id="head-account"
            className="field__input"
            value={accountId}
            onChange={(e) => setAccountId(e.target.value)}
          >
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.code} · {a.name}
              </option>
            ))}
          </select>
        </Field>

        <fieldset className="exp__vat">
          <legend className="field__label">{t('exp.inputVat')}</legend>
          <p className="field__hint">{t('exp.inputVatHint')}</p>

          {/* Radios, not a checkbox. A checkbox has a default, and both
              defaults are wrong: unticked silently stops a shop reclaiming VAT
              it is owed, ticked silently claims VAT on entertainment. This is
              a decision, so it is asked as one. */}
          <label className="exp__choice">
            <input
              type="radio"
              name="recoverable"
              checked={recoverable === true}
              onChange={() => setRecoverable(true)}
            />
            <span>
              <span className="detail__strong">{t('exp.reclaimable')}</span>
              <span className="ds-caption">{t('exp.reclaimableHint')}</span>
            </span>
          </label>

          <label className="exp__choice">
            <input
              type="radio"
              name="recoverable"
              checked={recoverable === false}
              onChange={() => setRecoverable(false)}
            />
            <span>
              <span className="detail__strong">{t('exp.notReclaimable')}</span>
              <span className="ds-caption">{t('exp.notReclaimableHint')}</span>
            </span>
          </label>
        </fieldset>

        <FormActions
          submitLabel={t(head ? 'action.save' : 'exp.addCategory')}
          busy={busy}
          disabled={!code.trim() || !name.trim() || !accountId || recoverable === null}
          onCancel={onCancel}
        />
      </form>
    </section>
  );
}
