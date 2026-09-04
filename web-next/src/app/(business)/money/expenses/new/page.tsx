'use client';

// Recording what was spent.
//
// # The category decides whether the tax comes back
//
// Each expense head carries `input_vat_recoverable`, and E2.3 restricts it by
// CATEGORY rather than by amount — all of it or none of it, never apportioned.
// So the line says so as soon as a category is chosen, rather than letting
// somebody discover it on the VAT return.
//
// # Nobody types a tax rate here either
//
// The expenses service says it outright: "The RATE comes from the registry at
// the expense date, never from the caller: a client that could state its own
// VAT rate could state what the return claims." The treatment is the caller's,
// because only they know whether the supplier charged.
//
// # The uuid is minted here
//
// So a retry after a lost response returns the original rather than paying the
// electricity bill twice.

import { Plus, Trash2 } from 'lucide-react';
import { useRouter } from 'next/navigation';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import { TAX_TREATMENT } from '@/lib/purchasing/orders';
import type { Department, Expense, ExpenseHead } from '@/lib/money/expenses';
import { sumOf } from '@/lib/purchasing/allocate';

interface DraftLine {
  head_id: string;
  department_id: string;
  net: string;
  tax_treatment: string;
  note: string;
}

function blank(): DraftLine {
  return {
    head_id: '',
    department_id: '',
    net: '',
    tax_treatment: 'standard',
    note: '',
  };
}

function NewExpenseScreen() {
  const t = useT();
  const router = useRouter();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();

  const heads = useApiList<ExpenseHead>(
    scope ? '/expenses/heads' : null,
    scope ?? undefined,
  );
  const departments = useApiList<Department>(
    scope ? '/expenses/departments' : null,
    scope ?? undefined,
  );

  const [date, setDate] = useState(() => new Date().toISOString().slice(0, 10));
  const [paidFrom, setPaidFrom] = useState('');
  const [supplier, setSupplier] = useState('');
  const [reference, setReference] = useState('');
  const [note, setNote] = useState('');
  const [lines, setLines] = useState<DraftLine[]>([blank()]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [docUUID, setDocUUID] = useState(() => crypto.randomUUID());

  const headList = (heads.data?.data ?? []).filter((h) => h.is_active);
  const netTotal = sumOf(lines.map((l) => l.net));

  async function save() {
    if (!scope) return;
    setError(null);
    setFieldErrors({});
    if (!paidFrom) return setError(t('nx.exp.needFrom'));
    const real = lines.filter((l) => l.head_id !== '' && l.net.trim() !== '');
    if (real.length === 0) return setError(t('nx.exp.needLines'));

    setBusy(true);
    try {
      const out = await api.post<Expense>(`/expenses?company_id=${scope.company_id}`, {
        uuid: docUUID,
        expense_date: date,
        paid_from: paidFrom,
        // The route carries ONE department for the voucher rather than one per
        // line, so the first line that names one sets it. C3.1 treats a cost
        // centre as a dimension of the document, not of the line.
        department_id: real.find((l) => l.department_id)?.department_id || undefined,
        reference,
        description: [supplier, note].filter(Boolean).join(' — '),
        lines: real.map((l) => ({
          head_id: l.head_id,
          description: l.note,
          // NET, never gross. "The server computes the tax from the registry
          // rate for the expense date, so a client cannot decide what the VAT
          // return claims."
          net_amount: l.net,
          tax_treatment: l.tax_treatment,
        })),
      });
      setDocUUID(crypto.randomUUID());
      router.push(`/money/expenses/${out.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title={t('nx.exp.newTitle')} description={t('nx.exp.newSubtitle')} />

      <FormError message={error} className="mb-4" />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <Panel title={t('nx.exp.what')}>
          <ul className="flex flex-col divide-y divide-line">
            {lines.map((line, i) => {
              const head = headList.find((h) => h.id === line.head_id);
              return (
                <li key={i} className="py-3 first:pt-0">
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label={t('nx.exp.head')} required>
                      <Select
                        value={line.head_id}
                        onChange={(e) =>
                          setLines((c) =>
                            c.map((l, j) =>
                              j === i ? { ...l, head_id: e.target.value } : l,
                            ),
                          )
                        }
                      >
                        <option value="">{t('nx.exp.chooseHead')}</option>
                        {headList.map((h) => (
                          <option key={h.id} value={h.id}>
                            {h.name}
                          </option>
                        ))}
                      </Select>
                    </Field>
                    {/* One department per voucher, not per line: the route
                        carries a single department_id and C3.1 treats a cost
                        centre as a dimension of the document. Shown on the
                        first line only. */}
                    <Field label={t('nx.exp.department')}>
                      <Select
                        value={line.department_id}
                        onChange={(e) =>
                          setLines((c) =>
                            c.map((l, j) =>
                              j === i ? { ...l, department_id: e.target.value } : l,
                            ),
                          )
                        }
                      >
                        <option value="">{t('nx.exp.noDepartment')}</option>
                        {(departments.data?.data ?? [])
                          .filter((d) => d.is_active)
                          .map((d) => (
                            <option key={d.id} value={d.id}>
                              {d.name}
                            </option>
                          ))}
                      </Select>
                    </Field>
                    <Field label={t('nx.exp.net')} required>
                      <Input
                        value={line.net}
                        onChange={(e) =>
                          setLines((c) =>
                            c.map((l, j) => (j === i ? { ...l, net: e.target.value } : l)),
                          )
                        }
                        inputMode="decimal"
                        autoComplete="off"
                      />
                    </Field>
                    <Field label={t('nx.exp.taxTreatment')}>
                      <Select
                        value={line.tax_treatment}
                        onChange={(e) =>
                          setLines((c) =>
                            c.map((l, j) =>
                              j === i ? { ...l, tax_treatment: e.target.value } : l,
                            ),
                          )
                        }
                      >
                        {Object.entries(TAX_TREATMENT).map(([value, key]) => (
                          <option key={value} value={value}>
                            {t(key)}
                          </option>
                        ))}
                      </Select>
                    </Field>
                  </div>

                  {/* Said as soon as the category is chosen, rather than
                      discovered on the VAT return. */}
                  {head && !head.input_vat_recoverable ? (
                    <p className="mt-2">
                      <Badge tone="caution">{t('nx.exp.notReclaimable')}</Badge>
                    </p>
                  ) : null}

                  <div className="mt-3 flex items-end gap-3">
                    <Field label={t('nx.exp.note')} className="flex-1">
                      <Input
                        value={line.note}
                        onChange={(e) =>
                          setLines((c) =>
                            c.map((l, j) => (j === i ? { ...l, note: e.target.value } : l)),
                          )
                        }
                      />
                    </Field>
                    {lines.length > 1 ? (
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={t('nx.npo.remove')}
                        className="mb-1"
                        onClick={() => setLines((c) => c.filter((_, j) => j !== i))}
                      >
                        <Trash2 aria-hidden="true" />
                      </Button>
                    ) : null}
                  </div>
                </li>
              );
            })}
          </ul>

          <Button
            variant="secondary"
            size="sm"
            className="mt-3"
            onClick={() => setLines((c) => [...c, blank()])}
          >
            <Plus aria-hidden="true" />
            {t('nx.exp.addLine')}
          </Button>

          <div className="mt-4 flex items-baseline justify-between gap-4 border-t border-line pt-3">
            <span className="text-body font-semibold">{t('nx.exp.net')}</span>
            <span className="num text-body font-semibold">
              {formatMoney(netTotal, { currency, market })}
            </span>
          </div>
        </Panel>

        <div className="flex flex-col gap-6">
          <Panel>
            <div className="flex flex-col gap-4">
              {/* A ROLE, not an account. The service is explicit: 'cash or
                  bank -- a role rather than an account id because these two
                  ARE configuration: every company has exactly one of each and
                  the chart already maps them.' A picker of treasury accounts
                  here would offer a choice the route does not have. */}
              <Field
                label={t('nx.exp.paidFrom')}
                hint={t('nx.exp.paidFromHint')}
                error={fieldErrors.paid_from}
                required
              >
                <Select value={paidFrom} onChange={(e) => setPaidFrom(e.target.value)}>
                  <option value="">{t('nx.exp.choosePaidFrom')}</option>
                  <option value="cash">{t('nx.exp.fromCash')}</option>
                  <option value="bank">{t('nx.exp.fromBank')}</option>
                </Select>
              </Field>
              <Field label={t('nx.exp.when')} error={fieldErrors.date}>
                <Input type="date" value={date} onChange={(e) => setDate(e.target.value)} />
              </Field>
              <Field label={t('nx.exp.paidTo')}>
                <Input value={supplier} onChange={(e) => setSupplier(e.target.value)} />
              </Field>
              <Field label={t('nx.exp.reference')}>
                <Input
                  value={reference}
                  onChange={(e) => setReference(e.target.value)}
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              <Field label={t('nx.exp.note')}>
                <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={2} />
              </Field>
            </div>
          </Panel>

          <div>
            <Button variant="primary" busy={busy} className="w-full" onClick={() => void save()}>
              {t('nx.exp.save')}
            </Button>
            <p className="mt-2 text-caption text-muted">{t('nx.exp.saveHint')}</p>
          </div>
        </div>
      </div>
    </>
  );
}

export default function NewExpensePage() {
  return (
    <RequirePermission anyOf={['expense.record']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <NewExpenseScreen />
      </Suspense>
    </RequirePermission>
  );
}
