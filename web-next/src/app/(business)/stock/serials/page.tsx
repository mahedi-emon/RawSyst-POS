'use client';

// Serial numbers, and what the warranty desk needs from one.
//
// # This screen is a lookup before it is a list
//
// Somebody standing at a counter with a returned unit has a serial number in
// their hand and one question: is this still under warranty. So the search is
// the top of the screen and answers on its own, and the list below is for
// everything else.
//
// # The warranty answer is the server's
//
// `under_warranty` is derived from the date rather than stored, because "a flag
// would be wrong every morning until a job ran, and the warranty desk is
// exactly where a stale answer costs the shop money". Nothing here recomputes
// it from `warranty_until` — a second answer that could disagree with the first
// is the one thing this screen must not produce.
//
// # Four states, not two
//
// Covered, expired, sold-with-no-warranty, and never sold. The last two look
// identical if you only ask "is it in warranty", and they lead to completely
// different conversations with the customer.

import { ScanBarcode } from 'lucide-react';
import { Suspense, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { Field, Input } from '@/components/ui/field';
import { FormError } from '@/components/ui/form-error';
import { Badge, PageHeader, Panel } from '@/components/ui/panel';
import { EmptyState, ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { api } from '@/lib/api/client';
import { messageFor } from '@/lib/api/errors';
import { useApiList } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { warrantyState, type Serial } from '@/lib/devices/hardware';
import { useT, type Key } from '@/lib/i18n/locale';
import { useUrlState } from '@/lib/url-state';

const WARRANTY_LABEL: Record<string, Key> = {
  covered: 'nx.ser.covered',
  expired: 'nx.ser.expired',
  none: 'nx.ser.noWarranty',
  unsold: 'nx.ser.unsold',
};

const WARRANTY_TONE: Record<string, 'positive' | 'critical' | 'neutral' | 'info'> = {
  covered: 'positive',
  expired: 'critical',
  none: 'neutral',
  unsold: 'info',
};

/** One unit, answered in full. This is what the counter actually reads. */
function Found({ serial }: { serial: Serial }) {
  const t = useT();
  const state = warrantyState(serial);
  return (
    <Panel
      className="mb-6"
      title={serial.product || serial.sku || serial.serial_no}
      actions={
        <Badge tone={WARRANTY_TONE[state]}>{t(WARRANTY_LABEL[state] as Key)}</Badge>
      }
    >
      <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div>
          <dt className="text-label text-muted">{t('nx.ser.serialNo')}</dt>
          <dd className="num mt-0.5 text-body">{serial.serial_no}</dd>
        </div>
        <div>
          <dt className="text-label text-muted">{t('nx.ser.sku')}</dt>
          <dd className="num mt-0.5 text-body">{serial.sku || '—'}</dd>
        </div>
        <div>
          <dt className="text-label text-muted">{t('nx.ser.state')}</dt>
          <dd className="mt-0.5 text-body">{serial.status}</dd>
        </div>
        <div>
          <dt className="text-label text-muted">{t('nx.ser.soldOn')}</dt>
          <dd className="num mt-0.5 text-body">
            {serial.sold_at || t('nx.ser.notSold')}
          </dd>
        </div>
        <div>
          <dt className="text-label text-muted">{t('nx.ser.warrantyUntil')}</dt>
          <dd className="num mt-0.5 text-body">{serial.warranty_until || '—'}</dd>
        </div>
        <div>
          <dt className="text-label text-muted">{t('nx.ser.soldTo')}</dt>
          <dd className="mt-0.5 text-body">{serial.customer || '—'}</dd>
        </div>
        <div>
          <dt className="text-label text-muted">{t('nx.ser.suppliedBy')}</dt>
          <dd className="mt-0.5 text-body">{serial.supplier || '—'}</dd>
        </div>
      </dl>

      {state === 'unsold' ? (
        // Not the same as "no warranty": this one has not been sold, so the
        // clock has not started and the conversation is a different one.
        <p className="mt-4 max-w-prose text-caption text-muted">
          {t('nx.ser.unsoldHint')}
        </p>
      ) : null}
      {state === 'none' ? (
        <p className="mt-4 max-w-prose text-caption text-muted">
          {t('nx.ser.noWarrantyHint')}
        </p>
      ) : null}
    </Panel>
  );
}

function SerialsScreen() {
  const t = useT();
  const scope = useCompanyScope();

  const [query, setQuery] = useUrlState('serial', '');
  const [typed, setTyped] = useState(query);
  const [found, setFound] = useState<Serial | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [missing, setMissing] = useState(false);

  const serials = useApiList<Serial>(scope ? '/serials' : null, scope ?? undefined);
  const rows = serials.data?.data ?? [];

  async function lookUp() {
    const no = typed.trim();
    if (!scope || no === '') return;
    setBusy(true);
    setError(null);
    setMissing(false);
    setFound(null);
    try {
      const out = await api.get<Serial>(
        `/serials/${encodeURIComponent(no)}?company_id=${scope.company_id}`,
      );
      setFound(out);
      setQuery(no);
    } catch (e) {
      // A serial nobody has on file is an ordinary answer at a counter, not a
      // fault: somebody has read a number off a unit this shop never sold.
      const message = messageFor(e, t);
      if (/not.*(found|on file)/i.test(message)) setMissing(true);
      else setError(message);
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Serial>[] = [
    {
      key: 'serial',
      header: t('nx.ser.colSerial'),
      primary: true,
      cell: (s) => (
        <span className="flex flex-col gap-0.5">
          <span className="num font-medium">{s.serial_no}</span>
          <span className="text-caption text-muted">{s.product || s.sku || ''}</span>
        </span>
      ),
    },
    {
      key: 'status',
      header: t('nx.ser.colStatus'),
      width: 'w-32',
      cell: (s) => <span>{s.status}</span>,
    },
    {
      key: 'sold',
      header: t('nx.ser.colSold'),
      secondary: true,
      width: 'w-40',
      cell: (s) => (
        <span className="flex flex-col gap-0.5">
          <span className="num text-muted">{s.sold_at || '—'}</span>
          {s.customer ? (
            <span className="text-caption text-muted">{s.customer}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'warranty',
      header: t('nx.ser.colWarranty'),
      width: 'w-44',
      cell: (s) => {
        const state = warrantyState(s);
        return (
          <span className="flex flex-col gap-1">
            <Badge tone={WARRANTY_TONE[state]}>
              {t(WARRANTY_LABEL[state] as Key)}
            </Badge>
            {s.warranty_until ? (
              <span className="num text-caption text-muted">{s.warranty_until}</span>
            ) : null}
          </span>
        );
      },
    },
  ];

  return (
    <>
      <PageHeader title={t('nx.ser.title')} description={t('nx.ser.subtitle')} />

      <FormError message={error} className="mb-4" />

      {/* The lookup leads, because somebody at a counter has a number in their
          hand and one question. */}
      <Panel className="mb-6" title={t('nx.ser.lookUpTitle')}>
        <div className="flex flex-wrap items-end gap-3">
          <Field name="serial_no" label={t('nx.ser.serialNo')}>
            <Input
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              onKeyDown={(e) => {
                // A barcode scanner types the number and presses Enter.
                if (e.key === 'Enter') void lookUp();
              }}
              className="num"
              autoComplete="off"
            />
          </Field>
          <Button
            variant="primary"
            disabled={busy || typed.trim() === ''}
            onClick={() => void lookUp()}
          >
            {t('nx.ser.lookUp')}
          </Button>
        </div>
        {missing ? (
          <p className="mt-3 max-w-prose text-body text-muted" role="status">
            {t('nx.ser.notOnFile', { serial: typed.trim() })}
          </p>
        ) : null}
      </Panel>

      {found ? <Found serial={found} /> : null}

      <h2 className="mb-3 text-card-title font-semibold text-fg">
        {t('nx.ser.allTitle')}
      </h2>

      {serials.error ? (
        <ErrorState error={serials.error} onRetry={() => void serials.refetch()} />
      ) : null}
      {serials.isLoading && !serials.data ? <TableSkeleton columns={4} /> : null}

      {!serials.isLoading && !serials.error && rows.length === 0 ? (
        <EmptyState
          icon={ScanBarcode}
          title={t('nx.ser.emptyTitle')}
          description={t('nx.ser.emptyDesc')}
        />
      ) : null}

      {rows.length > 0 ? (
        <DataTable
          caption={t('nx.ser.allTitle')}
          columns={columns}
          rows={rows}
          rowKey={(s) => s.id}
        />
      ) : null}
    </>
  );
}

export default function SerialsPage() {
  return (
    <RequirePermission anyOf={['serial.view']}>
      <Suspense fallback={<div className="h-64" aria-busy="true" />}>
        <SerialsScreen />
      </Suspense>
    </RequirePermission>
  );
}
