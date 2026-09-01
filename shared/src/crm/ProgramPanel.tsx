// The loyalty scheme itself (blueprint B16).
//
// # The two rates are separate on purpose
//
// How much has to be spent to earn a point, and what a point is worth when it
// is spent. A shop that wants to be more generous changes the first; a shop
// that wants each point to buy more changes the second. Folding them into one
// "rate" would make both changes the same change.
//
// # Changing the scheme does not revalue points already earned
//
// Those were posted at the value in force when they were earned, and repricing
// them would move a liability a closed month has already reported. The screen
// says so, because somebody halving the point value expects the outstanding
// balance to halve and it will not.

import { useCallback, useState } from 'react';

import { readProgram, saveProgram, type LoyaltyProgram, type Tier } from '../api/crm';
import { useAuth } from '../auth/session';
import { RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useT } from '../i18n/locale';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { money } from '../ui/format';

export function ProgramPanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();

  const load = useCallback(
    () => readProgram(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(program: LoyaltyProgram) => (
        <ProgramForm
          companyId={companyId}
          program={program}
          onSaved={reload}
        />
      )}
    </RemoteBody>
  );
}

function ProgramForm({
  companyId,
  program,
  onSaved,
}: {
  companyId: string;
  program: LoyaltyProgram;
  onSaved: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [active, setActive] = useState(program.exists ? program.is_active : true);
  const [spend, setSpend] = useState(program.spend_per_point || '100');
  const [value, setValue] = useState(program.point_value || '1');
  const [expiry, setExpiry] = useState(
    program.expiry_months ? String(program.expiry_months) : '',
  );
  const [tiers, setTiers] = useState<Tier[]>(() =>
    program.tiers.length > 0
      ? program.tiers
      : [
          // A starting ladder, because a scheme with no tiers is a scheme with
          // no reason to come back. Every figure here is editable.
          { key: 'bronze', name: 'Bronze', min_spend: '0', discount_percent: '0' },
          { key: 'silver', name: 'Silver', min_spend: '5000', discount_percent: '5' },
          { key: 'gold', name: 'Gold', min_spend: '20000', discount_percent: '10' },
        ],
  );
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  function setTier(i: number, patch: Partial<Tier>) {
    setTiers((prev) => prev.map((tier, j) => (j === i ? { ...tier, ...patch } : tier)));
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    setSaved(false);
    try {
      await saveProgram(client, companyId, {
        is_active: active,
        spend_per_point: spend,
        point_value: value,
        expiry_months: expiry.trim() === '' ? null : Number(expiry),
        tiers: tiers.filter((x) => x.name.trim() !== ''),
      });
      setSaved(true);
      onSaved();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <section className="ds-panel">
        <div className="ds-panel__body">
          <dl className="crm__facts">
            <div>
              <dt>{t('crm.pointsOutstanding')}</dt>
              <dd>{program.points_outstanding}</dd>
            </div>
            <div>
              <dt>{t('crm.owedInPoints')}</dt>
              <dd className="num">
                {money(program.owed, { currency: program.currency })}
              </dd>
            </div>
          </dl>
          {/* What the shop owes, said plainly. Points are a liability from the
              moment they are earned, and an owner reading this screen should
              see the number rather than a percentage. */}
          <p className="ds-caption">{t('crm.owedWhy')}</p>
        </div>
      </section>

      <form
        className="ds-panel crm__form"
        onSubmit={(e) => void submit(e)}
        noValidate
      >
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('crm.program')}</h2>
          {!program.exists && (
            <p className="ds-caption">{t('crm.noProgramYet')}</p>
          )}
        </div>

        <div className="ds-panel__body">
          <FormError message={failure} />
          {saved && (
            <p className="ds-caption" role="status">
              {t('crm.programSaved')}
            </p>
          )}

          <label className="crm__check">
            <input
              type="checkbox"
              checked={active}
              onChange={(e) => setActive(e.target.checked)}
            />
            <span>{t('crm.programActive')}</span>
          </label>

          <div className="form__grid">
            <Field
              label={t('crm.spendPerPoint')}
              hint={t('crm.spendPerPointHint')}
              htmlFor="lp-spend"
              required
            >
              <TextInput
                id="lp-spend"
                value={spend}
                onChange={setSpend}
                inputMode="decimal"
              />
            </Field>
            <Field
              label={t('crm.pointValue')}
              hint={t('crm.pointValueHint')}
              htmlFor="lp-value"
              required
            >
              <TextInput
                id="lp-value"
                value={value}
                onChange={setValue}
                inputMode="decimal"
              />
            </Field>
            <Field
              label={t('crm.pointsExpireAfter')}
              hint={t('crm.pointsExpireAfterHint')}
              htmlFor="lp-expiry"
            >
              <TextInput
                id="lp-expiry"
                value={expiry}
                onChange={setExpiry}
                inputMode="numeric"
              />
            </Field>
          </div>

          {/* Said before somebody halves the point value and expects the
              outstanding balance to halve with it. */}
          <p className="ds-caption">{t('crm.rateChangeWhy')}</p>

          <h3 className="ds-h3">{t('crm.tiers')}</h3>
          <div className="ds-scroll-x">
            <table className="ds-table">
              <thead>
                <tr>
                  <th scope="col">{t('crm.tierName')}</th>
                  <th scope="col" className="num">
                    {t('crm.tierFrom')}
                  </th>
                  <th scope="col" className="num">
                    {t('crm.tierDiscount')}
                  </th>
                  <th scope="col">
                    <span className="ds-visually-hidden">
                      {t('common.actions')}
                    </span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {tiers.map((tier, i) => (
                  <tr key={i}>
                    <td>
                      <TextInput
                        id={`tier-name-${i}`}
                        value={tier.name}
                        onChange={(v) =>
                          setTier(i, {
                            name: v,
                            // The key follows the name unless somebody has
                            // typed one: a shop should not have to think about
                            // an identifier they will never see.
                            key: tier.key || v.toLowerCase().replace(/\s+/g, '_'),
                          })
                        }
                      />
                    </td>
                    <td className="num">
                      <TextInput
                        id={`tier-min-${i}`}
                        value={tier.min_spend}
                        onChange={(v) => setTier(i, { min_spend: v })}
                        inputMode="decimal"
                      />
                    </td>
                    <td className="num">
                      <TextInput
                        id={`tier-disc-${i}`}
                        value={tier.discount_percent ?? ''}
                        onChange={(v) => setTier(i, { discount_percent: v })}
                        inputMode="decimal"
                      />
                    </td>
                    <td>
                      <button
                        type="button"
                        className="ds-btn ds-btn--quiet"
                        onClick={() =>
                          setTiers((prev) => prev.filter((_, j) => j !== i))
                        }
                      >
                        {t('action.remove')}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <button
            type="button"
            className="ds-btn ds-btn--quiet"
            onClick={() =>
              setTiers((prev) => [
                ...prev,
                { key: '', name: '', min_spend: '0', discount_percent: '0' },
              ])
            }
          >
            {t('crm.addTier')}
          </button>

          <FormActions
            submitLabel={t('action.saveChanges')}
            busy={busy}
            onCancel={onSaved}
          />
        </div>
      </form>
    </>
  );
}
