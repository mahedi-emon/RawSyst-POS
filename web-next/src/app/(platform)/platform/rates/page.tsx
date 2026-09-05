'use client';

// Published rate schedules, and how far through their checks each one is.
//
// # Activation is what opens a market. Review and verification are not.
//
// This distinction is the whole screen, and getting it backwards would be a
// lie about the law. `handleActivateRates` says it plainly: the two-person
// review is RawSyst's own internal governance, it is not a CDTFA requirement,
// and requiring it left five hundred and forty-one lawfully published
// Californian rates unusable. Activation checks what software can honestly
// check — provenance, an authority chain that reaches a country, and the
// schema's guarantees about range and overlap — and records what it checked.
//
// So the primary action on an imported schedule is Activate, and review and
// verification sit beside it as internal controls a platform may choose to
// apply. Nothing here describes either as required by a tax authority, because
// neither is.
//
// # Two people, and the server is the one that enforces it
//
// Verification is refused to whoever recorded the review. The screen does not
// try to predict that refusal by hiding the button from the reviewer: it does
// not reliably know who reviewed a batch it did not just act on, and a button
// hidden on a guess is worse than a refusal that explains itself. The server
// refuses and the sentence is shown.
//
// # A batch is named by four fields, not by an id
//
// There is no batch id anywhere in the API. `BatchRef` is
// (country, source_document, treatment, effective_from), because one authority
// publishes many schedules and the effective date is what tells them apart.
// Every action here sends all four back exactly as they were received.

import { FileSpreadsheet } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequireWorkspace } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Select } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useT } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

interface RateBatch {
  country: string;
  source_authority: string;
  source_document: string;
  treatment: string;
  effective_from: string;
  rates: number;
  reviewed: number;
  verified: number;
  reviewed_by?: string;
  verified_by?: string;
  review_note?: string;
  /** imported, reviewed, verified, or part-verified. */
  status: string;
}

/** The four fields that name a schedule. There is no id. */
function refOf(b: RateBatch) {
  return {
    country: b.country,
    source_document: b.source_document,
    treatment: b.treatment,
    effective_from: b.effective_from,
  };
}

function keyOf(b: RateBatch) {
  return [b.country, b.source_document, b.treatment, b.effective_from].join('|');
}

const STATUS_TONE: Record<string, 'neutral' | 'info' | 'positive' | 'caution'> = {
  imported: 'neutral',
  reviewed: 'info',
  verified: 'positive',
  'part-verified': 'caution',
};

function RatesScreen() {
  const t = useT();
  const [country, setCountry] = useUrlState('country');
  const { data, isLoading, error, refetch } = useApiList<RateBatch>(
    '/platform/jurisdictions/rates',
  );

  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const all = data?.data ?? [];
  const rows = country === '' ? all : all.filter((b) => b.country === country);

  async function act(batch: RateBatch, verb: 'review' | 'activate' | 'verify') {
    setBusy(keyOf(batch) + verb);
    setActionError(null);
    setNote(null);
    try {
      const out = await api.post<Record<string, number>>(
        `/platform/jurisdictions/rates/${verb}`,
        refOf(batch),
      );
      // How many rows the action actually touched, not merely that it worked.
      // Acting on a schedule somebody has already stamped answers zero, and
      // "0 rates" and "541 rates" are different outcomes.
      const count = out.reviewed ?? out.verified ?? out.activated ?? 0;
      setNote(
        t(
          verb === 'review'
            ? 'nx.plat.raReviewed'
            : verb === 'verify'
              ? 'nx.plat.raVerified'
              : 'nx.plat.raActivated',
          { count: String(count) },
        ),
      );
      void refetch();
    } catch (e) {
      setActionError(messageFor(e, t));
    } finally {
      setBusy(null);
    }
  }

  const columns: Column<RateBatch>[] = [
    {
      key: 'document',
      header: t('nx.plat.raDocument'),
      primary: true,
      cell: (b) => (
        <span className="flex flex-col">
          <span>{b.source_document}</span>
          <span className="text-caption text-muted">
            <span className="num uppercase">{b.source_authority}</span> ·{' '}
            <span className="num uppercase">{b.country}</span> · {b.treatment}
          </span>
        </span>
      ),
    },
    {
      key: 'from',
      header: t('nx.plat.raFrom'),
      width: 'w-28',
      cell: (b) => <time dateTime={b.effective_from}>{b.effective_from}</time>,
    },
    {
      key: 'rates',
      header: t('nx.plat.raRates'),
      numeric: true,
      width: 'w-24',
      cell: (b) => b.rates,
    },
    {
      key: 'checks',
      header: t('nx.plat.raChecks'),
      width: 'w-44',
      cell: (b) => (
        <span className="flex flex-col text-caption text-muted">
          <span>{t('nx.plat.raReviewedOf', { n: String(b.reviewed), of: String(b.rates) })}</span>
          <span>{t('nx.plat.raVerifiedOf', { n: String(b.verified), of: String(b.rates) })}</span>
        </span>
      ),
    },
    {
      key: 'status',
      header: t('nx.plat.raStatus'),
      width: 'w-32',
      cell: (b) => (
        <Badge tone={STATUS_TONE[b.status] ?? 'neutral'}>{b.status}</Badge>
      ),
    },
    {
      key: 'actions',
      header: t('nx.plat.raActions'),
      width: 'w-64',
      cell: (b) => (
        <span className="flex flex-wrap gap-1.5">
          {/* Activate first and unqualified: it is what makes a lawfully
              published schedule usable by the shops in that jurisdiction. */}
          <Button
            size="sm"
            busy={busy === keyOf(b) + 'activate'}
            onClick={() => void act(b, 'activate')}
          >
            {t('nx.plat.raActivate')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            busy={busy === keyOf(b) + 'review'}
            onClick={() => void act(b, 'review')}
          >
            {t('nx.plat.raReview')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            busy={busy === keyOf(b) + 'verify'}
            onClick={() => void act(b, 'verify')}
          >
            {t('nx.plat.raVerify')}
          </Button>
        </span>
      ),
    },
  ];

  if (error) return <ErrorState error={error} onRetry={() => void refetch()} />;

  return (
    <>
      <PageHeader
        title={t('nx.plat.raTitle')}
        description={t('nx.plat.raSubtitle')}
      />

      <Panel className="mb-4">
        <p className="text-body text-muted">{t('nx.plat.raGovernance')}</p>
      </Panel>

      <FormError message={actionError} className="mb-4" />
      {note ? (
        <p className="mb-4 text-body text-positive-fg" role="status">
          {note}
        </p>
      ) : null}

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Select
          aria-label={t('nx.plat.raFilterCountry')}
          value={country}
          onChange={(e) => setCountry(e.target.value)}
        >
          <option value="">{t('nx.plat.raAllCountries')}</option>
          {[...new Set(all.map((b) => b.country))].sort().map((c) => (
            <option key={c} value={c}>
              {c.toUpperCase()}
            </option>
          ))}
        </Select>
      </div>

      {isLoading && !data ? <TableSkeleton columns={6} /> : null}

      {!isLoading && rows.length === 0 ? (
        <EmptyState
          icon={FileSpreadsheet}
          title={t('nx.plat.raEmptyTitle')}
          description={t('nx.plat.raEmptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable<RateBatch>
          rows={rows}
          columns={columns}
          rowKey={keyOf}
          caption={t('nx.plat.raCaption')}
        />
      ) : null}
    </>
  );
}

export default function RatesPage() {
  return (
    <RequireWorkspace workspace="platform">
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <RatesScreen />
      </Suspense>
    </RequireWorkspace>
  );
}
