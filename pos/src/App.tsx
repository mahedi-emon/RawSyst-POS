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

import { useEffect, useMemo, useState } from 'react';

import { LoginScreen } from './auth/LoginScreen';
import { useAuth } from './auth/session';
import { listCompanies, type Company } from './api/companies';
import { Dashboard, type DrillTarget } from './dashboard/Dashboard';
import { SalesDetailScreen } from './dashboard/SalesDetailScreen';
import { ExpensesDetailScreen } from './dashboard/ExpensesDetailScreen';
import { ComplianceScreen } from './dashboard/ComplianceScreen';
import { StockScreen } from './dashboard/StockScreen';
import { PurchasingScreen } from './purchasing/PurchasingScreen';
import { PosCounter } from './pos/PosCounter';
import { ReturnsScreen } from './pos/ReturnsScreen';
import { TerminalBanner } from './ui/TerminalBanner';
import { terminalCapabilities, type Capabilities } from './pos/terminal';

type Screen = 'dashboard' | 'sell' | 'return' | 'buying';

export function App() {
  const { status, me, signOut, client } = useAuth();
  const [caps, setCaps] = useState<Capabilities | null>(null);
  const [companies, setCompanies] = useState<Company[] | null>(null);
  const [companyId, setCompanyId] = useState<string | null>(null);

  useEffect(() => {
    terminalCapabilities().then(setCaps).catch(() => setCaps(null));
  }, []);

  const may = useMemo(
    () => (permission: string) => me?.permissions.includes(permission) ?? false,
    [me],
  );

  const mayReadFigures = may('accounting.view');
  const maySell = may('sales.create');
  const mayRefund = may('sales.refund');
  const mayBuy = may('purchasing.view');

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

  if (status === 'restoring') {
    return (
      <main className="splash" aria-busy="true">
        <p>Checking this terminal…</p>
      </main>
    );
  }

  if (status !== 'signed_in' || !me) {
    return <LoginScreen />;
  }

  const destinations: Array<{ key: Screen; label: string; shown: boolean }> = [
    { key: 'dashboard', label: 'Dashboard', shown: mayReadFigures },
    { key: 'sell', label: 'Sell', shown: maySell },
    { key: 'return', label: 'Returns', shown: mayRefund },
    { key: 'buying', label: 'Buying', shown: mayBuy },
  ];
  const visible = destinations.filter((d) => d.shown);

  return (
    <div className="app">
      <header className="app__bar">
        <span className="app__brand">RawSyst</span>

        {/* Only rendered when there is a genuine choice. A single-destination
            nav is chrome that teaches nothing. */}
        {visible.length > 1 && (
          <nav className="app__nav" aria-label="Sections">
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
            <span className="ds-caption">Company</span>
            <select
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

        <button className="ds-btn ds-btn--quiet" onClick={() => void signOut()}>
          Sign out
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
      return <SalesDetailScreen companyId={companyId} date={drill.date} onBack={onBack} />;
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
          <p className="ds-state__title">No business set up yet</p>
          <p className="ds-state__body">
            The dashboard reports on a registered business. Finish setup to add
            one, and today's figures will appear here.
          </p>
        </div>
      </div>
    </main>
  );
}

function NoAccess({ mayRefund }: { mayRefund: boolean }) {
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">This account cannot ring up sales</p>
          <p className="ds-state__body">
            Your role does not include permission to sell
            {mayRefund ? ', though you can take goods back' : ''}. An owner can
            change that under Settings &gt; People.
          </p>
        </div>
      </div>
    </main>
  );
}
