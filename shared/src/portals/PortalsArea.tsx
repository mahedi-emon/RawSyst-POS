// The staff side of the two portals (blueprint F2, F3).
//
// # Two lists a shop actually works from
//
// Customers asking to send something back, and the supplier contacts who can
// sign in. Everything else in the portals happens without staff: a customer
// reading their own receipts and a supplier accepting an order are the point of
// the feature, and a screen that showed them would be a screen nobody opens.
//
// # Answering a request is not posting a return
//
// Accepting tells the customer to bring the goods in. The return itself goes
// through the returns screen, where the money and the stock move — which is why
// this panel has no amounts on it at all.

import { useCallback, useState } from 'react';

import { listSuppliers, type Supplier } from '../api/purchasing';
import type { PortalReturnRequest } from '../api/portal';
import {
  decideReturnRequest,
  inviteSupplierContact,
  listReturnRequests,
  listSupplierContacts,
  revokeSupplierContact,
  type SupplierContact,
} from '../api/portalAdmin';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { LabelledSelect, LabelledText } from '../governance/fields';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { FormError } from '../ui/Form';
import { shortDate } from '../ui/format';

type Tab = 'returns' | 'contacts';

export function PortalsArea({ companyId }: { companyId: string }) {
  const t = useT();
  const [tab, setTab] = useState<Tab>('returns');

  const tabs: Array<{ key: Tab; label: Key }> = [
    { key: 'returns', label: 'ptl.returns' },
    { key: 'contacts', label: 'ptl.contacts' },
  ];

  return (
    <main className="detail">
      <header className="detail__head detail__head--flat">
        <div className="detail__titles">
          <h1 className="ds-h1">{t('ptl.title')}</h1>
          <p className="ds-caption">{t('ptl.intro')}</p>
        </div>
        <div className="detail__actions">
          <div
            className="segmented"
            role="group"
            aria-label={t('common.whatToShow')}
          >
            {tabs.map((x) => (
              <button
                key={x.key}
                className={`segmented__btn${tab === x.key ? ' segmented__btn--on' : ''}`}
                aria-pressed={tab === x.key}
                onClick={() => setTab(x.key)}
              >
                {t(x.label)}
              </button>
            ))}
          </div>
        </div>
      </header>

      {tab === 'returns' && <Returns companyId={companyId} />}
      {tab === 'contacts' && <Contacts companyId={companyId} />}
    </main>
  );
}

function Returns({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const mayManage = can('portal.manage');

  const [openOnly, setOpenOnly] = useState(true);
  const load = useCallback(
    () => listReturnRequests(client, companyId, openOnly),
    [client, companyId, openOnly],
  );
  const { remote, reload } = useRemote(load);

  const [refusing, setRefusing] = useState<PortalReturnRequest | null>(null);
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function run(work: () => Promise<unknown>) {
    setBusy(true);
    setFailure(null);
    try {
      await work();
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('ptl.returns')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('ptl.returns')}</h2>
          <p className="ds-caption">{t('ptl.returnsHint')}</p>
        </div>
        <label className="ds-check">
          <input
            type="checkbox"
            checked={openOnly}
            onChange={(e) => setOpenOnly(e.target.checked)}
          />
          {t('pri.openOnly')}
        </label>
      </div>

      <div className="ds-panel__body">
        <FormError message={failure} />
        {refusing && (
          <div className="pri__form">
            <p className="ds-caption">
              {t('ptl.refusingHint', { no: refusing.request_no })}
            </p>
            <LabelledText
              id="rr-note"
              label={t('pri.reasonForThem')}
              value={note}
              onChange={setNote}
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={busy || note.trim() === ''}
                onClick={() =>
                  void run(async () => {
                    await decideReturnRequest(client, companyId, refusing.id, {
                      accept: false,
                      note,
                    });
                    setRefusing(null);
                    setNote('');
                  })
                }
              >
                {t('ptl.refuse')}
              </button>
              <button
                className="ds-btn ds-btn--quiet"
                onClick={() => setRefusing(null)}
              >
                {t('action.cancel')}
              </button>
            </div>
          </div>
        )}
      </div>

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: PortalReturnRequest[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('ptl.noReturnsTitle')}
                body={t('ptl.noReturnsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('pri.reference')}</th>
                    <th scope="col">{t('ptl.customer')}</th>
                    <th scope="col">{t('ptl.what')}</th>
                    <th scope="col">{t('ptl.why')}</th>
                    <th scope="col">{t('common.status')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((r) => (
                    <tr key={r.id}>
                      <td>
                        {r.request_no}
                        <span className="ds-caption">
                          {' '}
                          {shortDate(r.created_at, locale)}
                        </span>
                      </td>
                      <td>{r.customer_name}</td>
                      <td>
                        {r.items}
                        {r.invoice_no && (
                          <span className="ds-caption"> {r.invoice_no}</span>
                        )}
                      </td>
                      <td>{r.reason}</td>
                      <td>
                        <span
                          className={`ds-badge ds-badge--${
                            r.status === 'requested'
                              ? 'warning'
                              : r.status === 'refused'
                                ? 'danger'
                                : 'success'
                          }`}
                        >
                          {t(`ptl.status.${r.status}` as Key)}
                        </span>
                        {r.decision_note && (
                          <span className="ds-caption"> {r.decision_note}</span>
                        )}
                      </td>
                      <td className="ds-table__actions">
                        {mayManage && r.status === 'requested' && (
                          <>
                            <button
                              className="ds-btn ds-btn--quiet ds-btn--sm"
                              disabled={busy}
                              onClick={() =>
                                void run(() =>
                                  decideReturnRequest(client, companyId, r.id, {
                                    accept: true,
                                  }),
                                )
                              }
                            >
                              {t('ptl.accept')}
                            </button>
                            <button
                              className="ds-btn ds-btn--quiet ds-btn--sm"
                              onClick={() => setRefusing(r)}
                            >
                              {t('ptl.refuse')}
                            </button>
                          </>
                        )}
                      </td>
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

function Contacts({ companyId }: { companyId: string }) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();
  const mayManage = can('portal.manage');

  const load = useCallback(
    () => listSupplierContacts(client, companyId),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  const supplierLoad = useCallback(
    () => listSuppliers(client, companyId),
    [client, companyId],
  );
  const suppliers = useRemote(supplierLoad);

  const [supplierId, setSupplierId] = useState('');
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const options: Supplier[] =
    suppliers.remote.state === 'ready' ? suppliers.remote.data : [];

  async function run(work: () => Promise<unknown>) {
    setBusy(true);
    setFailure(null);
    try {
      await work();
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="ds-panel" aria-label={t('ptl.contacts')}>
      <div className="ds-panel__head">
        <div>
          <h2 className="ds-h3">{t('ptl.contacts')}</h2>
          <p className="ds-caption">{t('ptl.contactsHint')}</p>
        </div>
      </div>

      {mayManage && (
        <div className="ds-panel__body">
          <FormError message={failure} />
          <div className="pri__form">
            <LabelledSelect
              id="pc-supplier"
              label={t('ptl.supplier')}
              value={supplierId}
              onChange={setSupplierId}
              options={[
                { value: '', label: t('ptl.chooseSupplier') },
                ...options.map((s) => ({
                  value: s.id,
                  label: s.legal_name,
                })),
              ]}
            />
            <LabelledText
              id="pc-name"
              label={t('ptl.contactName')}
              value={name}
              onChange={setName}
            />
            <LabelledText
              id="pc-email"
              label={t('ptl.contactEmail')}
              value={email}
              onChange={setEmail}
              inputMode="email"
            />
            <LabelledText
              id="pc-password"
              label={t('ptl.firstPassword')}
              hint={t('ptl.firstPasswordHint')}
              value={password}
              onChange={setPassword}
              type="password"
            />
            <div className="form__actions">
              <button
                className="ds-btn ds-btn--primary"
                disabled={
                  busy ||
                  supplierId === '' ||
                  name.trim() === '' ||
                  email.trim() === '' ||
                  password.length < 12
                }
                onClick={() =>
                  void run(async () => {
                    await inviteSupplierContact(client, companyId, {
                      supplier_id: supplierId,
                      full_name: name,
                      email,
                      password,
                    });
                    setName('');
                    setEmail('');
                    setPassword('');
                  })
                }
              >
                {t('ptl.invite')}
              </button>
            </div>
          </div>
        </div>
      )}

      <RemoteBody remote={remote} onRetry={reload}>
        {(payload: { data: SupplierContact[] }) =>
          payload.data.length === 0 ? (
            <div className="ds-panel__body">
              <EmptyState
                title={t('ptl.noContactsTitle')}
                body={t('ptl.noContactsBody')}
              />
            </div>
          ) : (
            <div className="ds-panel__body ds-scroll-x">
              <table className="ds-table">
                <thead>
                  <tr>
                    <th scope="col">{t('ptl.supplier')}</th>
                    <th scope="col">{t('ptl.contactName')}</th>
                    <th scope="col">{t('ptl.contactEmail')}</th>
                    <th scope="col">{t('ptl.lastSignedIn')}</th>
                    <th scope="col">
                      <span className="ds-visually-hidden">
                        {t('common.actions')}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {payload.data.map((c) => (
                    <tr key={c.id}>
                      <td>{c.supplier_name}</td>
                      <td>
                        {c.full_name}
                        {!c.is_active && (
                          <span className="ds-badge ds-badge--neutral">
                            {t('ptl.revoked')}
                          </span>
                        )}
                      </td>
                      <td>{c.email}</td>
                      <td>
                        {c.last_seen_at
                          ? shortDate(c.last_seen_at, locale)
                          : t('ptl.never')}
                      </td>
                      <td className="ds-table__actions">
                        {mayManage && c.is_active && (
                          <button
                            className="ds-btn ds-btn--quiet ds-btn--sm"
                            disabled={busy}
                            onClick={() =>
                              void run(() =>
                                revokeSupplierContact(client, companyId, c.id),
                              )
                            }
                          >
                            {t('ptl.revoke')}
                          </button>
                        )}
                      </td>
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
