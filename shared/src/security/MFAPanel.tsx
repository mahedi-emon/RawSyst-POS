// Turning the second factor on and off (blueprint H1).
//
// # Enrolment is two steps and the screen shows both
//
// Scan, then type a code the app produces. The second step is not a formality:
// it is the only proof the phone holds the same secret the server does, and a
// screen that switched the factor on at step one would lock somebody out of
// their own account the moment a scan silently failed.
//
// # The recovery codes are shown once and the screen says so
//
// The server has no route that would show them again, so this panel refuses to
// leave the codes screen until somebody has ticked that they wrote them down.
// A polite "you can find these later in settings" would be a lie.
//
// # The QR is drawn here, not fetched
//
// A QR of the enrolment secret is the secret. Sending it to an image service to
// be rendered would hand a third party the second factor for every account that
// ever enrolled, and there is no version of that which is acceptable.

import { useCallback, useState } from 'react';

import {
  beginMFA,
  completeMFA,
  disableMFA,
  mfaStatus,
  regenerateRecoveryCodes,
  type MFAEnrolment,
  type MFAStatus,
} from '../api/security';
import { useAuth } from '../auth/session';
import { RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { LabelledText } from '../governance/fields';
import { useLocale, useT } from '../i18n/locale';
import { FormError } from '../ui/Form';
import { shortDate } from '../ui/format';
import { QRCode } from './QRCode';

export function MFAPanel() {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const load = useCallback(() => mfaStatus(client), [client]);
  const { remote, reload } = useRemote(load);

  const [enrolment, setEnrolment] = useState<MFAEnrolment | null>(null);
  const [code, setCode] = useState('');
  const [codes, setCodes] = useState<string[] | null>(null);
  const [written, setWritten] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function run(work: () => Promise<unknown>) {
    setBusy(true);
    setFailure(null);
    try {
      await work();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  // The codes screen. Shown after enrolment and after a regeneration, and it
  // holds the panel until somebody says they have them.
  if (codes) {
    return (
      <section className="ds-panel" aria-label={t('sec.recoveryCodes')}>
        <div className="ds-panel__head">
          <div>
            <h2 className="ds-h3">{t('sec.recoveryCodes')}</h2>
            <p className="ds-caption">{t('sec.recoveryHint')}</p>
          </div>
        </div>
        <div className="ds-panel__body">
          <p className="sec__warning" role="status">
            {t('sec.shownOnce')}
          </p>
          <ul className="sec__codes">
            {codes.map((c) => (
              <li className="sec__code num" key={c}>
                {c}
              </li>
            ))}
          </ul>
          <label className="ds-check">
            <input
              type="checkbox"
              checked={written}
              onChange={(e) => setWritten(e.target.checked)}
            />
            {t('sec.iWroteThemDown')}
          </label>
          <div className="form__actions">
            <button
              className="ds-btn ds-btn--primary"
              disabled={!written}
              onClick={() => {
                setCodes(null);
                setWritten(false);
                setEnrolment(null);
                setCode('');
                reload();
              }}
            >
              {t('action.done')}
            </button>
          </div>
        </div>
      </section>
    );
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(payload: { mfa: MFAStatus }) => {
        const mfa = payload.mfa;
        return (
          <section className="ds-panel" aria-label={t('sec.factor')}>
            <div className="ds-panel__head">
              <div>
                <h2 className="ds-h3">{t('sec.factor')}</h2>
                <p className="ds-caption">{t('sec.factorHint')}</p>
              </div>
              <span
                className={`ds-badge ds-badge--${mfa.enabled ? 'success' : 'neutral'}`}
              >
                {mfa.enabled ? t('sec.on') : t('sec.off')}
              </span>
            </div>

            <div className="ds-panel__body">
              <FormError message={failure} />

              {mfa.enabled ? (
                <>
                  <p className="ds-caption">
                    {mfa.enrolled_at
                      ? t('sec.onSince', {
                          when: shortDate(mfa.enrolled_at, locale),
                        })
                      : ''}
                  </p>
                  {mfa.recovery_remaining <= 3 && (
                    <p className="sec__warning" role="status">
                      {t('sec.fewCodesLeft', {
                        n: String(mfa.recovery_remaining),
                      })}
                    </p>
                  )}
                  <p className="ds-caption">
                    {t('sec.codesLeft', {
                      n: String(mfa.recovery_remaining),
                    })}
                  </p>

                  <div className="pri__form">
                    <LabelledText
                      id="mfa-code"
                      label={t('sec.currentCode')}
                      hint={t('sec.currentCodeHint')}
                      value={code}
                      onChange={setCode}
                      inputMode="numeric"
                    />
                    <div className="form__actions">
                      <button
                        className="ds-btn ds-btn--quiet"
                        disabled={busy || code.trim() === ''}
                        onClick={() =>
                          void run(async () => {
                            const out = await regenerateRecoveryCodes(
                              client,
                              code,
                            );
                            setCodes(out.recovery_codes);
                            setCode('');
                          })
                        }
                      >
                        {t('sec.newRecoveryCodes')}
                      </button>
                      <button
                        className="ds-btn ds-btn--quiet"
                        disabled={busy || code.trim() === ''}
                        onClick={() =>
                          void run(async () => {
                            await disableMFA(client, code);
                            setCode('');
                            reload();
                          })
                        }
                      >
                        {t('sec.turnOff')}
                      </button>
                    </div>
                  </div>
                </>
              ) : enrolment ? (
                <div className="sec__enrol">
                  <p className="ds-caption">{t('sec.scanThis')}</p>
                  <QRCode value={enrolment.uri} />
                  <p className="ds-caption">{t('sec.orTypeIt')}</p>
                  <p className="sec__secret num">{enrolment.secret}</p>

                  <div className="pri__form">
                    <LabelledText
                      id="mfa-confirm"
                      label={t('sec.codeFromApp')}
                      hint={t('sec.codeFromAppHint')}
                      value={code}
                      onChange={setCode}
                      inputMode="numeric"
                    />
                    <div className="form__actions">
                      <button
                        className="ds-btn ds-btn--primary"
                        disabled={busy || code.trim().length !== 6}
                        onClick={() =>
                          void run(async () => {
                            const out = await completeMFA(client, code);
                            setCodes(out.recovery_codes);
                          })
                        }
                      >
                        {t('sec.turnOn')}
                      </button>
                      <button
                        className="ds-btn ds-btn--quiet"
                        onClick={() => {
                          setEnrolment(null);
                          setCode('');
                        }}
                      >
                        {t('action.cancel')}
                      </button>
                    </div>
                  </div>
                </div>
              ) : (
                <div className="form__actions">
                  <button
                    className="ds-btn ds-btn--primary"
                    disabled={busy}
                    onClick={() =>
                      void run(async () => {
                        const out = await beginMFA(client);
                        setEnrolment(out.enrolment);
                      })
                    }
                  >
                    {t('sec.setItUp')}
                  </button>
                </div>
              )}
            </div>
          </section>
        );
      }}
    </RemoteBody>
  );
}
