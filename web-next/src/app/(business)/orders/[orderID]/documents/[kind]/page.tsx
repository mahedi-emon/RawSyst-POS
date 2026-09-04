'use client';

// One of B11's three warehouse documents.
//
// # Three documents, three jobs
//
// A picking slip says what to take off the shelves and where each is kept; a
// packing slip is what to check against before the box is sealed; a delivery
// note goes in the box. They are not three names for one printout, so each says
// which job it is for.
//
// # No prices, and not by omission
//
// B11 requires the delivery note itemised without pricing, and the route note
// adds that "the type it is built from has no price fields at all so no screen
// can put them back". None of the three carries a price, and the page says so
// rather than leaving somebody wondering whether it failed to load them.
//
// # It is a page, not a download
//
// The route answers JSON. A link straight to it would show a customer raw JSON,
// so this renders it and hands the printing to the browser — which is also what
// makes it work on the warehouse tablet that has no printer driver.

import { Printer } from 'lucide-react';
import Link from 'next/link';
import { use } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { PageHeader, Panel } from '@/components/ui/panel';
import { ErrorState } from '@/components/ui/states';
import { DataTable, TableSkeleton, type Column } from '@/components/ui/table';
import { useApi } from '@/lib/api/hooks';
import { useCompanyScope } from '@/lib/company/company-context';
import { formatQuantity } from '@/lib/format/money';
import { useT, type Key } from '@/lib/i18n/locale';

interface DocumentLine {
  line_no: number;
  sku: string;
  barcode?: string;
  product: string;
  description?: string;
  /** Where it is kept, on a picking slip. */
  location?: string;
  qty: string;
}

interface OrderDocument {
  kind: string;
  order_no: string;
  customer?: string;
  deliver_to?: string;
  deliver_phone?: string;
  store?: string;
  printed_at: string;
  lines: DocumentLine[];
  note?: string;
}

/** The three the route draws. Anything else is refused by name. */
const DOCUMENT: Record<string, { title: Key; hint: Key }> = {
  picking: { title: 'nx.doc.picking', hint: 'nx.doc.pickingHint' },
  packing: { title: 'nx.doc.packing', hint: 'nx.doc.packingHint' },
  delivery: { title: 'nx.doc.delivery', hint: 'nx.doc.deliveryHint' },
};

function DocumentScreen({ orderID, kind }: { orderID: string; kind: string }) {
  const t = useT();
  const scope = useCompanyScope();
  const named = DOCUMENT[kind];

  const { data, isLoading, error, refetch } = useApi<OrderDocument>(
    scope && named ? `/orders/${orderID}/documents/${kind}` : null,
    scope ?? undefined,
  );

  if (!named || error) {
    return (
      <>
        <PageHeader title={t('nx.doc.notFound')} />
        {error ? <ErrorState error={error} onRetry={() => void refetch()} /> : null}
        <Link
          href={`/orders/${orderID}`}
          className="mt-4 inline-block text-label text-muted underline underline-offset-4 hover:text-fg"
        >
          {t('nx.doc.backToOrder')}
        </Link>
      </>
    );
  }

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title={t(named.title)} />
        <TableSkeleton columns={4} />
      </>
    );
  }

  const columns: Column<DocumentLine>[] = [
    {
      key: 'no',
      header: t('nx.doc.colLine'),
      width: 'w-16',
      cell: (l) => <span className="num text-muted">{l.line_no}</span>,
    },
    {
      key: 'item',
      header: t('nx.doc.colItem'),
      primary: true,
      cell: (l) => (
        <span className="flex flex-col gap-0.5">
          <span>{l.product}</span>
          <span className="num text-caption text-muted">
            {[l.sku, l.barcode].filter(Boolean).join(' · ')}
          </span>
        </span>
      ),
    },
    {
      key: 'where',
      header: t('nx.doc.colWhere'),
      secondary: true,
      // Filled on a picking slip and empty on the other two, which is the
      // difference between "walk to this shelf" and "check this box".
      cell: (l) => <span className="text-muted">{l.location || '—'}</span>,
    },
    {
      key: 'qty',
      header: t('nx.doc.colQty'),
      numeric: true,
      width: 'w-28',
      cell: (l) => <span className="num font-medium">{formatQuantity(l.qty)}</span>,
    },
  ];

  return (
    <>
      <PageHeader
        title={
          <span className="flex flex-wrap items-baseline gap-2">
            {t(named.title)}
            <span className="num text-body text-muted">{data.order_no}</span>
          </span>
        }
        description={[data.customer, data.deliver_to, data.deliver_phone]
          .filter(Boolean)
          .join(' · ')}
        actions={
          <div className="flex items-center gap-3 print:hidden">
            <Link
              href={`/orders/${orderID}`}
              className="text-label text-muted underline underline-offset-4 hover:text-fg"
            >
              {t('nx.doc.backToOrder')}
            </Link>
            <Button variant="secondary" onClick={() => window.print()}>
              <Printer aria-hidden="true" />
              {t('nx.doc.print')}
            </Button>
          </div>
        }
      />

      <p className="mb-4 max-w-prose text-body text-muted print:hidden">
        {t(named.hint)}
      </p>

      <Panel flush>
        <DataTable
          caption={t('nx.doc.caption')}
          columns={columns}
          rows={data.lines}
          rowKey={(l) => `${l.line_no}-${l.sku}`}
          className="rounded-none border-0"
        />
      </Panel>

      {data.note ? (
        <p className="mt-4 max-w-prose text-body text-fg">{data.note}</p>
      ) : null}

      <p className="mt-6 text-caption text-muted">
        {t('nx.doc.noPrices')}
        {' · '}
        {t('nx.doc.printedAt', { when: data.printed_at.slice(0, 16).replace('T', ' ') })}
      </p>
    </>
  );
}

export default function OrderDocumentPage({
  params,
}: {
  params: Promise<{ orderID: string; kind: string }>;
}) {
  const { orderID, kind } = use(params);
  return (
    // order.view, not order.manage: a picker and a driver both need to print
    // one, and neither should be able to change a price.
    <RequirePermission anyOf={['order.view']}>
      <DocumentScreen orderID={orderID} kind={kind} />
    </RequirePermission>
  );
}
