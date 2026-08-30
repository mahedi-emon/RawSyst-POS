// The picking slip, the packing slip and the delivery note (blueprint B11).
//
// # No prices, and not because this file declines to draw them
//
// `OrderDocument` has no price fields. The server does not send them and the
// type has nowhere to put them, so no future edit to this file can put a total
// on a delivery note by accident. That paper is handled by a picker, a driver,
// a courier and whoever signs for the goods, and none of them are owed the
// margin.
//
// # It prints from the browser
//
// A sheet laid out for A4 with everything else on the page hidden by the print
// stylesheet, rather than a PDF the server renders. A warehouse prints this
// twenty times a day on whatever is nearest, and the browser's own dialogue is
// the one they already know.

import type { OrderDocument } from '../api/orders';
import { useLocale, useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';
import { shortDate } from '../ui/format';

export function DocumentSheet({
  sheet,
  onClose,
}: {
  sheet: OrderDocument;
  onClose: () => void;
}) {
  const t = useT();
  const { locale } = useLocale();

  return (
    <section className="ds-panel sheet" aria-label={t(`orders.document.${sheet.kind}` as Key)}>
      <div className="ds-panel__head sheet__bar">
        <h2 className="ds-h3">{t(`orders.document.${sheet.kind}` as Key)}</h2>
        <div className="orders__actions">
          <button className="ds-btn ds-btn--primary" onClick={() => window.print()}>
            {t('action.print')}
          </button>
          <button className="ds-btn ds-btn--quiet" onClick={onClose}>
            {t('action.close')}
          </button>
        </div>
      </div>

      <div className="ds-panel__body sheet__page">
        <header className="sheet__head">
          <h3 className="ds-h2">{t(`orders.document.${sheet.kind}` as Key)}</h3>
          <p className="sheet__no">{sheet.order_no}</p>
        </header>

        <dl className="sheet__facts">
          {sheet.customer && (
            <div>
              <dt>{t('orders.customer')}</dt>
              <dd>{sheet.customer}</dd>
            </div>
          )}
          {sheet.deliver_to && (
            <div>
              <dt>{t('orders.deliverTo')}</dt>
              <dd>{sheet.deliver_to}</dd>
            </div>
          )}
          {sheet.deliver_phone && (
            <div>
              <dt>{t('orders.deliverPhone')}</dt>
              <dd>{sheet.deliver_phone}</dd>
            </div>
          )}
          {sheet.store && (
            <div>
              <dt>{t('orders.from')}</dt>
              <dd>{sheet.store}</dd>
            </div>
          )}
          <div>
            <dt>{t('orders.printed')}</dt>
            <dd>{shortDate(sheet.printed_at, locale)}</dd>
          </div>
        </dl>

        <table className="ds-table sheet__lines">
          <thead>
            <tr>
              <th scope="col">#</th>
              <th scope="col">{t('orders.item')}</th>
              {sheet.kind === 'picking' && <th scope="col">{t('orders.where')}</th>}
              <th scope="col" className="num">
                {t('orders.qty')}
              </th>
            </tr>
          </thead>
          <tbody>
            {sheet.lines.map((l) => (
              <tr key={l.line_no}>
                <td>{l.line_no}</td>
                <td>
                  <span className="detail__strong">{l.product}</span>
                  <span className="ds-caption">
                    {l.barcode ? `${l.sku} · ${l.barcode}` : l.sku}
                  </span>
                  {l.description && <span className="ds-caption">{l.description}</span>}
                </td>
                {sheet.kind === 'picking' && <td>{l.location ?? '—'}</td>}
                <td className="num">{l.qty}</td>
              </tr>
            ))}
          </tbody>
        </table>

        {sheet.note && <p className="sheet__note">{sheet.note}</p>}

        {/* A delivery note nobody signed is a delivery nobody can prove. */}
        {sheet.kind === 'delivery' && (
          <div className="sheet__signature">
            <span>{t('orders.receivedBy')}</span>
            <span className="sheet__rule" aria-hidden="true" />
            <span>{t('orders.signedOn')}</span>
            <span className="sheet__rule" aria-hidden="true" />
          </div>
        )}
      </div>
    </section>
  );
}
