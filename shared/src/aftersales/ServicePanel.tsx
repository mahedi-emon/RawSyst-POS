// Repairs and service work (blueprint B15).
//
// # A warranty job and a paid job look the same and cost differently
//
// The parts come out of the same stock either way. On a warranty job the shop
// absorbs the cost; on a paid one the customer is charged. The kind is chosen
// when the item is booked in, and the screen shows what will be charged
// alongside what it cost, because those are two different numbers and the
// difference is the shop's warranty bill.

import { useCallback, useState } from 'react';

import {
  bookInRepair,
  listServiceJobs,
  readServiceJob,
  updateRepair,
  type ServiceJob,
  type ServiceStatus,
} from '../api/aftersales';
import { useAuth } from '../auth/session';
import { EmptyState, RemoteBody } from '../dashboard/DetailScreen';
import { useRemote } from '../dashboard/useRemote';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import {
  Field,
  FormActions,
  FormError,
  SelectInput,
  TextInput,
} from '../ui/Form';
import { money, shortDate } from '../ui/format';

const STATUSES: ServiceStatus[] = [
  'received',
  'inspecting',
  'awaiting_parts',
  'repaired',
  'irreparable',
  'replaced',
  'delivered',
  'cancelled',
];

export function ServicePanel({ companyId }: { companyId: string }) {
  const { client } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const [open, setOpen] = useState<string | null>(null);
  const [booking, setBooking] = useState(false);
  const [showClosed, setShowClosed] = useState(false);

  const load = useCallback(
    () => listServiceJobs(client, companyId, {}),
    [client, companyId],
  );
  const { remote, reload } = useRemote(load);

  if (open) {
    return (
      <JobDetail
        companyId={companyId}
        jobId={open}
        onBack={() => {
          setOpen(null);
          reload();
        }}
      />
    );
  }

  return (
    <>
      {booking && (
        <BookInForm
          companyId={companyId}
          onCancel={() => setBooking(false)}
          onBooked={(id) => {
            setBooking(false);
            reload();
            setOpen(id);
          }}
        />
      )}

      <section className="ds-panel" aria-label={t('after.service')}>
        <div className="ds-panel__head">
          <h2 className="ds-h3">{t('after.service')}</h2>
          <div className="after__actions">
            <button
              className="ds-btn ds-btn--quiet"
              onClick={() => setShowClosed(!showClosed)}
            >
              {t(showClosed ? 'after.showOpenJobs' : 'after.showAllJobs')}
            </button>
            {!booking && (
              <button
                className="ds-btn ds-btn--primary"
                onClick={() => setBooking(true)}
              >
                {t('after.bookIn')}
              </button>
            )}
          </div>
        </div>

        <RemoteBody remote={remote} onRetry={reload}>
          {(payload: { data: ServiceJob[] }) => {
            const rows = showClosed
              ? payload.data
              : payload.data.filter(
                  (j) => !['delivered', 'cancelled'].includes(j.status),
                );

            return rows.length === 0 ? (
              <div className="ds-panel__body">
                <EmptyState
                  title={t('after.noJobsTitle')}
                  body={t('after.noJobsBody')}
                />
              </div>
            ) : (
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('after.job')}</th>
                      <th scope="col">{t('after.item')}</th>
                      <th scope="col">{t('after.fault')}</th>
                      <th scope="col">{t('common.status')}</th>
                      <th scope="col" className="num">
                        {t('after.cost')}
                      </th>
                      <th scope="col" className="num">
                        {t('after.charged')}
                      </th>
                      <th scope="col">
                        <span className="ds-visually-hidden">
                          {t('common.actions')}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((j) => (
                      <tr key={j.id}>
                        <td>
                          <span className="detail__strong">{j.job_no}</span>
                          <span className="ds-caption">
                            {t(`after.jobKind.${j.kind}` as Key)}
                            {j.customer ? ` · ${j.customer}` : ''}
                          </span>
                          {j.promised_on && (
                            <span className="ds-caption">
                              {t('after.promised', {
                                date: shortDate(j.promised_on, locale),
                              })}
                            </span>
                          )}
                        </td>
                        <td>
                          {j.product ?? '—'}
                          {j.serial_no && (
                            <span className="ds-caption">{j.serial_no}</span>
                          )}
                        </td>
                        <td>{j.fault_reported}</td>
                        <td>
                          <span
                            className={`ds-badge ds-badge--${jobBadge(j.status)}`}
                          >
                            {t(`after.job.${j.status}` as Key)}
                          </span>
                        </td>
                        <td className="num">
                          {money(addCosts(j.parts_cost, j.labour_cost), {
                            currency: j.currency,
                          })}
                        </td>
                        <td className="num">
                          {/* On a warranty job this is zero and the cost is
                              not, which is the shop's warranty bill. */}
                          {money(j.charged, { currency: j.currency })}
                        </td>
                        <td>
                          <button
                            className="ds-btn ds-btn--quiet"
                            onClick={() => setOpen(j.id)}
                          >
                            {t('action.view')}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
          }}
        </RemoteBody>
      </section>
    </>
  );
}

function jobBadge(status: ServiceStatus): string {
  switch (status) {
    case 'repaired':
    case 'delivered':
      return 'success';
    case 'irreparable':
    case 'cancelled':
      return 'danger';
    case 'awaiting_parts':
      return 'warning';
    default:
      return 'info';
  }
}

// addCosts sums parts and labour in minor units.
function addCosts(a: string, b: string): string {
  const cents = [a, b].reduce((sum, v) => {
    const [whole = '0', frac = ''] = (v || '0').trim().split('.');
    return (
      sum +
      BigInt(whole || '0') * 100n +
      BigInt((frac + '00').slice(0, 2) || '0')
    );
  }, 0n);
  return `${cents / 100n}.${String(cents % 100n).padStart(2, '0')}`;
}

function JobDetail({
  companyId,
  jobId,
  onBack,
}: {
  companyId: string;
  jobId: string;
  onBack: () => void;
}) {
  const { client, can } = useAuth();
  const t = useT();
  const { locale } = useLocale();

  const mayManage = can('service.manage');
  const load = useCallback(
    () => readServiceJob(client, companyId, jobId),
    [client, companyId, jobId],
  );
  const { remote, reload } = useRemote(load);

  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [status, setStatus] = useState<string>('');
  const [diagnosis, setDiagnosis] = useState('');
  const [workDone, setWorkDone] = useState('');
  const [labour, setLabour] = useState('');
  const [charged, setCharged] = useState('');

  async function save(j: ServiceJob) {
    setBusy(true);
    setFailure(null);
    try {
      await updateRepair(client, companyId, j.id, {
        status: status || undefined,
        diagnosis: diagnosis || undefined,
        work_done: workDone || undefined,
        labour_cost: labour || undefined,
        charged: charged || undefined,
      });
      setStatus('');
      setDiagnosis('');
      setWorkDone('');
      setLabour('');
      setCharged('');
      reload();
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <RemoteBody remote={remote} onRetry={reload}>
      {(j: ServiceJob) => (
        <>
          <section className="ds-panel">
            <div className="ds-panel__head">
              <div>
                <button className="ds-btn ds-btn--quiet" onClick={onBack}>
                  {t('action.back')}
                </button>
                <h2 className="ds-h3">{j.job_no}</h2>
                <p className="ds-caption">
                  {t(`after.jobKind.${j.kind}` as Key)} ·{' '}
                  {t(`after.job.${j.status}` as Key)}
                  {j.customer ? ` · ${j.customer}` : ''}
                </p>
              </div>
            </div>

            <div className="ds-panel__body">
              <FormError message={failure} />

              <dl className="after__facts">
                <div>
                  <dt>{t('after.item')}</dt>
                  <dd>
                    {j.product ?? '—'}
                    {j.serial_no ? ` · ${j.serial_no}` : ''}
                  </dd>
                </div>
                <div>
                  <dt>{t('after.fault')}</dt>
                  <dd>{j.fault_reported}</dd>
                </div>
                <div>
                  <dt>{t('after.receivedOn')}</dt>
                  <dd>{shortDate(j.received_at, locale)}</dd>
                </div>
                <div>
                  <dt>{t('after.partsCost')}</dt>
                  <dd className="num">
                    {money(j.parts_cost, { currency: j.currency })}
                  </dd>
                </div>
                <div>
                  <dt>{t('after.labour')}</dt>
                  <dd className="num">
                    {money(j.labour_cost, { currency: j.currency })}
                  </dd>
                </div>
                <div>
                  <dt>{t('after.charged')}</dt>
                  <dd className="num">
                    {money(j.charged, { currency: j.currency })}
                  </dd>
                </div>
              </dl>

              {j.diagnosis && (
                <p>
                  <strong>{t('after.diagnosis')}: </strong>
                  {j.diagnosis}
                </p>
              )}
              {j.work_done && (
                <p>
                  <strong>{t('after.workDone')}: </strong>
                  {j.work_done}
                </p>
              )}
            </div>
          </section>

          {mayManage && (
            <section className="ds-panel after__form">
              <div className="ds-panel__head">
                <h3 className="ds-h3">{t('after.recordProgress')}</h3>
              </div>
              <div className="ds-panel__body">
                <div className="form__grid">
                  <Field label={t('common.status')} htmlFor="sj-status">
                    <SelectInput
                      id="sj-status"
                      value={status}
                      onChange={setStatus}
                      options={STATUSES.map((s) => ({ id: s }))}
                      label={(s) => t(`after.job.${s.id}` as Key)}
                      placeholder={t('after.leaveAsIs')}
                    />
                  </Field>
                  <Field label={t('after.diagnosis')} htmlFor="sj-diag">
                    <TextInput
                      id="sj-diag"
                      value={diagnosis}
                      onChange={setDiagnosis}
                    />
                  </Field>
                  <Field label={t('after.workDone')} htmlFor="sj-work">
                    <TextInput
                      id="sj-work"
                      value={workDone}
                      onChange={setWorkDone}
                    />
                  </Field>
                  <Field label={t('after.labour')} htmlFor="sj-labour">
                    <TextInput
                      id="sj-labour"
                      value={labour}
                      onChange={setLabour}
                      inputMode="decimal"
                    />
                  </Field>
                  <Field
                    label={t('after.charged')}
                    hint={t('after.chargedHint')}
                    htmlFor="sj-charged"
                  >
                    <TextInput
                      id="sj-charged"
                      value={charged}
                      onChange={setCharged}
                      inputMode="decimal"
                    />
                  </Field>
                </div>
                <FormActions
                  submitLabel={t('action.saveChanges')}
                  busy={busy}
                  onCancel={onBack}
                />
                <button
                  type="button"
                  className="ds-btn ds-btn--primary"
                  disabled={busy}
                  onClick={() => void save(j)}
                >
                  {t('after.recordProgress')}
                </button>
              </div>
            </section>
          )}

          {(j.parts ?? []).length > 0 && (
            <section className="ds-panel" aria-label={t('after.partsFitted')}>
              <div className="ds-panel__head">
                <h3 className="ds-h3">{t('after.partsFitted')}</h3>
              </div>
              <div className="ds-panel__body ds-scroll-x">
                <table className="ds-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('after.part')}</th>
                      <th scope="col" className="num">
                        {t('orders.qty')}
                      </th>
                      <th scope="col" className="num">
                        {t('after.cost')}
                      </th>
                      <th scope="col">{t('common.when')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(j.parts ?? []).map((p) => (
                      <tr key={p.id}>
                        <td>{p.sku ?? p.variant_id}</td>
                        <td className="num">{p.qty}</td>
                        <td className="num">
                          {money(p.unit_cost, { currency: j.currency })}
                        </td>
                        <td>{shortDate(p.issued_at, locale)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )}
        </>
      )}
    </RemoteBody>
  );
}

function BookInForm({
  companyId,
  onCancel,
  onBooked,
}: {
  companyId: string;
  onCancel: () => void;
  onBooked: (id: string) => void;
}) {
  const { client } = useAuth();
  const t = useT();

  const [serial, setSerial] = useState('');
  const [fault, setFault] = useState('');
  const [kind, setKind] = useState<string>('paid');
  const [promised, setPromised] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setFailure(null);
    try {
      const job = await bookInRepair(client, companyId, {
        serial_no: serial || undefined,
        fault_reported: fault,
        kind,
        promised_on: promised || undefined,
      });
      onBooked(job.id);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : t('common.didNotWork'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="ds-panel after__form"
      onSubmit={(e) => void submit(e)}
      noValidate
    >
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('after.bookIn')}</h2>
        <p className="ds-caption">{t('after.bookInHint')}</p>
      </div>
      <div className="ds-panel__body">
        <FormError message={failure} />
        <div className="form__grid">
          <Field
            label={t('after.serialNo')}
            hint={t('after.serialForWarranty')}
            htmlFor="bi-serial"
          >
            <TextInput id="bi-serial" value={serial} onChange={setSerial} />
          </Field>
          <Field label={t('after.jobKind')} htmlFor="bi-kind" required>
            <SelectInput
              id="bi-kind"
              value={kind}
              onChange={setKind}
              options={[{ id: 'paid' }, { id: 'warranty' }, { id: 'goodwill' }]}
              label={(k) => t(`after.jobKind.${k.id}` as Key)}
            />
          </Field>
          <Field label={t('after.fault')} htmlFor="bi-fault" required>
            <TextInput id="bi-fault" value={fault} onChange={setFault} />
          </Field>
          <Field
            label={t('after.promisedFor')}
            hint={t('after.promisedForHint')}
            htmlFor="bi-promised"
          >
            <input
              id="bi-promised"
              type="date"
              className="field__input"
              value={promised}
              onChange={(e) => setPromised(e.target.value)}
            />
          </Field>
        </div>
        <FormActions
          submitLabel={t('after.bookIn')}
          busy={busy}
          disabled={fault.trim() === ''}
          onCancel={onCancel}
        />
      </div>
    </form>
  );
}
