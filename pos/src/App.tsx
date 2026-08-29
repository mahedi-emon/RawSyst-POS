// The shell.
//
// Exactly one rule governs this file: nothing renders until a real session
// exists. There is no development bypass, no "skip login" flag and no default
// user — a till that could be opened without signing in would attribute every
// sale to nobody, and the cash session it belongs to could never be reconciled
// against a person.
//
// # Navigation is what the user may actually do
//
// The bar shows a destination only when the permission behind it is held, so a
// cashier sees Sell and nothing else while an owner sees the figures too. That
// is presentation, not security: every route is gated server-side and QA gate
// M7 proves it. But it matters for usability — a nav full of items that refuse
// you teaches people to distrust the whole bar.

import { useCallback, useEffect, useMemo, useState } from 'react';

import { LoginScreen } from '@rawsyst/shared/auth/LoginScreen';
import { LanguageSwitch } from '@rawsyst/shared/i18n/LanguageSwitch';
import { ThemeSwitch } from '@rawsyst/shared/ui/ThemeSwitch';
import { useT } from '@rawsyst/shared/i18n/locale';
import { useAuth } from '@rawsyst/shared/auth/session';
import { listCompanies, type Company } from '@rawsyst/shared/api/companies';
import { Dashboard, type DrillTarget } from '@rawsyst/shared/dashboard/Dashboard';
import { SalesDetailScreen } from '@rawsyst/shared/dashboard/SalesDetailScreen';
import { ExpensesDetailScreen } from '@rawsyst/shared/dashboard/ExpensesDetailScreen';
import { ComplianceScreen } from '@rawsyst/shared/dashboard/ComplianceScreen';
import { StockScreen } from '@rawsyst/shared/dashboard/StockScreen';
import { InvoiceDetailScreen } from '@rawsyst/shared/invoices/InvoiceDetailScreen';
import { PurchasingScreen } from '@rawsyst/shared/purchasing/PurchasingScreen';
import { PosCounter } from './pos/PosCounter';
import { ReturnsScreen } from './pos/ReturnsScreen';
import { ShiftScreen } from './pos/ShiftScreen';
import { TerminalBanner } from './pos/TerminalBanner';
import { terminalCapabilities, type Capabilities } from './pos/terminal';
import { PairingScreen, TerminalBlocked } from './pos/PairingScreen';
import {
  available as keystoreAvailable,
  faultFrom,
  identify,
  isPaired,
  type PairingFault,
  type TerminalIdentity,
} from './offline/credential';

type Screen = 'dashboard' | 'sell' | 'return' | 'buying' | 'shift';

/** Where this machine stands with the server, before anybody signs in. */
type Pairing =
  | { state: 'checking' }
  | { state: 'unpaired' }
  | { state: 'blocked'; fault: PairingFault }
  | { state: 'ready'; identity: TerminalIdentity }
  /** Not a till at all — a browser during development, where there is no
   *  keystore to hold a credential. The counter still works against the live
   *  API; nothing is durable and nothing can be paired. */
  | { state: 'not_a_terminal' };

export function App({ apiBaseUrl }: { apiBaseUrl: string }) {
  const { status, me, signOut, client } = useAuth();
  const t = useT();
  const [caps, setCaps] = useState<Capabilities | null>(null);
  const [pairing, setPairing] = useState<Pairing>({ state: 'checking' });
  const [companies, setCompanies] = useState<Company[] | null>(null);
  const [companyId, setCompanyId] = useState<string | null>(null);

  useEffect(() => {
    terminalCapabilities().then(setCaps).catch(() => setCaps(null));
  }, []);

  // Asked on startup and re-asked on demand. The server is the authority on
  // whether this terminal may work, and it answers with WHICH state it is in —
  // revoked, switched off, or unrecognised — so the screen can say the right
  // thing rather than a generic failure.
  const checkPairing = useCallback(async () => {
    setPairing({ state: 'checking' });
    if (!(await keystoreAvailable())) {
      setPairing({ state: 'not_a_terminal' });
      return;
    }
    if (!(await isPaired())) {
      setPairing({ state: 'unpaired' });
      return;
    }
    try {
      setPairing({ state: 'ready', identity: await identify(apiBaseUrl) });
    } catch (err) {
      const fault = faultFrom(err);
      // Offline is not blocked. A till that has been paired must keep trading
      // through a dead connection — that is the whole offline-first design —
      // so an unreachable server leaves it working on what it already knows.
      //
      // `refused` is treated the same way here, and deliberately. The three
      // kinds that mean this terminal may not trade — revoked, paused,
      // unrecognised — are named above, and anything else the server happened
      // to say is a bad reason to stop a till mid-shift: a rate limit or a
      // transient fault would otherwise close a counter with a queue at it.
      setPairing(
        fault.kind === 'offline' || fault.kind === 'refused'
          ? { state: 'ready', identity: { device_id: '', terminal_label: '', store_id: '', company_id: '' } }
          : { state: 'blocked', fault },
      );
    }
  }, [apiBaseUrl]);

  useEffect(() => {
    void checkPairing();
  }, [checkPairing]);

  const may = useMemo(
    () => (permission: string) => me?.permissions.includes(permission) ?? false,
    [me],
  );

  const mayReadFigures = may('accounting.view');
  const maySell = may('sales.create');
  const mayRefund = may('sales.refund');
  const mayBuy = may('purchasing.view');
  // Opening and closing a till carries the permission the design assigns to
  // taking payment: blueprint A6.1 gives the Cashier "shift open/close" as one
  // capability alongside billing.
  const mayRunTill = may('sales.receive_payment');

  // The dashboard opens first for whoever can read the figures — that is what
  // an owner signs in for. A cashier lands on the till.
  const [screen, setScreen] = useState<Screen | null>(null);

  // Where the dashboard has drilled to, or null for the dashboard itself.
  //
  // Deliberately not a URL router. This is one authenticated surface with no
  // deep links to honour and no browser history of its own to fight, so a
  // router would add a dependency and a class of bugs to solve a problem that
  // does not exist here. The Dashboard button in each detail header is the way
  // back: A8 promises one click in, and one click out is the other half of that
  // promise — a drill-through you have to navigate out of is a trap, and people
  // stop clicking into things that trap them.
  const [drill, setDrill] = useState<DrillTarget | null>(null);

  useEffect(() => {
    if (screen !== null || !me) return;
    setScreen(mayReadFigures ? 'dashboard' : 'sell');
  }, [screen, me, mayReadFigures]);

  // The dashboard is about one legal entity, and a group can hold several. The
  // list is fetched rather than assumed because a terminal resolves its company
  // from its device and a browser has no device to resolve from.
  useEffect(() => {
    if (!me || !(mayReadFigures || mayBuy)) return;
    let cancelled = false;
    listCompanies(client)
      .then((found) => {
        if (cancelled) return;
        setCompanies(found);
        setCompanyId((current) => current ?? found[0]?.id ?? null);
      })
      .catch(() => {
        if (!cancelled) setCompanies([]);
      });
    return () => {
      cancelled = true;
    };
  }, [client, me, mayReadFigures, mayBuy]);

  // Pairing is decided BEFORE sign-in, and that order matters. A cashier who
  // signed in first and only then learned the till was revoked would have given
  // their password to a machine that cannot trade — and on a revoked terminal,
  // possibly to somebody else's machine entirely.
  if (pairing.state === 'checking' || status === 'restoring') {
    return (
      <main className="splash" aria-busy="true">
        <p>{t('till.checking')}</p>
      </main>
    );
  }

  if (pairing.state === 'unpaired') {
    return (
      <PairingScreen
        apiBaseUrl={apiBaseUrl}
        onPaired={() => void checkPairing()}
      />
    );
  }

  if (pairing.state === 'blocked') {
    const { fault } = pairing;
    return (
      <TerminalBlocked
        title={
          fault.kind === 'revoked'
            ? t('till.revoked')
            : fault.kind === 'paused'
              ? t('till.switchedOff')
              : t('till.unrecognised')
        }
        message={
          'message' in fault
            ? fault.message
            : t('till.askOwner')
        }
        onRetry={() => void checkPairing()}
        busy={false}
      />
    );
  }

  if (status !== 'signed_in' || !me) {
    return <LoginScreen />;
  }

  const destinations: Array<{ key: Screen; label: string; shown: boolean }> = [
    { key: 'dashboard', label: t('nav.dashboard'), shown: mayReadFigures },
    { key: 'sell', label: t('pos.title'), shown: maySell },
    { key: 'return', label: t('nav.returns'), shown: mayRefund },
    { key: 'shift', label: t('shift.title'), shown: mayRunTill },
    { key: 'buying', label: t('nav.buying'), shown: mayBuy },
  ];
  const visible = destinations.filter((d) => d.shown);

  return (
    <div className="app">
      <header className="app__bar">
        <span className="app__brand">
          <span className="app__mark" aria-hidden="true">
            R
          </span>
          RawSyst
        </span>

        {/* Only rendered when there is a genuine choice. A single-destination
            nav is chrome that teaches nothing. */}
        {visible.length > 1 && (
          <nav className="app__nav" aria-label={t('nav.sections')}>
            {visible.map((d) => (
              <button
                key={d.key}
                className={`app__navlink${screen === d.key ? ' app__navlink--on' : ''}`}
                aria-current={screen === d.key ? 'page' : undefined}
                onClick={() => {
                  setScreen(d.key);
                  // Leaving the section clears the drill, so coming back to
                  // the dashboard starts at the dashboard rather than three
                  // levels into wherever you were last week.
                  setDrill(null);
                }}
              >
                {d.label}
              </button>
            ))}
          </nav>
        )}

        <div className="app__spacer" />

        {/* Shown only when there is more than one company. A picker offering a
            single option is a control that cannot be used. */}
        {screen === 'dashboard' && companies && companies.length > 1 && (
          <label className="app__company">
            <span className="ds-visually-hidden">{t('nav.company')}</span>
            <select
              className="field__input"
              value={companyId ?? ''}
              onChange={(e) => setCompanyId(e.target.value)}
            >
              {companies.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.trade_name || c.legal_name}
                </option>
              ))}
            </select>
          </label>
        )}

        <ThemeSwitch />
        <LanguageSwitch />

        <button className="ds-btn ds-btn--quiet" onClick={() => void signOut()}>
          {t('nav.signOut')}
        </button>
      </header>

      {/* The terminal says plainly what it cannot do. A cashier must never be
          left assuming invoices are reaching ZATCA when signing is gated. */}
      <TerminalBanner caps={caps} />

      {screen === 'dashboard' && mayReadFigures ? (
        companyId ? (
          <DashboardArea
            companyId={companyId}
            drill={drill}
            onOpen={setDrill}
            onBack={() => setDrill(null)}
          />
        ) : (
          <NoCompany loading={companies === null} />
        )
      ) : screen === 'buying' && mayBuy ? (
        companyId ? (
          <PurchasingScreen companyId={companyId} />
        ) : (
          <NoCompany loading={companies === null} />
        )
      ) : screen === 'return' && mayRefund ? (
        <ReturnsScreen />
      ) : screen === 'shift' && mayRunTill ? (
        // Landing back on the counter after a Z report is deliberate: the till
        // is closed, and the next thing that happens is either a new shift or
        // the end of the day. Leaving the cashier on a closed-shift screen with
        // nothing to do teaches them to navigate away from it.
        <ShiftScreen onClosed={() => setScreen(maySell ? 'sell' : null)} />
      ) : maySell ? (
        <PosCounter />
      ) : (
        <NoAccess mayRefund={mayRefund} />
      )}
    </div>
  );
}

/** The dashboard and everything it drills into.
 *
 * Each detail screen is gated server-side on the permission covering the
 * records it shows — sales.view for invoices, inventory.view for stock — and
 * each renders its own permission-denied state when refused. That is the honest
 * arrangement: the client cannot know every scope on the token, so it asks and
 * reports the answer rather than pre-judging it and hiding something the user
 * was in fact allowed to see.
 */
function DashboardArea({
  companyId,
  drill,
  onOpen,
  onBack,
}: {
  companyId: string;
  drill: DrillTarget | null;
  onOpen: (target: DrillTarget) => void;
  onBack: () => void;
}) {
  if (!drill) return <Dashboard companyId={companyId} onOpen={onOpen} />;

  switch (drill.screen) {
    case 'sales':
      return (
        <SalesDetailScreen
          companyId={companyId}
          date={drill.date}
          onBack={onBack}
          onOpenInvoice={(invoiceId) => onOpen({ screen: 'invoice', invoiceId })}
        />
      );
    case 'invoice':
      return (
        <InvoiceDetailScreen
          invoiceId={drill.invoiceId}
          companyId={companyId}
          onBack={onBack}
          onOpenInvoice={(invoiceId) => onOpen({ screen: 'invoice', invoiceId })}
        />
      );
    case 'expenses':
      return <ExpensesDetailScreen companyId={companyId} date={drill.date} onBack={onBack} />;
    case 'compliance':
      return <ComplianceScreen companyId={companyId} onBack={onBack} />;
    case 'stock':
      return (
        <StockScreen companyId={companyId} initialFilter={drill.filter} onBack={onBack} />
      );
  }
}

function NoCompany({ loading }: { loading: boolean }) {
  const t = useT();
  if (loading) {
    return (
      <main className="dash" aria-busy="true">
        <div className="ds-skeleton" style={{ blockSize: 180 }} />
      </main>
    );
  }
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">{t('till.noBusiness')}</p>
          <p className="ds-state__body">{t('till.noBusinessBody')}</p>
        </div>
      </div>
    </main>
  );
}

function NoAccess({ mayRefund }: { mayRefund: boolean }) {
  const t = useT();
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">{t('till.cannotSell')}</p>
          <p className="ds-state__body">
            {t('till.noSellPermission')}
            {mayRefund ? t('till.noSellCanRefund') : ''}
            {t('till.ownerCanChange')}
          </p>
        </div>
      </div>
    </main>
  );
}
