'use client';

// One request for quotation: who was asked, what came back, and who was chosen.
//
// # Lowest is marked, and never called best
//
// `lowest_quote_id` is documented as "a convenience for the eye, NOT a
// recommendation: B5.1 requires a person to choose and to say why, because lead
// time and payment terms routinely outweigh price." So the badge says **Lowest
// total** and the hint under it says exactly that. A screen that said
// "recommended" would be making the decision the control exists to record.
//
// # Every quote carries the things that outweigh price
//
// Lead time, payment terms, how long the price holds, and whatever the supplier
// said about the goods. A comparison that showed only totals would push every
// buyer toward the cheapest and make the required reason a formality.
//
// # A supplier who said no is on the screen
//
// "Records that a supplier was asked and said no, which a missing quote cannot
// tell you." Three suppliers asked, one quote and one refusal is a different
// situation from three asked and one quote — the second still has somebody to
// chase.
//
// # Comparing is not awarding
//
// `purchasing.view` reads this; `purchasing.award_rfq` chooses. The award panel
// is absent without it rather than disabled, because the buyer who ran the
// comparison is deliberately not always the person who signs it off.

import { Award, Check, X } from 'lucide-react';
import Link from 'next/link';
import { use, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input, Select, Textarea } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { ApiError, messageFor } from '@/lib/api/errors';
import { useApi } from '@/lib/api/hooks';
import { useGrants } from '@/lib/auth/session';
import { useCompany, useCompanyScope } from '@/lib/company/company-context';
import { formatMoney, formatQuantity } from '@/lib/format/money';
import { useT } from '@/lib/i18n/locale';
import {
  RFQ_STATUS,
  canAward,
  canAwardQuote,
  type Comparison,
  type Quote,
  type RFQLine,
} from '@/lib/purchasing/sourcing';

function RFQScreen({ rfqID }: { rfqID: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const { currency, market } = useCompany();
  const grants = useGrants();

  const { data, isLoading, error, refetch } = useApi<Comparison>(
    scope ? `/purchasing/rfqs/${rfqID}/comparison` : null,
    scope ?? undefined,
  );

  const [awarding, setAwarding] = useState<string | null>(null);
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [orderId, setOrderId] = useState<string | null>(null);

  async function award(quoteId: string) {
    if (!scope || reason.trim() === '') return;
    setBusy(true);
    setActionError(null);
    setFieldErrors({});
    try {
      const out = await api.post<{ purchase_order?: { id: string } }>(
        `/purchasing/rfqs/${rfqID}/award?company_id=${scope.company_id}`,
        { quote_id: quoteId, reason },
      );
      setOrderId(out.purchase_order?.id ?? null);
      setAwarding(null);
      setReason('');
      await refetch();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setActionError(messageFor(e, t));
    } finally {
      setBusy(false);
    }
  }

  if (error) {
    return (
      <>
        <PageHeader title={t('nx.rfq.notFound')} />
        <ErrorState error={error} onRetry={() => void refetch()} />
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t('nx.rfq.title')} />
        <TableSkeleton columns={4} />
      </>
    );
  }

  const rfq = data.rfq;
  const quotes = data.quotes ?? [];
  const status = RFQ_STATUS[rfq.status];
  const money = (v: string) =>
    formatMoney(v, { currency: rfq.currency || currency, market });
  const mayAward = canAward(rfq.status) && grants.can('purchasing.award_rfq');

  const wanted: Column<RFQLine>[] = [
    {
      key: 'what',
      header: t('nx.npo.colProduct'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col gap-0.5">
          <span>{l.description}</span>
          {l.sku ? <span className="num text-caption text-muted">{l.sku}</span> : null}
        </span>
      ),
    },
    {
      key: 'qty',
      header: t('nx.req.colQty'),
      numeric: true,
      width: 'w-28',
      cell: (l) => <span className="num">{formatQuantity(l.qty)}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className="num">{rfq.rfq_number}</span>
            {status ? <Badge tone={status.tone}>{t(status.key)}</Badge> : null}
          </span>
        }
        description={[
          rfq.closes_on ? t('nx.rfq.colCloses') + ' ' + rfq.closes_on : '',
          rfq.notes,
        ]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <Link
            href="/buying/quotes"
            className="text-label text-muted underline underline-offset-4 hover:text-fg"
          >
            {t('nx.rfq.back')}
          </Link>
        }
      />

      <FormError message={actionError} className="mb-4" />

      {rfq.awarded_quote_id ? (
        <Panel className="mb-6">
          <p className="text-body font-medium text-fg">
            {t('nx.rfq.awardedTo', {
              supplier:
                quotes.find((q) => q.id === rfq.awarded_quote_id)?.supplier_name ??
                '—',
            })}
          </p>
          {rfq.awarded_by && rfq.awarded_at ? (
            <p className="mt-1 text-caption text-muted">
              {t('nx.rfq.awardedBy', {
                who: rfq.awarded_by,
                date: rfq.awarded_at.slice(0, 10),
              })}
            </p>
          ) : null}
          {rfq.award_reason ? (
            <p className="mt-2 max-w-prose text-body text-fg">{rfq.award_reason}</p>
          ) : null}
          {orderId ? (
            <Link
              href={`/buying/orders/${orderId}`}
              className="mt-3 inline-flex h-10 items-center rounded-sm border border-line-strong bg-surface px-3 text-body font-medium hover:border-primary"
            >
              {t('nx.rfq.openOrder')}
            </Link>
          ) : null}
        </Panel>
      ) : null}

      {/* Who was asked, and what each of them did about it. */}
      <Panel className="mb-6" title={t('nx.rfq.asked')}>
        <ul className="flex flex-col gap-2">
          {(rfq.invited ?? []).map((i) => (
            <li
              key={i.supplier_id}
              className="flex flex-wrap items-baseline justify-between gap-2"
            >
              <span className="text-body text-fg">{i.supplier_name}</span>
              {i.quoted ? (
                <Badge tone="positive">
                  <Check className="size-3" aria-hidden="true" />
                  {t('nx.rfq.quoted')}
                </Badge>
              ) : i.declined_at ? (
                <span className="flex flex-wrap items-center gap-2">
                  <Badge tone="neutral">
                    <X className="size-3" aria-hidden="true" />
                    {t('nx.rfq.declined')}
                  </Badge>
                  {i.decline_reason ? (
                    <span className="text-caption text-muted">{i.decline_reason}</span>
                  ) : null}
                </span>
              ) : (
                <Badge tone="caution">{t('nx.rfq.silent')}</Badge>
              )}
            </li>
          ))}
        </ul>
        <p className="mt-3 text-caption text-muted">{t('nx.rfq.declinedWhy')}</p>
      </Panel>

      {quotes.length === 0 ? (
        <EmptyState
          icon={Award}
          title={t('nx.rfq.noQuotes')}
          description={t('nx.rfq.noQuotesDesc')}
        />
      ) : (
        <>
          <h2 className="mb-3 text-section-title font-semibold text-fg">
            {t('nx.rfq.compare')}
          </h2>
          {/* Cards side by side rather than a table of totals: the things that
              outweigh price are sentences and durations, not figures, and a
              numeric grid buries them. Wraps to one column on a phone. */}
          <ul className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {quotes.map((q) => (
              <li key={q.id}>
                <QuoteCard
                  quote={q}
                  lowest={q.id === data.lowest_quote_id}
                  chosen={q.id === rfq.awarded_quote_id}
                  money={money}
                  mayAward={mayAward}
                  awarding={awarding === q.id}
                  busy={busy}
                  reason={reason}
                  reasonError={fieldErrors.award_reason}
                  onOpen={() => {
                    setAwarding(q.id);
                    setReason('');
                    setActionError(null);
                  }}
                  onCancel={() => setAwarding(null)}
                  onReason={setReason}
                  onConfirm={() => void award(q.id)}
                />
              </li>
            ))}
          </ul>

          <div className="mt-6">
            <ByLine quotes={quotes} lines={rfq.lines ?? []} money={money} />
          </div>
        </>
      )}

      <div className="mt-6">
        <Panel title={t('nx.rfq.wanted')} flush>
          <DataTable
            caption={t('nx.rfq.wantedCaption')}
            columns={wanted}
            rows={rfq.lines ?? []}
            rowKey={(l) => l.id}
            className="rounded-none border-0"
          />
        </Panel>
      </div>

      {canAward(rfq.status) && grants.can('purchasing.manage_rfq') ? (
        <div className="mt-6">
          <RecordReply rfqID={rfqID} onSaved={() => void refetch()} />
        </div>
      ) : null}
    </>
  );
}

/** One supplier's answer, with the things that decide it beside the total. */
function QuoteCard({
  quote,
  lowest,
  chosen,
  money,
  mayAward,
  awarding,
  busy,
  reason,
  reasonError,
  onOpen,
  onCancel,
  onReason,
  onConfirm,
}: {
  quote: Quote;
  lowest: boolean;
  chosen: boolean;
  money: (v: string) => string;
  mayAward: boolean;
  awarding: boolean;
  busy: boolean;
  reason: string;
  reasonError?: string;
  onOpen: () => void;
  onCancel: () => void;
  onReason: (v: string) => void;
  onConfirm: () => void;
}) {
  const t = useT();
  const usable = canAwardQuote(quote);

  return (
    <Panel
      title={
        <span className="flex flex-wrap items-center gap-2">
          {quote.supplier_name}
          {chosen ? <Badge tone="positive">{t('nx.rfq.stAwarded')}</Badge> : null}
          {lowest && !chosen ? (
            <Badge tone="info">{t('nx.rfq.lowestTotal')}</Badge>
          ) : null}
          {quote.expired ? <Badge tone="critical">{t('nx.rfq.expired')}</Badge> : null}
        </span>
      }
      description={[
        quote.quote_number,
        quote.revision > 1 ? t('nx.rfq.revision', { n: quote.revision }) : '',
      ]
        .filter(Boolean)
        .join(' · ')}
    >
      <p className="num text-page-title font-semibold text-fg">
        {money(quote.total_inclusive)}
      </p>

      <dl className="mt-3 flex flex-col gap-1 text-body">
        <div className="flex justify-between gap-3">
          <dt className="text-muted">{t('nx.rfq.leadDays')}</dt>
          <dd className={quote.lead_time_days === undefined ? 'text-subtle' : 'num'}>
            {quote.lead_time_days === undefined
              ? t('nx.rfq.leadUnknown')
              : t('nx.rfq.leadTime', { days: quote.lead_time_days })}
          </dd>
        </div>
        <div className="flex justify-between gap-3">
          <dt className="text-muted">{t('nx.rfq.termsDays')}</dt>
          <dd className={quote.payment_terms_days === undefined ? 'text-subtle' : 'num'}>
            {quote.payment_terms_days === undefined
              ? t('nx.rfq.termsUnknown')
              : t('nx.rfq.terms', { days: quote.payment_terms_days })}
          </dd>
        </div>
        {quote.valid_until ? (
          <div className="flex justify-between gap-3">
            <dt className="text-muted">{t('nx.rfq.validTo')}</dt>
            <dd className="num">
              {t('nx.rfq.validUntil', { date: quote.valid_until })}
            </dd>
          </div>
        ) : null}
      </dl>

      {quote.quality_note ? (
        <p className="mt-3 text-caption text-muted">{quote.quality_note}</p>
      ) : null}

      {quote.expired ? (
        <p className="mt-3 text-caption text-critical-fg">{t('nx.rfq.expiredWhy')}</p>
      ) : null}

      {lowest && !chosen ? (
        <p className="mt-3 text-caption text-muted">{t('nx.rfq.lowestHint')}</p>
      ) : null}

      {mayAward && usable && !chosen ? (
        awarding ? (
          <div className="mt-4 border-t border-line pt-3">
            <Field
              label={t('nx.rfq.awardReason')}
              hint={t('nx.rfq.awardReasonHint')}
              error={reasonError}
              required
            >
              <Textarea
                value={reason}
                onChange={(e) => onReason(e.target.value)}
                placeholder={t('nx.rfq.awardReasonPlaceholder')}
                rows={3}
                autoFocus
              />
            </Field>
            <p className="mt-2 text-caption text-muted">{t('nx.rfq.awardEffect')}</p>
            <div className="mt-3 flex flex-wrap gap-2">
              <Button
                variant="primary"
                size="sm"
                busy={busy}
                disabled={reason.trim() === ''}
                onClick={onConfirm}
              >
                {t('nx.rfq.awardConfirm')}
              </Button>
              <Button variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
                {t('nx.po.sendCancel')}
              </Button>
            </div>
          </div>
        ) : (
          <Button variant="secondary" className="mt-4" onClick={onOpen}>
            {t('nx.rfq.award')}
          </Button>
        )
      ) : null}
    </Panel>
  );
}

/** The same lines across every supplier, for the buyer who reads down a column. */
function ByLine({
  quotes,
  lines,
  money,
}: {
  quotes: Quote[];
  lines: RFQLine[];
  money: (v: string) => string;
}) {
  const t = useT();
  // Keyed by rfq_line_id so a supplier who did not price a line leaves a gap
  // that says so rather than shifting the column.
  const priced = new Map<string, Map<string, string>>();
  for (const q of quotes) {
    for (const l of q.lines ?? []) {
      if (!priced.has(l.rfq_line_id)) priced.set(l.rfq_line_id, new Map());
      priced.get(l.rfq_line_id)!.set(q.id, l.unit_cost);
    }
  }

  return (
    <Panel title={t('nx.rfq.byLine')} flush>
      {/* One column per supplier, so this is the table the guidance means when
          it says a wide table scrolls inside its own container. */}
      <div className="overflow-x-auto">
        <table className="w-full min-w-[36rem] border-collapse text-body">
          <caption className="sr-only">{t('nx.rfq.byLineCaption')}</caption>
          <thead>
            <tr className="border-b border-line">
              <th scope="col" className="px-4 py-2 text-start text-label text-muted">
                {t('nx.npo.colProduct')}
              </th>
              {quotes.map((q) => (
                <th
                  key={q.id}
                  scope="col"
                  className="px-4 py-2 text-end text-label text-muted"
                >
                  {q.supplier_name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {lines.map((l) => (
              <tr key={l.id} className="border-b border-line last:border-0">
                <th scope="row" className="px-4 py-2 text-start font-normal">
                  <span className="flex flex-col gap-0.5">
                    <span>{l.description}</span>
                    <span className="num text-caption text-muted">
                      {formatQuantity(l.qty)}
                    </span>
                  </span>
                </th>
                {quotes.map((q) => {
                  const cost = priced.get(l.id)?.get(q.id);
                  return (
                    <td key={q.id} className="px-4 py-2 text-end">
                      {cost ? (
                        <span className="num">{money(cost)}</span>
                      ) : (
                        <span className="text-caption text-subtle">
                          {t('nx.rfq.noQuoteForLine')}
                        </span>
                      )}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

/** Filing what a supplier said, whether that was a price or a refusal. */
function RecordReply({
  rfqID,
  onSaved,
}: {
  rfqID: string;
  onSaved: () => void;
}) {
  const t = useT();
  const scope = useCompanyScope();
  const { data } = useApi<Comparison>(
    scope ? `/purchasing/rfqs/${rfqID}/comparison` : null,
    scope ?? undefined,
  );

  const [supplierId, setSupplierId] = useState('');
  const [quoteNumber, setQuoteNumber] = useState('');
  const [receivedOn, setReceivedOn] = useState(() =>
    new Date().toISOString().slice(0, 10),
  );
  const [validUntil, setValidUntil] = useState('');
  const [lead, setLead] = useState('');
  const [terms, setTerms] = useState('');
  const [quality, setQuality] = useState('');
  const [costs, setCosts] = useState<Record<string, string>>({});
  const [declineReason, setDeclineReason] = useState('');
  const [busy, setBusy] = useState<'quote' | 'decline' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  const rfq = data?.rfq;
  // Only somebody who has not already answered. A second reply is a revision
  // and is a deliberate act, so it is not offered by accident here.
  const waiting = (rfq?.invited ?? []).filter((i) => !i.quoted && !i.declined_at);

  async function saveQuote() {
    if (!scope || !rfq || !supplierId) return;
    setBusy('quote');
    setError(null);
    setFieldErrors({});
    try {
      await api.post(
        `/purchasing/rfqs/${rfqID}/quotes?company_id=${scope.company_id}`,
        {
          supplier_id: supplierId,
          quote_number: quoteNumber,
          received_on: receivedOn,
          valid_until: validUntil,
          lead_time_days: lead === '' ? null : Number(lead),
          payment_terms_days: terms === '' ? null : Number(terms),
          quality_note: quality,
          lines: (rfq.lines ?? []).map((l) => ({
            rfq_line_id: l.id,
            qty: l.qty,
            unit_cost: costs[l.id] || '0',
            // The treatment the register prices; the rate is never sent.
            tax_treatment: 'standard',
          })),
        },
      );
      setSupplierId('');
      setCosts({});
      setQuoteNumber('');
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.fields) setFieldErrors(e.fields);
      setError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  async function saveDecline() {
    if (!scope || !supplierId) return;
    setBusy('decline');
    setError(null);
    try {
      await api.post(
        `/purchasing/rfqs/${rfqID}/declines?company_id=${scope.company_id}`,
        { supplier_id: supplierId, reason: declineReason },
      );
      setSupplierId('');
      setDeclineReason('');
      onSaved();
    } catch (e) {
      setError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  if (!rfq) return null;

  return (
    <Panel title={t('nx.rfq.recordQuote')}>
      <FormError message={error} className="mb-3" />

      <Field label={t('nx.rfq.quoteFrom')} required>
        <Select value={supplierId} onChange={(e) => setSupplierId(e.target.value)}>
          <option value="">{t('nx.rfq.chooseSupplierFirst')}</option>
          {waiting.map((i) => (
            <option key={i.supplier_id} value={i.supplier_id}>
              {i.supplier_name}
            </option>
          ))}
        </Select>
      </Field>

      {supplierId ? (
        <>
          <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            <Field label={t('nx.rfq.quoteNumber')}>
              <Input
                value={quoteNumber}
                onChange={(e) => setQuoteNumber(e.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label={t('nx.rfq.receivedOn')} error={fieldErrors.received_on}>
              <Input
                type="date"
                value={receivedOn}
                onChange={(e) => setReceivedOn(e.target.value)}
              />
            </Field>
            <Field label={t('nx.rfq.validTo')} error={fieldErrors.valid_until}>
              <Input
                type="date"
                value={validUntil}
                onChange={(e) => setValidUntil(e.target.value)}
              />
            </Field>
            <Field label={t('nx.rfq.leadDays')}>
              <Input
                value={lead}
                onChange={(e) => setLead(e.target.value)}
                inputMode="numeric"
                type="number"
                min={0}
              />
            </Field>
            <Field label={t('nx.rfq.termsDays')}>
              <Input
                value={terms}
                onChange={(e) => setTerms(e.target.value)}
                inputMode="numeric"
                type="number"
                min={0}
              />
            </Field>
            <Field label={t('nx.rfq.qualityNote')}>
              <Input value={quality} onChange={(e) => setQuality(e.target.value)} />
            </Field>
          </div>

          <ul className="mt-4 flex flex-col divide-y divide-line">
            {(rfq.lines ?? []).map((l) => (
              <li
                key={l.id}
                className="flex flex-wrap items-center justify-between gap-3 py-2"
              >
                <span className="min-w-0">
                  <span className="block truncate text-body text-fg">
                    {l.description}
                  </span>
                  <span className="num text-caption text-muted">
                    {formatQuantity(l.qty)}
                  </span>
                </span>
                <div className="w-36">
                  <Field label={t('nx.rfq.unitCost')}>
                    <Input
                      value={costs[l.id] ?? ''}
                      onChange={(e) =>
                        setCosts((c) => ({ ...c, [l.id]: e.target.value }))
                      }
                      inputMode="decimal"
                      autoComplete="off"
                    />
                  </Field>
                </div>
              </li>
            ))}
          </ul>

          <p className="mt-3 text-caption text-muted">{t('nx.rfq.revisionHint')}</p>

          <div className="mt-3 flex flex-wrap gap-2">
            <Button
              variant="primary"
              busy={busy === 'quote'}
              disabled={busy !== null}
              onClick={() => void saveQuote()}
            >
              {t('nx.rfq.saveQuote')}
            </Button>
          </div>

          <div className="mt-6 border-t border-line pt-4">
            <Field label={t('nx.rfq.declineReason')} hint={t('nx.rfq.decline')}>
              <Input
                value={declineReason}
                onChange={(e) => setDeclineReason(e.target.value)}
              />
            </Field>
            <Button
              variant="secondary"
              size="sm"
              className="mt-3"
              busy={busy === 'decline'}
              disabled={busy !== null || declineReason.trim() === ''}
              onClick={() => void saveDecline()}
            >
              {t('nx.rfq.saveDecline')}
            </Button>
          </div>
        </>
      ) : null}
    </Panel>
  );
}

export default function RFQPage({ params }: { params: Promise<{ rfqID: string }> }) {
  const { rfqID } = use(params);
  return (
    <RequirePermission anyOf={['purchasing.view']}>
      <RFQScreen rfqID={rfqID} />
    </RequirePermission>
  );
}
