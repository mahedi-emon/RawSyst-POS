// Ending a terminal.
//
// The only irreversible action in this module, so it is the only one that
// interrupts. 01-invoice-zatca-engine.md §7 pairs revocation with destroying
// the terminal's local CSID key on next start; there is no route back.
//
// Two things make it hard to do by accident and easy to do on purpose. The
// terminal's name has to be typed — a confirm button alone is pressed by
// muscle memory, a name is not — and the reason is required, because a
// revocation nobody can explain a month later is one somebody will undo.
//
// It also says plainly what revoking does NOT do. A shop owner deciding whether
// to revoke a stolen till needs to know the day's sales are safe, and nothing
// else on the screen tells them.

import { useState } from 'react';

import { Offline, RequestFailed } from '../api/client';
import { revokeTerminal, type Terminal } from '../api/devices';
import { useAuth } from '../auth/session';
import { useT } from '../i18n/locale';

export function RevokeDialog({
  companyId,
  terminal,
  onRevoked,
  onCancel,
}: {
  companyId: string;
  terminal: Terminal;
  onRevoked: (t: Terminal) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { client } = useAuth();

  const [typed, setTyped] = useState('');
  const [reason, setReason] = useState('');
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const nameMatches =
    typed.trim().toLowerCase() === terminal.terminal_label.trim().toLowerCase();
  const ready = nameMatches && reason.trim().length > 0 && !busy;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!ready) return;

    setBusy(true);
    setFailure(null);
    try {
      onRevoked(await revokeTerminal(client, companyId, terminal.id, reason.trim()));
    } catch (err) {
      if (err instanceof Offline) {
        setFailure(
          t('revoke.offline') +
            t('common.tryWhenBack'),
        );
      } else if (err instanceof RequestFailed) {
        setFailure(err.message);
      } else {
        setFailure(err instanceof Error ? err.message : 'That did not work.');
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      className="dialog__backdrop"
      role="presentation"
      onClick={(e) => {
        if (e.target === e.currentTarget) onCancel();
      }}
    >
      <form
        className="dialog dialog--danger"
        role="dialog"
        aria-modal="true"
        aria-labelledby="revoke-title"
        onSubmit={(e) => void submit(e)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') onCancel();
        }}
        noValidate
      >
        <h2 className="ds-h3" id="revoke-title">
          Revoke {terminal.terminal_label}?
        </h2>

        <p className="dialog__body">{t('revoke.stopsImmediately')}</p>
        <p className="dialog__body ds-muted">{t('revoke.tradingKept')}</p>

        <label className="field__label" htmlFor="revoke-reason">
          {t('dev.whyRevoking')}
        </label>
        <p className="field__hint" id="revoke-reason-hint">
          {t('dev.revokeNote')}
        </p>
        <input
          id="revoke-reason"
          className="input"
          value={reason}
          placeholder={t('dev.stolenExample')}
          aria-describedby="revoke-reason-hint"
          onChange={(e) => setReason(e.target.value)}
        />

        <label className="field__label" htmlFor="revoke-name">
          {t('common.type')} <strong>{terminal.terminal_label}</strong> to confirm
        </label>
        <input
          id="revoke-name"
          className="input"
          value={typed}
          autoComplete="off"
          onChange={(e) => setTyped(e.target.value)}
        />

        {failure && (
          <p className="form__error" role="alert">
            {failure}
          </p>
        )}

        <div className="form__actions">
          {/* Destructive, and styled as such. Disabled until both the name and
              a reason are given, so it cannot be reached by pressing Enter
              through the form. */}
          <button
            className="ds-btn ds-btn--danger"
            type="submit"
            disabled={!ready}
          >
            {busy ? t('revoke.revoking') : t('revoke.revokeTerminal')}
          </button>
          <button className="ds-btn ds-btn--quiet" type="button" onClick={onCancel}>
            {t('action.cancel')}
          </button>
        </div>
      </form>
    </div>
  );
}
