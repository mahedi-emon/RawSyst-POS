// Terminals, blueprint H3.
//
// The screen a shop owner opens for one of three reasons: a new till has
// arrived, an existing one is behaving oddly, or one has gone missing. So the
// list is ordered by how much attention a terminal needs rather than by name,
// and every row says what to do next rather than only what state it is in.
//
// Everything here reuses what already exists: the DetailScreen frame, the
// useRemote hook and its five states, the design system's tables, badges and
// buttons. Nothing new was invented for this module.

import { useCallback, useState } from 'react';

import { Offline } from '../api/client';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { shortDate } from '../ui/format';
import {
  issueEnrolmentCode,
  listDeviceStores,
  listTerminals,
  setTerminalActive,
  type DeviceStore,
  type IssuedCode,
  type Terminal,
} from '../api/devices';
import { listEgsUnits, type EgsUnit } from '../api/egs';
import { sellingBlocked } from '../einvoicing/egs';
import { describeState, offered } from './devices';
import { TerminalForm } from './TerminalForm';
import { EnrolmentCode } from './EnrolmentCode';
import { RevokeDialog } from './RevokeDialog';
import { useT } from '../i18n/locale';

interface Loaded {
  terminals: Terminal[];
  stores: DeviceStore[];
  units: EgsUnit[];
}

export function DevicesScreen({ companyId }: { companyId: string }) {
  const t = useT();
  const { client, can } = useAuth();

  const [editing, setEditing] = useState<Terminal | null>(null);
  const [creating, setCreating] = useState(false);
  const [issued, setIssued] = useState<IssuedCode | null>(null);
  const [revoking, setRevoking] = useState<Terminal | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const mayManage = can('devices.manage');

  // The list, the branches and the signing units together: the register form
  // cannot be shown without somewhere to put the terminal and something for it
  // to sign under, and fetching them apart would let the form open against a
  // stale list of either.
  const load = useCallback(async (): Promise<Loaded> => {
    const [terminals, stores, units] = await Promise.all([
      listTerminals(client, companyId),
      listDeviceStores(client, companyId),
      listEgsUnits(client, companyId),
    ]);
    return { terminals, stores, units };
  }, [client, companyId]);
  const { remote, reload } = useRemote(load);

  async function getCode(deviceId: string) {
    setBusy(deviceId);
    setNotice(null);
    try {
      setIssued(await issueEnrolmentCode(client, companyId, deviceId));
      reload();
    } catch (err) {
      setNotice(
        err instanceof Offline
          ? 'This device cannot reach the server, so no code was made. Try again when the connection is back.'
          : err instanceof Error
            ? err.message
            : 'That did not work.',
      );
    } finally {
      setBusy(null);
    }
  }

  async function setActive(deviceId: string, active: boolean) {
    setBusy(deviceId);
    setNotice(null);
    try {
      await setTerminalActive(client, companyId, deviceId, active);
      reload();
    } catch (err) {
      // The server refuses to switch on a terminal that was never paired, and
      // says why. Shown as-is: it says what to do.
      setNotice(err instanceof Error ? err.message : 'That did not work.');
    } finally {
      setBusy(null);
    }
  }

  if (issued) {
    return (
      <FormPage title={t('dev.terminals')} onBack={() => setIssued(null)}>
        <EnrolmentCode
          issued={issued}
          busy={busy !== null}
          onReissue={() => void getCode(issued.device_id)}
          onDone={() => {
            setIssued(null);
            reload();
          }}
        />
      </FormPage>
    );
  }

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('dev.terminals')}</h1>
          <p className="ds-caption">{t('dev.tillsThatSell')}</p>
        </div>
        {mayManage && !creating && !editing && (
          <div className="detail__actions">
            <button className="ds-btn ds-btn--primary" onClick={() => setCreating(true)}>
              {t('dev.addTerminal')}
            </button>
          </div>
        )}
      </header>

      <RemoteBody remote={remote} onRetry={reload}>
        {(loaded: Loaded) => {
          if (creating || editing) {
            const done = () => {
              setCreating(false);
              setEditing(null);
            };
            const wasCreating = creating;
            return (
              <TerminalForm
                companyId={companyId}
                stores={loaded.stores}
                units={loaded.units}
                existing={editing ?? undefined}
                onSaved={(t) => {
                  done();
                  reload();
                  // Straight to a code for a brand-new terminal, because
                  // getting one is the only thing anybody does next.
                  if (wasCreating) void getCode(t.id);
                }}
                onCancel={done}
              />
            );
          }

          return (
            <>
              {notice && (
                <p className="ds-panel purchase__notice" role="alert">
                  {notice}
                </p>
              )}

              <div className="ds-panel">
                <div className="ds-panel__body ds-scroll-x">
                  {loaded.terminals.length === 0 ? (
                    <EmptyState
                      title={t('dev.noTerminals')}
                      body="A terminal is one till. Add one here, then type the code it gives you into the machine on your counter — that is what lets it ring up sales."
                    />
                  ) : (
                    <table className="ds-table">
                      <thead>
                        <tr>
                          <th scope="col">{t('dev.terminal')}</th>
                          <th scope="col">{t('common.branch')}</th>
                          <th scope="col">{t('dev.state')}</th>
                          <th scope="col">{t('dev.lastSeen')}</th>
                          {mayManage && <th scope="col" />}
                        </tr>
                      </thead>
                      <tbody>
                        {loaded.terminals.map((t) => (
                          <TerminalRow
                            key={t.id}
                            terminal={t}
                            mayManage={mayManage}
                            busy={busy === t.id}
                            onCode={() => void getCode(t.id)}
                            onEdit={() => setEditing(t)}
                            onSetActive={(a) => void setActive(t.id, a)}
                            onRevoke={() => setRevoking(t)}
                          />
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>
              </div>

              {revoking && (
                <RevokeDialog
                  companyId={companyId}
                  terminal={revoking}
                  onRevoked={() => {
                    setRevoking(null);
                    reload();
                  }}
                  onCancel={() => setRevoking(null)}
                />
              )}
            </>
          );
        }}
      </RemoteBody>
    </main>
  );
}

function TerminalRow({
  terminal,
  mayManage,
  busy,
  onCode,
  onEdit,
  onSetActive,
  onRevoke,
}: {
  terminal: Terminal;
  mayManage: boolean;
  busy: boolean;
  onCode: () => void;
  onEdit: () => void;
  onSetActive: (active: boolean) => void;
  onRevoke: () => void;
}) {
  const t = useT();
  const state = describeState(terminal);
  const controls = offered(terminal, mayManage);
  const gone = terminal.status === 'revoked';
  // Separate from the state sentence rather than folded into it. A terminal can
  // be waiting for a code AND unlinked, and a reader who fixes only the one
  // they were told about would find the till still refusing to sell.
  const cannotSell = sellingBlocked(terminal);

  return (
    <tr className={gone ? 'detail__row--aside' : undefined}>
      <td>
        <span className="detail__strong">{terminal.terminal_label}</span>
        <span className="ds-caption">
          {terminal.os || t('device.notSetUp')}
          {terminal.app_version ? ` · ${terminal.app_version}` : ''}
        </span>
      </td>
      <td>
        {terminal.store}
        {terminal.egs_unit && <span className="ds-caption">{terminal.egs_unit}</span>}
      </td>
      <td>
        <span className={`ds-badge ds-badge--${state.tone}`}>{t(state.label)}</span>
        {/* The sentence that says what to do. Present only when there IS
            something to do, so it never becomes noise a reader learns to skip. */}
        {state.next && <span className="ds-caption">{t(state.next)}</span>}
        {cannotSell && <span className="ds-caption">{cannotSell}</span>}
        {gone && terminal.revoked_reason && (
          <span className="ds-caption">{terminal.revoked_reason}</span>
        )}
      </td>
      <td className="num">
        {terminal.last_active_at ? (
          shortDate(terminal.last_active_at)
        ) : (
          <span className="ds-subtle">—</span>
        )}
      </td>
      {mayManage && (
        <td>
          <div className="supplier__actions">
            {controls.code && (
              <button className="ds-btn ds-btn--quiet" onClick={onCode} disabled={busy}>
                {busy ? t('common.working') : terminal.enrolled_at ? t('device.setUpAgain') : t('device.getCode')}
              </button>
            )}
            {controls.edit && (
              <button className="ds-btn ds-btn--quiet" onClick={onEdit}>
                {t('action.edit')}
              </button>
            )}
            {controls.pause && (
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => onSetActive(false)}
                disabled={busy}
              >
                {t('dev.switchOff')}
              </button>
            )}
            {controls.resume && (
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => onSetActive(true)}
                disabled={busy}
              >
                {t('dev.switchOn')}
              </button>
            )}
            {controls.revoke && (
              <button className="ds-btn ds-btn--quiet ds-btn--warn" onClick={onRevoke}>
                {t('dev.revoke')}
              </button>
            )}
          </div>
        </td>
      )}
    </tr>
  );
}

/** A form filling the page, with one click back. The same shape as the Buying
 *  and Customers screens, because it is the same journey. */
function FormPage({
  title,
  onBack,
  children,
}: {
  title: string;
  onBack: () => void;
  children: React.ReactNode;
}) {
  return (
    <main className="detail">
      <header className="detail__head">
        <button className="detail__back" onClick={onBack}>
          <span aria-hidden="true" className="detail__backarrow">
            ←
          </span>
          {title}
        </button>
      </header>
      {children}
    </main>
  );
}
