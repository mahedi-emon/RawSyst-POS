// Where stock can be.
//
// The screen `sales.resolveWarehouse` has been pointing at since it was
// written: "An owner can add one under Settings > Stock locations." Until
// migration 0078 there was no such screen, no route behind it, and nothing in
// the product had ever created a stock location at all — so a fully onboarded
// shop was refused on its first sale and sent to a page that did not exist.
//
// # Retiring is refused rather than hidden
//
// A location holding stock cannot be retired, and the reason is on the row
// before anybody presses: hiding a room does not empty it. The movements stay,
// C13's valuation keeps counting them, and the shop is left with stock the
// screens no longer show.

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { Field, FormActions, FormError, TextInput } from '../ui/Form';
import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import {
  createStockLocation,
  listStockLocations,
  renameStockLocation,
  setStockLocationActive,
  type Branch,
  type Places,
  type StockLocation,
} from '../api/stock';

export function LocationsPanel({
  companyId,
  onChanged,
}: {
  companyId: string;
  onChanged: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const mayManage = can('inventory.adjust_stock');

  const load = useCallback(
    () => listStockLocations(client, companyId, true),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const [adding, setAdding] = useState(false);

  const refresh = () => {
    setAdding(false);
    reload();
    onChanged();
  };

  return (
    <>
      {adding && (
        <LocationForm
          companyId={companyId}
          branches={
            remote.state === 'ready' ? remote.data.branches : []
          }
          onCancel={() => setAdding(false)}
          onSaved={refresh}
        />
      )}

      <section className="ds-panel" aria-label={t('stock.locations')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('stock.locations')}</h2>
          {mayManage && !adding && (
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => setAdding(true)}
            >
              {t('stock.addLocation')}
            </button>
          )}
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: Places) =>
            payload.data.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('stock.noLocationsTitle')}
                  body={t('stock.noLocationsBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('stock.location')}</th>
                      <th scope="col">{t('stock.locationKind')}</th>
                      <th scope="col">{t('stock.branch')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {payload.data.map((l) => (
                      <LocationRow
                        key={l.id}
                        companyId={companyId}
                        location={l}
                        mayManage={mayManage}
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

function LocationRow({
  companyId,
  location,
  mayManage,
  onChanged,
}: {
  companyId: string;
  location: StockLocation;
  mayManage: boolean;
  onChanged: () => void;
}) {
  const { client } = useAuth();
  const t = useT();
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState(location.name);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function rename() {
    setBusy(true);
    setFailure(null);
    try {
      await renameStockLocation(client, companyId, location.id, name);
      setRenaming(false);
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  async function toggle() {
    setBusy(true);
    setFailure(null);
    try {
      await setStockLocationActive(
        client,
        companyId,
        location.id,
        !location.is_active,
      );
      onChanged();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <tr className={location.is_active ? undefined : 'detail__row--aside'}>
      <td>
        {renaming ? (
          <TextInput id={`loc-${location.id}`} value={name} onChange={setName} />
        ) : (
          <span className="detail__strong">{location.name}</span>
        )}
        <span className="ds-caption">{location.code}</span>
        {failure && (
          <span className="form__error" role="alert">
            {failure}
          </span>
        )}
      </td>
      <td>{t(`stock.locationKind.${location.kind}` as Key)}</td>
      <td>{location.store || t('stock.everyBranch')}</td>
      <td>
        <span
          className={`ds-badge ds-badge--${location.is_active ? 'success' : 'muted'}`}
        >
          {t(location.is_active ? 'stock.inUse' : 'stock.retired')}
        </span>
        {location.holds_stock && (
          <span className="ds-caption">{t('stock.holdsStock')}</span>
        )}
      </td>
      <td>
        {mayManage && (
          <div className="stock__rowactions">
            {renaming ? (
              <>
                <button
                  className="ds-btn ds-btn--primary"
                  disabled={busy}
                  onClick={() => void rename()}
                >
                  {t('action.save')}
                </button>
                <button
                  className="ds-btn ds-btn--quiet"
                  onClick={() => {
                    setName(location.name);
                    setRenaming(false);
                  }}
                >
                  {t('action.cancel')}
                </button>
              </>
            ) : (
              <>
                <button
                  className="ds-btn ds-btn--quiet"
                  onClick={() => setRenaming(true)}
                >
                  {t('action.rename')}
                </button>
                {/* Offered even when the location holds stock, because the
                    refusal explains WHY — "move it somewhere else first" — and
                    a button that is simply absent explains nothing. */}
                <button
                  className="ds-btn ds-btn--quiet"
                  disabled={busy}
                  onClick={() => void toggle()}
                >
                  {t(location.is_active ? 'stock.retire' : 'stock.bringBack')}
                </button>
              </>
            )}
          </div>
        )}
      </td>
    </tr>
  );
}

function LocationForm({
  companyId,
  branches,
  onCancel,
  onSaved,
}: {
  companyId: string;
  branches: Branch[];
  onCancel: () => void;
  onSaved: () => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [code, setCode] = useState('');
  const [name, setName] = useState('');
  const [kind, setKind] = useState<'store_room' | 'central' | 'shop_floor'>(
    'store_room',
  );
  const [storeId, setStoreId] = useState(branches[0]?.id ?? '');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      await createStockLocation(client, companyId, {
        code,
        name,
        kind,
        // A central warehouse serves every branch, which is the whole of what
        // makes it central. Sending a branch with one is refused.
        store_id: kind === 'central' ? undefined : storeId,
      });
      onSaved();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ds-panel stock__form" onSubmit={(e) => void submit(e)} noValidate>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('stock.addLocation')}</h2>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />

        <div className="form__grid">
          <Field
            label={t('stock.locationCode')}
            hint={t('stock.locationCodeHint')}
            htmlFor="loc-code"
            required
          >
            <TextInput id="loc-code" value={code} onChange={setCode} />
          </Field>

          <Field label={t('stock.locationName')} htmlFor="loc-name" required>
            <TextInput id="loc-name" value={name} onChange={setName} />
          </Field>

          <Field label={t('stock.locationKind')} htmlFor="loc-kind" required>
            <select
              id="loc-kind"
              className="input"
              value={kind}
              onChange={(e) =>
                setKind(e.target.value as 'store_room' | 'central' | 'shop_floor')
              }
            >
              <option value="store_room">
                {t('stock.locationKind.store_room')}
              </option>
              <option value="shop_floor">
                {t('stock.locationKind.shop_floor')}
              </option>
              <option value="central">{t('stock.locationKind.central')}</option>
            </select>
          </Field>

          {kind !== 'central' && (
            <Field label={t('stock.branch')} htmlFor="loc-store" required>
              <select
                id="loc-store"
                className="input"
                value={storeId}
                onChange={(e) => setStoreId(e.target.value)}
              >
                {branches.map((b) => (
                  <option key={b.id} value={b.id}>
                    {b.name}
                  </option>
                ))}
              </select>
            </Field>
          )}
        </div>

        <FormActions
          submitLabel={t('stock.addLocation')}
          busy={busy}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
