// E-invoicing units.
//
// The screen a shop owner opens for one reason: a till has refused a sale and
// said to come here. So the list leads with how many terminals each unit
// carries and what is still outstanding on it, rather than with certificate
// fields nobody can act on yet.
//
// Everything reuses what already exists — the DetailScreen frame, useRemote and
// its five states, the design system's tables and badges. The arrangement is
// the Terminals screen's, because it is the same journey a step earlier.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { listDeviceStores, type DeviceStore } from '../api/devices';
import { listEgsUnits, type EgsUnit } from '../api/egs';
import { architectureName, describeUnit, missingCsrFields } from './egs';
import { EgsUnitForm } from './EgsUnitForm';
import { ZatcaOnboarding } from './ZatcaOnboarding';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';

interface Loaded {
  units: EgsUnit[];
  stores: DeviceStore[];
}

export function EgsUnitsScreen({ companyId }: { companyId: string }) {
  const t = useT();
  const { client, can } = useAuth();

  const [editing, setEditing] = useState<EgsUnit | null>(null);
  const [creating, setCreating] = useState(false);
  const [connecting, setConnecting] = useState<EgsUnit | null>(null);

  const mayManage = can('einvoicing.manage');

  // Both together: the form cannot ask which branch a unit is in without the
  // branch list, and fetching them apart would let it open against a stale one.
  const load = useCallback(async (): Promise<Loaded> => {
    const [units, stores] = await Promise.all([
      listEgsUnits(client, companyId),
      listDeviceStores(client, companyId),
    ]);
    return { units, stores };
  }, [client, companyId]);
  const { remote, reload } = useRemote(load);

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('egs.einvoicing')}</h1>
          <p className="ds-caption">
            {t('egs.unitsExplain')}
          </p>
        </div>
        {mayManage && !creating && !editing && (
          <div className="detail__actions">
            <button className="ds-btn ds-btn--primary" onClick={() => setCreating(true)}>
              {t('egs.addUnit')}
            </button>
          </div>
        )}
      </header>

      <RemoteBody remote={remote} onRetry={reload}>
        {(loaded: Loaded) => {
          if (connecting) {
            return (
              <div className="ds-panel">
                <div className="ds-panel__body">
                  <button
                    className="ds-btn ds-btn--quiet"
                    onClick={() => setConnecting(null)}
                  >
                    {t('action.back')}
                  </button>
                  <ZatcaOnboarding unit={connecting} />
                </div>
              </div>
            );
          }

          if (creating || editing) {
            const done = () => {
              setCreating(false);
              setEditing(null);
            };
            return (
              <EgsUnitForm
                companyId={companyId}
                stores={loaded.stores}
                existing={editing ?? undefined}
                onSaved={() => {
                  done();
                  reload();
                }}
                onCancel={done}
              />
            );
          }

          return (
            <div className="ds-panel">
              <div className="ds-panel__body ds-scroll-x">
                {loaded.units.length === 0 ? (
                  <EmptyState
                    title={t('egs.noUnits')}
                    body={t('egs.whatIsAUnit')}
                  />
                ) : (
                  <table className="ds-table">
                    <thead>
                      <tr>
                        <th scope="col">{t('common.unit')}</th>
                        <th scope="col">{t('common.branch')}</th>
                        <th scope="col">ZATCA</th>
                        <th scope="col" className="num">
                          {t('dev.terminals')}
                        </th>
                        <th scope="col" className="num">
                          {t('egs.invoices')}
                        </th>
                        {mayManage && <th scope="col" />}
                      </tr>
                    </thead>
                    <tbody>
                      {loaded.units.map((u) => (
                        <UnitRow
                          key={u.id}
                          unit={u}
                          mayManage={mayManage}
                          onEdit={() => setEditing(u)}
                          onConnect={() => setConnecting(u)}
                        />
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
            </div>
          );
        }}
      </RemoteBody>
    </main>
  );
}

function UnitRow({
  unit,
  mayManage,
  onEdit,
  onConnect,
}: {
  unit: EgsUnit;
  mayManage: boolean;
  onEdit: () => void;
  onConnect: () => void;
}) {
  const t = useT();
  const state = describeUnit(unit);
  const missing = missingCsrFields(unit.csr);

  return (
    <tr>
      <td>
        <span className="detail__strong">{unit.label}</span>
        <span className="ds-caption">{t(architectureName(unit.architecture) as Key)}</span>
      </td>
      <td>{unit.store || <span className="ds-subtle">{t('egs.wholeBusiness')}</span>}</td>
      <td>
        <span className={`ds-badge ds-badge--${state.tone}`}>{t(state.label)}</span>
        {state.next && <span className="ds-caption">{t(state.next)}</span>}
        {/* Named rather than counted. "Three fields missing" makes somebody
            open the form to find out which three. */}
        {missing.length > 0 && (
          <span className="ds-caption">
            {t('egs.stillNeeded')}: {missing.map((k) => t(k)).join(', ')}.
          </span>
        )}
      </td>
      <td className="num">{unit.terminals}</td>
      <td className="num">{unit.invoices}</td>
      {mayManage && (
        <td>
          <div className="supplier__actions">
            <button className="ds-btn ds-btn--quiet" onClick={onConnect}>
              {t('zatca.connectAction')}
            </button>
            <button className="ds-btn ds-btn--quiet" onClick={onEdit}>
              {t('action.edit')}
            </button>
          </div>
        </td>
      )}
    </tr>
  );
}
