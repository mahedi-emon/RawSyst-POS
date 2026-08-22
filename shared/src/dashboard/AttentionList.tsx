// What needs doing.
//
// Most severe first, and compliance always above convenience: an unreported
// invoice has a legal deadline attached to it, a low stock level has an
// inconvenience. The server sorts nothing — it reports facts — so the ordering
// decision lives here, where the product's priorities belong.
//
// # Severity is never colour alone
//
// Each row carries a word and a stripe. Roughly 1 in 12 men has a colour vision
// deficiency, and this list is the one place on the screen where missing a row
// has consequences.
//
// # An empty list is a good day
//
// It says so, plainly, rather than rendering nothing. A panel that vanishes
// when healthy leaves the reader unsure whether it is empty or broken.

import type { Attention } from '../api/dashboard';
import { sortAttention } from './logic';
import type { DrillTarget } from './Dashboard';
import { targetForLink } from './drilldown';
import { useT } from '../i18n/locale';


const LABEL: Record<Attention['severity'], string> = {
  critical: 'Urgent',
  warning: 'Needs attention',
  notice: 'Worth knowing',
};

export function AttentionList({
  items,
  onOpen,
}: {
  items: Attention[];
  onOpen: (target: DrillTarget) => void;
}) {
  const t = useT();
  const sorted = sortAttention(items);

  return (
    <section className="ds-panel attention" aria-label={t('dash.needsAttention')}>
      <div className="ds-panel__head">
        <h2 className="ds-h3">{t('dash.needsAttention')}</h2>
        {sorted.length > 0 && (
          <span className="ds-caption">{sorted.length} item{sorted.length === 1 ? '' : 's'}</span>
        )}
      </div>

      <div className="ds-panel__body">
        {sorted.length === 0 ? (
          <div className="ds-state">
            <p className="ds-state__title">{t('dash.nothingNeedsAttention')}</p>
            <p className="ds-state__body">
              {t('dash.allHealthy')}
            </p>
          </div>
        ) : (
          <ul className="attention__list">
            {sorted.map((item, i) => (
              <li
                className={`attention__row attention__row--${item.severity}`}
                key={`${item.kind}-${i}`}
              >
                <div className="attention__main">
                  <span className="attention__title">
                    {item.title}
                    {item.count > 0 && (
                      <span className="attention__count num">{item.count}</span>
                    )}
                  </span>
                  <span className="attention__detail ds-body-sm ds-muted">
                    {item.detail}
                  </span>
                </div>

                <div className="attention__side">
                  {/* The severity in words, so the stripe is reinforcement
                      rather than the only signal. */}
                  <span className={`ds-badge ${badge(item.severity)}`}>
                    {LABEL[item.severity] ?? item.severity}
                  </span>

                  {/* Every row that has somewhere to go, goes there. A8. */}
                  {targetForLink(item.link) && (
                    <button
                      className="ds-btn ds-btn--quiet attention__open"
                      onClick={() => {
                        const target = targetForLink(item.link);
                        if (target) onOpen(target);
                      }}
                    >
                      {t('dash.open')}
                    </button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

function badge(severity: Attention['severity']): string {
  switch (severity) {
    case 'critical':
      return 'ds-badge--danger';
    case 'warning':
      return 'ds-badge--warning';
    default:
      return 'ds-badge--neutral';
  }
}
