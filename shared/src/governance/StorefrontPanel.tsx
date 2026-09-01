// The disclosures Saudi e-commerce law requires an online shop to publish
// (blueprint E5), and the company's privacy posture (E4).
//
// # What is missing is named, not counted
//
// E5's penalties run to around a million riyals and the easiest violation in
// the list to commit is a missing return policy. So the panel opens with the
// specific fields that are not filled in, in the reader's own language, rather
// than a completeness percentage nobody can act on.
//
// # The Arabic copy is required and the English is not
//
// E5 says the disclosures must render in Arabic. A shop that has written its
// return policy only in English has not met the requirement even though the
// form looks full, which is why the missing list checks the Arabic fields.

import { useCallback, useEffect, useState } from 'react';

import {
  getDisclosure,
  getPrivacySettings,
  listSubprocessors,
  saveDisclosure,
  savePrivacySettings,
  type Disclosure,
  type PrivacySettings,
  type Subprocessor,
} from '../api/governance';
import { useAuth } from '../auth/session';
import { RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError } from '../ui/Form';
import { LabelledText } from './fields';

export function StorefrontPanel({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const mayManage = can('privacy.manage');

  const load = useCallback(
    () => getDisclosure(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [draft, setDraft] = useState<Disclosure | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  useEffect(() => {
    if (remote.state === 'ready') setDraft(remote.data.disclosure);
  }, [remote]);

  const set = (patch: Partial<Disclosure>) =>
    setDraft((d) => (d ? { ...d, ...patch } : d));

  async function save() {
    if (!draft) return;
    setBusy(true);
    setFailure(null);
    try {
      await saveDisclosure(client, companyId, draft);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { disclosure: Disclosure }) => {
          const missing = payload.disclosure.missing;
          return (
            <section className="ds-panel" aria-label={t('sto.title')}>
              <div className="ds-panel__head">
                <div>
                  <h2 className="ds-h3">{t('sto.title')}</h2>
                  <p className="ds-caption">{t('sto.intro')}</p>
                </div>
                {mayManage && (
                  <button
                    className="ds-btn ds-btn--primary"
                    disabled={busy}
                    onClick={() => void save()}
                  >
                    {t('action.save')}
                  </button>
                )}
              </div>

              <div className="ds-panel__body">
                <FormError message={failure} />

                {missing.length > 0 && (
                  <div className="sto__missing" role="status">
                    <p className="ds-caption">{t('sto.missingIntro')}</p>
                    <ul className="sto__list">
                      {missing.map((m) => (
                        <li key={m}>{t(`cmp.disc.${m}` as Key)}</li>
                      ))}
                    </ul>
                  </div>
                )}

                {draft && (
                  <div className="pri__form">
                    <LabelledText
                      id="sf-reg"
                      label={t('sto.registrationRef')}
                      hint={t('sto.registrationHint')}
                      value={draft.registration_ref ?? ''}
                      onChange={(v) => set({ registration_ref: v })}
                    />
                    <LabelledText
                      id="sf-channel"
                      label={t('sto.registrationChannel')}
                      value={draft.registration_channel ?? ''}
                      onChange={(v) => set({ registration_channel: v })}
                    />
                    <LabelledText
                      id="sf-ret-ar"
                      label={t('sto.returnPolicyAr')}
                      hint={t('sto.arabicRequired')}
                      value={draft.return_policy_ar ?? ''}
                      onChange={(v) => set({ return_policy_ar: v })}
                    />
                    <LabelledText
                      id="sf-ret"
                      label={t('sto.returnPolicy')}
                      value={draft.return_policy ?? ''}
                      onChange={(v) => set({ return_policy: v })}
                    />
                    <LabelledText
                      id="sf-del-ar"
                      label={t('sto.deliveryTermsAr')}
                      hint={t('sto.arabicRequired')}
                      value={draft.delivery_terms_ar ?? ''}
                      onChange={(v) => set({ delivery_terms_ar: v })}
                    />
                    <LabelledText
                      id="sf-del"
                      label={t('sto.deliveryTerms')}
                      value={draft.delivery_terms ?? ''}
                      onChange={(v) => set({ delivery_terms: v })}
                    />
                    <LabelledText
                      id="sf-phone"
                      label={t('sto.contactPhone')}
                      value={draft.contact_phone ?? ''}
                      onChange={(v) => set({ contact_phone: v })}
                      inputMode="tel"
                    />
                    <LabelledText
                      id="sf-email"
                      label={t('sto.contactEmail')}
                      value={draft.contact_email ?? ''}
                      onChange={(v) => set({ contact_email: v })}
                      inputMode="email"
                    />
                    <LabelledText
                      id="sf-hours"
                      label={t('sto.supportHours')}
                      value={draft.support_hours ?? ''}
                      onChange={(v) => set({ support_hours: v })}
                    />
                    <LabelledText
                      id="sf-cool"
                      label={t('sto.coolingOff')}
                      hint={t('sto.coolingOffHint')}
                      value={
                        draft.cooling_off_days === undefined
                          ? ''
                          : String(draft.cooling_off_days)
                      }
                      onChange={(v) =>
                        set({
                          cooling_off_days:
                            v.trim() === '' ? undefined : Number(v),
                        })
                      }
                      inputMode="numeric"
                    />

                    {/* Read-only. Both live on the company record, and a second
                        copy would be a second thing to keep true. */}
                    <dl className="sto__facts">
                      <div className="sto__fact">
                        <dt>{t('sto.crNumber')}</dt>
                        <dd>{payload.disclosure.cr_number || '—'}</dd>
                      </div>
                      <div className="sto__fact">
                        <dt>{t('cmp.vatNumber')}</dt>
                        <dd>{payload.disclosure.vat_number || '—'}</dd>
                      </div>
                    </dl>
                  </div>
                )}
              </div>
            </section>
          );
        }}
      </RemoteBody>

      <PrivacyPosture companyId={companyId} />
      <Subprocessors companyId={companyId} />
    </>
  );
}

/** The Data Protection Officer and the controller registration (E4.1, E4.2). */
function PrivacyPosture({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const mayManage = can('privacy.manage');

  const load = useCallback(
    () => getPrivacySettings(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [draft, setDraft] = useState<PrivacySettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  useEffect(() => {
    if (remote.state === 'ready') setDraft(remote.data.settings);
  }, [remote]);

  const set = (patch: Partial<PrivacySettings>) =>
    setDraft((d) => (d ? { ...d, ...patch } : d));

  async function save() {
    if (!draft) return;
    setBusy(true);
    setFailure(null);
    try {
      await savePrivacySettings(client, companyId, draft);
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('sto.posture')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('sto.posture')}</h2>
          <p className="ds-caption">{t('sto.postureHint')}</p>
        </div>
        {mayManage && (
          <button
            className="ds-btn ds-btn--primary"
            disabled={busy}
            onClick={() => void save()}
          >
            {t('action.save')}
          </button>
        )}
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />
        {draft && (
          <div className="pri__form">
            <LabelledText
              id="pp-dpo"
              label={t('sto.dpoName')}
              value={draft.dpo_name ?? ''}
              onChange={(v) => set({ dpo_name: v })}
            />
            <LabelledText
              id="pp-dpo-email"
              label={t('sto.dpoEmail')}
              value={draft.dpo_email ?? ''}
              onChange={(v) => set({ dpo_email: v })}
              inputMode="email"
            />
            <LabelledText
              id="pp-dpo-phone"
              label={t('sto.dpoPhone')}
              value={draft.dpo_phone ?? ''}
              onChange={(v) => set({ dpo_phone: v })}
              inputMode="tel"
            />
            <label className="ds-check">
              <input
                type="checkbox"
                checked={draft.dpo_external}
                onChange={(e) => set({ dpo_external: e.target.checked })}
              />
              {t('sto.dpoExternal')}
            </label>
            <LabelledText
              id="pp-sdaia"
              label={t('sto.registrationReference')}
              hint={t('sto.registrationReferenceHint')}
              value={draft.sdaia_registration_ref ?? ''}
              onChange={(v) => set({ sdaia_registration_ref: v })}
            />
            <LabelledText
              id="pp-notice"
              label={t('sto.privacyNotice')}
              value={draft.privacy_notice_url ?? ''}
              onChange={(v) => set({ privacy_notice_url: v })}
            />
            <dl className="sto__facts">
              <div className="sto__fact">
                <dt>{t('sto.dataRegion')}</dt>
                <dd>{t(`sto.region.${draft.data_region}` as Key)}</dd>
              </div>
            </dl>
          </div>
        )}
      </div>
    </section>
  );
}

/** Who else touches the data. The platform's disclosure about itself. */
function Subprocessors({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();

  const load = useCallback(
    () => listSubprocessors(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  return (
    <section className="ds-panel" aria-label={t('sto.subprocessors')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('sto.subprocessors')}</h2>
          <p className="ds-caption">{t('sto.subprocessorsHint')}</p>
        </div>
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: Subprocessor[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <p className="ds-caption">{t('sto.noSubprocessors')}</p>
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('sto.vendor')}</th>
                    <th scope="col">{t('reg.purpose')}</th>
                    <th scope="col">{t('sto.country')}</th>
                    <th scope="col">{t('reg.dataCategories')}</th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((p) => (
                    <tr key={p.id}>
                      <td>{p.name}</td>
                      <td>{p.purpose}</td>
                      <td>{p.country.toUpperCase()}</td>
                      <td>{p.data_categories}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        }
      </RemoteBody>
    </section>
  );
}
