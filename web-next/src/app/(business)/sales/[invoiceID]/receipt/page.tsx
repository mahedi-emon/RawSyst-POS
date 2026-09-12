'use client';

// The receipt a customer walks out with.
//
// # Why this screen had to be built
//
// The product could ring up a sale, chain it for tax, and record a reprint in
// the audit trail — and it could not PRINT anything. `POST /pos/sales/{id}/reprint`
// writes the audit entry and its own comment says it "is the CONTROL rather
// than the printing"; the till's completion dialog says printing "lives on the
// invoice screen"; and there was no invoice screen. A point-of-sale product
// that cannot produce a receipt is not finished, however much else works.
//
// # What a receipt has to carry
//
// Everything a customer or an inspector would look for, which is more than a
// list of prices:
//
//   - the seller: trading name, the BRANCH's address and telephone, VAT number
//   - the document: its number, when it was issued, and who served them
//   - the goods: description, quantity, unit price, discount, tax, line total
//   - the money: net, discount, tax, total, what was tendered, change given
//   - the tax: broken out by rate, because that is what a VAT receipt is for
//   - the chain: the ZATCA QR where the market requires one
//   - the shop's own words: header, footer, return policy
//
// # Printing, and the two paper widths
//
// `window.print()` rather than a PDF, for the reason the warehouse documents
// give next door: it works on a tablet with no printer driver, and the browser
// already knows how to reach the printer somebody has.
//
// The print stylesheet is 72mm wide, which is the printable area of the 80mm
// roll almost every counter thermal printer takes, and degrades to A4 without
// complaint. Everything that is not the receipt is hidden at print time rather
// than being a separate route, so what somebody sees is what comes out.

import { ArrowLeft, Printer } from 'lucide-react';
import Link from 'next/link';
import { use, useEffect, useState } from 'react';

import { RequirePermission } from '@/components/auth/guard';
import { Button } from '@/components/ui/button';
import { ErrorState, Skeleton } from '@/components/ui/states';
import { api } from '@/lib/api/client';
import { useApi } from '@/lib/api/hooks';
import { useCompany } from '@/lib/company/company-context';
import { useT } from '@/lib/i18n/locale';
import { formatMoney } from '@/lib/format/money';
import { QRCode } from '@biz1core/shared/security/QRCode';

interface Line {
  line_no: number;
  description: string;
  qty: string;
  unit_price: string;
  line_discount: string;
  invoice_discount_alloc: string;
  tax_rate: string;
  tax_amount: string;
  net_amount: string;
  gross_amount: string;
}

interface Tender {
  method: string;
  amount: string;
  reference?: string | null;
}

interface Sale {
  id: string;
  uuid: string;
  doc_type: string;
  human_number: string | null;
  state: string;
  issue_date: string;
  currency: string;
  subtotal_net: string;
  discount_total: string;
  tax_total: string;
  total_inclusive: string;
  lines: Line[];
  tenders: Tender[];
  // `qr_tlv` is base64 TLV, which is DATA and not a picture: it has to be
  // encoded into a QR here. Treating it as an image source would have printed
  // a broken icon on every Saudi receipt.
  zatca?: { qr_tlv?: string | null } | null;
  // Only an id and a name. There is no customer VAT number on this view, so
  // the receipt does not pretend to print one.
  customer?: { id: string; name: string } | null;
  // Who served them comes from the trail rather than a column of its own.
  audit?: { action: string; actor_label?: string | null }[];
}

interface Stationery {
  store_name: string;
  vat_number: string;
  base_currency: string;
  header_text: string;
  footer_text: string;
  return_policy: string;
  show_tax_number: boolean;
  store_address: string;
  store_phone: string;
  // Optional, and only for this screen. `/pos/stationery` serves two callers:
  // the till, which prints 42 columns of plain text and is deliberately not
  // sent these, and this screen, which prints HTML through the browser and is.
  // A browser session is never a device, so they are always present here —
  // typed optional because the payload genuinely omits them for the other
  // caller, and pretending otherwise would be a lie the compiler believes.
  show_logo?: boolean;
  company_id?: string;
}

function ReceiptScreen({ invoiceID }: { invoiceID: string }) {
  const t = useT();
  const { company } = useCompany();
  // The `?` is written out rather than carried in `scope`, and that is not
  // cosmetic. `make reach` decides which routes a screen can reach by reading
  // these paths, and it splits the query off at a literal `?`; with the query
  // hidden inside a variable the last segment reads as an interpolation, the
  // call matches `/pos/ANYTHING`, and the audit reported this screen as
  // reaching `/pos/stock` — a route no screen touches at all.
  const scope = company ? `company_id=${company.id}` : '';

  // Neither call means anything without a company: the API resolves one from a
  // registered device or from this parameter, and a browser is not a device.
  const sale = useApi<Sale>(
    company ? `/pos/sales/${invoiceID}?${scope}` : null,
  );
  const paper = useApi<Stationery>(company ? `/pos/stationery?${scope}` : null);

  // A second copy of a tax invoice in circulation is something an inspector
  // asks about, so printing again is recorded. The FIRST render is not a
  // reprint — somebody arriving here from the till has not printed yet.
  const [printedOnce, setPrintedOnce] = useState(false);

  // The shop's own logo, from the route that actually serves the bytes.
  //
  // This was `<img src="/api/v1/companies/{id}/logo">`, and it was wrong twice.
  //
  // `GET .../logo` is the METADATA — the router's own note says "what is set,
  // without the bytes" — so the image element was handed a JSON document and
  // rendered a broken-image box. `GET .../logo/image` is the file, and its note
  // says it is "destined for every receipt it prints". The bytes route is also
  // merely AUTHENTICATED while the metadata route needs `identity.view`, which
  // a Cashier does not hold: printing a receipt from the till would have been
  // refused outright even once the URL was right. `shared/api/branding.ts`
  // already has `logoImagePath` for this, and the Business settings screen
  // already used it; only this screen did not.
  //
  // And it is fetched rather than pointed at, because the API reads the bearer
  // header and nothing else, and a browser puts no Authorization header on an
  // image request. `api.download` is the call that exists for exactly that, and
  // its own note describes the failure.
  //
  // The object URL is a live reference into the document's memory and has to be
  // revoked, or printing a run of receipts leaks a blob each time.
  const [logoURL, setLogoURL] = useState<string | null>(null);
  const showLogo = paper.data?.show_logo ?? false;
  const logoCompany = paper.data?.company_id;
  useEffect(() => {
    if (!showLogo || !logoCompany) {
      setLogoURL(null);
      return;
    }
    let made: string | null = null;
    let cancelled = false;
    void api
      // The `?` is written out for the reason `scope` is: `make reach` splits
      // a path at a literal question mark, and a query hidden in a variable
      // makes this read as `/companies/ANYTHING`.
      .download(`/companies/${logoCompany}/logo/image?company_id=${logoCompany}`)
      .then(({ blob }) => {
        // A company whose stationery says "print the logo" but which has never
        // uploaded one answers with no bytes rather than an error. Handing
        // those to an <img> renders a browser's broken-image box on the
        // receipt, which is worse than the nothing it is standing in for.
        if (blob.size === 0 || !blob.type.startsWith('image/')) {
          if (!cancelled) setLogoURL(null);
          return;
        }
        const url = URL.createObjectURL(blob);
        if (cancelled) {
          URL.revokeObjectURL(url);
          return;
        }
        made = url;
        setLogoURL(url);
      })
      .catch(() => {
        // A logo that will not load is cosmetic; a receipt that will not print
        // is not. The shop's NAME is directly beneath it and is what identifies
        // the document, so this falls back to no picture rather than to an
        // error.
        if (!cancelled) setLogoURL(null);
      });
    return () => {
      cancelled = true;
      if (made) URL.revokeObjectURL(made);
    };
  }, [showLogo, logoCompany]);

  const s = sale.data;
  const p = paper.data;

  const money = (v: string | null | undefined) =>
    formatMoney(v ?? null, {
      currency: s?.currency ?? p?.base_currency ?? 'SAR',
      bare: true,
    });

  async function print() {
    if (printedOnce && company) {
      // Best effort: a failed audit write must not stop somebody handing a
      // customer their receipt. It is recorded, or it is not, and the printing
      // happens either way.
      try {
        await api.post(
          `/pos/sales/${invoiceID}/reprint?company_id=${company.id}`,
          {},
        );
      } catch {
        /* the receipt still prints */
      }
    }
    setPrintedOnce(true);
    window.print();
  }

  useEffect(() => {
    document.title = s?.human_number
      ? `${p?.store_name ?? ''} ${s.human_number}`.trim()
      : document.title;
  }, [s?.human_number, p?.store_name]);

  if (sale.error) {
    return <ErrorState error={sale.error} onRetry={() => void sale.refetch()} />;
  }
  if (!s || !p) return <Skeleton className="h-96 w-full" />;

  /** Tax broken out by rate, which is what makes it a VAT receipt. */
  const byRate = new Map<string, number>();
  for (const l of s.lines) {
    const amount = Number(l.tax_amount);
    if (!amount) continue;
    byRate.set(l.tax_rate, (byRate.get(l.tax_rate) ?? 0) + amount);
  }

  // Who served them. The invoice carries a trail rather than a cashier column,
  // and the entry that records the sale is the one with a name on it.
  const servedBy = s.audit?.find((a) => a.actor_label)?.actor_label ?? null;

  return (
    <>
      {/* Screen furniture. None of it prints. */}
      <div className="mb-4 flex flex-wrap items-center gap-2 print:hidden">
        <Button asChild variant="ghost" size="sm">
          <Link href="/sales">
            <ArrowLeft className="size-4 rtl:rotate-180" aria-hidden />
            {t('nx.slip.back')}
          </Link>
        </Button>
        <Button size="sm" onClick={() => void print()}>
          <Printer className="size-4" aria-hidden />
          {printedOnce ? t('nx.slip.printAgain') : t('nx.slip.print')}
        </Button>
        {printedOnce ? (
          <span className="text-caption text-subtle">
            {t('nx.slip.reprintRecorded')}
          </span>
        ) : null}
      </div>

      {/* The document. 72mm is the printable width of an 80mm roll; on A4 it
          simply sits at the top left, which is what a counter expects. */}
      <article
        className="mx-auto w-full max-w-[72mm] bg-surface p-4 text-fg print:max-w-none print:bg-white print:p-0"
        aria-label={t('nx.slip.title')}
      >
        <header className="text-center">
          {logoURL ? (
            // eslint-disable-next-line @next/next/no-img-element -- these are
            // bytes already in memory, which next/image cannot take.
            <img
              src={logoURL}
              alt=""
              className="mx-auto mb-2 max-h-16 w-auto"
            />
          ) : null}
          <p className="text-body font-semibold">{p.store_name}</p>
          {p.store_address ? (
            <p className="text-caption text-muted">{p.store_address}</p>
          ) : null}
          {p.store_phone ? (
            <p dir="ltr" className="num text-caption text-muted">
              {p.store_phone}
            </p>
          ) : null}
          {p.show_tax_number && p.vat_number ? (
            <p dir="ltr" className="num text-caption text-muted">
              {t('nx.slip.vatNumber')}: {p.vat_number}
            </p>
          ) : null}
          {p.header_text ? (
            <p className="mt-2 text-caption">{p.header_text}</p>
          ) : null}
        </header>

        <dl className="mt-3 border-t border-dashed border-line pt-2 text-caption">
          <Row label={t('nx.slip.number')} value={s.human_number || s.id.slice(0, 8)} />
          <Row label={t('nx.slip.date')} value={s.issue_date} />
          {servedBy ? (
            <Row label={t('nx.slip.servedBy')} value={servedBy} />
          ) : null}
          {s.customer?.name ? (
            <Row label={t('nx.slip.customer')} value={s.customer.name} />
          ) : null}
        </dl>

        <table className="mt-3 w-full border-t border-dashed border-line pt-2 text-caption">
          <caption className="sr-only">{t('nx.slip.itemsCaption')}</caption>
          <thead>
            <tr className="text-start">
              <th scope="col" className="py-1 text-start font-medium">
                {t('nx.slip.item')}
              </th>
              <th scope="col" className="py-1 text-end font-medium">
                {t('nx.slip.qty')}
              </th>
              <th scope="col" className="py-1 text-end font-medium">
                {t('nx.slip.amount')}
              </th>
            </tr>
          </thead>
          <tbody>
            {s.lines.map((l) => (
              <tr key={l.line_no} className="align-top">
                <td className="py-1">
                  {l.description}
                  {/* The unit price belongs under the name, not in a column of
                      its own: three columns is what fits on 72mm. */}
                  <span className="block text-subtle">
                    {money(l.unit_price)}
                    {Number(l.line_discount) > 0
                      ? ` − ${money(l.line_discount)}`
                      : ''}
                  </span>
                </td>
                <td className="num py-1 text-end tabular-nums">{l.qty}</td>
                <td className="num py-1 text-end tabular-nums">
                  {money(l.gross_amount)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>

        <dl className="mt-3 border-t border-dashed border-line pt-2 text-caption">
          <Row label={t('nx.slip.net')} value={money(s.subtotal_net)} />
          {Number(s.discount_total) > 0 ? (
            <Row label={t('nx.slip.discount')} value={money(s.discount_total)} />
          ) : null}
          {[...byRate.entries()].map(([rate, amount]) => (
            <Row
              key={rate}
              label={
                rate
                  ? t('nx.slip.taxAtRate', { rate })
                  : t('nx.slip.tax')
              }
              value={money(String(amount.toFixed(2)))}
            />
          ))}
          <div className="mt-1 flex justify-between border-t border-line pt-1 text-body font-semibold">
            <dt>{t('nx.slip.total')}</dt>
            <dd className="num tabular-nums">{money(s.total_inclusive)}</dd>
          </div>
        </dl>

        {s.tenders.length > 0 ? (
          <dl className="mt-3 border-t border-dashed border-line pt-2 text-caption">
            {s.tenders.map((tender, i) => (
              <Row
                key={`${tender.method}-${i}`}
                label={tender.method}
                value={money(tender.amount)}
              />
            ))}
          </dl>
        ) : null}

        {/* The chain. Required on a Saudi simplified invoice and absent
            everywhere it is not, rather than printed as an empty box. */}
        {s.zatca?.qr_tlv ? (
          <div className="mt-4" aria-label={t('nx.slip.qrAlt')}>
            {/* Inline SVG, drawn from the TLV the server signed. It prints at
                whatever size the roll gives it without going soft, which a
                bitmap at 203dpi would not. */}
            <div className="mx-auto w-32">
              <QRCode value={s.zatca.qr_tlv} />
            </div>
          </div>
        ) : null}

        <footer className="mt-4 border-t border-dashed border-line pt-2 text-center text-caption text-muted">
          {p.return_policy ? <p>{p.return_policy}</p> : null}
          {p.footer_text ? <p className="mt-1">{p.footer_text}</p> : null}
        </footer>
      </article>
    </>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-3 py-0.5">
      <dt className="text-muted">{label}</dt>
      <dd className="num tabular-nums">{value}</dd>
    </div>
  );
}

export default function ReceiptPage({
  params,
}: {
  params: Promise<{ invoiceID: string }>;
}) {
  const { invoiceID } = use(params);
  return (
    <RequirePermission anyOf={['sales.view']}>
      <ReceiptScreen invoiceID={invoiceID} />
    </RequirePermission>
  );
}
