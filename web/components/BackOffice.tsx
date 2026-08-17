'use client';

// The back-office shell.
//
// Everything below the navigation is a component shared with the Tauri POS.
// This file owns three things and nothing else: which section is showing, which
// company it is about, and the fact that nothing shows at all until somebody
// has signed in.
//
// # No business logic lives here, and none may
//
// Every figure on every screen it renders was computed by the Go service from
// the same journal the trial balance reads. If a number ever needs working out
// on this side, that is a signal the server is missing an endpoint — not an
// invitation to add arithmetic to a React component nobody can audit.
//
// # Sections are permission-gated, and that is presentation only
//
// The server refuses every route the caller lacks the permission for, and QA
// gate M7 proves it. Hiding the nav item matters for usability rather than
// security: a bar full of destinations that refuse you teaches people to
// distrust the whole thing.

import { useEffect, useMemo, useState } from 'react';

import { LoginScreen } from '@rawsyst/shared/auth/LoginScreen';
import { useAuth } from '@rawsyst/shared/auth/session';
import { listCompanies, type Company } from '@rawsyst/shared/api/companies';
import { Dashboard, type DrillTarget } from '@rawsyst/shared/dashboard/Dashboard';
import { SalesDetailScreen } from '@rawsyst/shared/dashboard/SalesDetailScreen';
import { ExpensesDetailScreen } from '@rawsyst/shared/dashboard/ExpensesDetailScreen';
import { ComplianceScreen } from '@rawsyst/shared/dashboard/ComplianceScreen';
import { StockScreen } from '@rawsyst/shared/dashboard/StockScreen';
import { PurchasingScreen } from '@rawsyst/shared/purchasing/PurchasingScreen';

type Section = 'dashboard' | 'buying';

export function BackOffice() {
  const { status, me, signOut, client } = useAuth();

  const [section, setSection] = useState<Section>('dashboard');
  const [drill, setDrill] = useState<DrillTarget | null>(null);
  // The company list carries the tenant it was fetched for.
  //
  // Storing them apart was a bug: clearing the selection in an effect still
  // let one render happen with the NEW session and the OLD company, which
  // fired a dashboard request for a company the new tenant cannot see and got
  // a 404. A browser check caught it after switching businesses. Keeping the
  // two together makes a stale pair unrepresentable rather than merely
  // short-lived.
  const [loaded, setLoaded] = useState<{
    tenant: string | null;
    companies: Company[];
  } | null>(null);
  const [companyId, setCompanyId] = useState<string | null>(null);

  const may = useMemo(
    () => (permission: string) => me?.permissions.includes(permission) ?? false,
    [me],
  );
  const mayReadFigures = may('accounting.view');
  const mayBuy = may('purchasing.view');

  // Keyed on the TENANT, not just on being signed in.
  //
  // The same person can sign out of one business and into another — that is
  // the whole point of the tenant picker — and the company they had selected
  // does not exist in the new one. Keeping it produced a 404 on the dashboard
  // for the moment between the new session opening and the company list
  // arriving, which a browser check caught. Clearing on the way in is the fix;
  // the alternative of clearing on sign-out misses a session that expires.
  const tenant = me?.tenant_id ?? null;

  useEffect(() => {
    if (!me) return;
    let cancelled = false;

    listCompanies(client)
      .then((found) => {
        if (cancelled) return;
        setLoaded({ tenant, companies: found });
        setCompanyId(found[0]?.id ?? null);
      })
      .catch(() => {
        if (!cancelled) setLoaded({ tenant, companies: [] });
      });
    return () => {
      cancelled = true;
    };
  }, [client, me, tenant]);

  // Land on whichever section this person can actually use. An owner opens on
  // the figures; a purchase manager with no accounting.view opens on buying,
  // rather than on a permission-denied panel they have to navigate out of.
  useEffect(() => {
    if (!me) return;
    if (!mayReadFigures && mayBuy) setSection('buying');
  }, [me, mayReadFigures, mayBuy]);

  if (status === 'restoring') {
    return (
      <main className="bo__splash" aria-busy="true">
        <p className="ds-muted">Signing you in…</p>
      </main>
    );
  }

  if (status !== 'signed_in' || !me) {
    return <LoginScreen />;
  }

  // A list from a previous tenant is not this tenant's list. Treated as still
  // loading, which is what it is.
  const companies = loaded && loaded.tenant === tenant ? loaded.companies : null;
  const activeCompany =
    companies && companyId && companies.some((c) => c.id === companyId)
      ? companyId
      : null;

  const sections: Array<{ key: Section; label: string; shown: boolean }> = [
    { key: 'dashboard', label: 'Dashboard', shown: mayReadFigures },
    { key: 'buying', label: 'Buying', shown: mayBuy },
  ];
  const visible = sections.filter((s) => s.shown);

  return (
    <div className="bo">
      <header className="bo__bar">
        <span className="bo__brand">RawSyst</span>

        {visible.length > 1 && (
          <nav className="app__nav" aria-label="Sections">
            {visible.map((s) => (
              <button
                key={s.key}
                className={`app__navlink${section === s.key ? ' app__navlink--on' : ''}`}
                aria-current={section === s.key ? 'page' : undefined}
                onClick={() => {
                  setSection(s.key);
                  // Leaving a section clears the drill, so returning starts at
                  // the top rather than three levels into last week.
                  setDrill(null);
                }}
              >
                {s.label}
              </button>
            ))}
          </nav>
        )}

        <div className="app__spacer" />

        {/* Only when there is a genuine choice. A picker offering one option
            is a control that cannot be used. */}
        {companies && companies.length > 1 && (
          <label className="app__company">
            <span className="ds-caption">Company</span>
            <select
              value={companyId ?? ''}
              onChange={(e) => {
                setCompanyId(e.target.value);
                // The drill belongs to the company it was opened from.
                setDrill(null);
              }}
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

      {visible.length === 0 ? (
        <NoSections />
      ) : !activeCompany ? (
        <NoCompany loading={companies === null} />
      ) : section === 'buying' && mayBuy ? (
        <PurchasingScreen companyId={activeCompany} />
      ) : mayReadFigures ? (
        <DashboardArea
          companyId={activeCompany}
          drill={drill}
          onOpen={setDrill}
          onBack={() => setDrill(null)}
        />
      ) : (
        <NoSections />
      )}
    </div>
  );
}

/** The dashboard and everything it drills into.
 *
 * Identical to the Tauri shell's arrangement, because it is the same
 * components. Deliberately not a URL router: these screens carry no deep links
 * anybody shares, and a router here would be a second navigation model to keep
 * in step with the POS's. */
function DashboardArea({
  companyId,
  drill,
  onOpen,
  onBack,
}: {
  companyId: string;
  drill: DrillTarget | null;
  onOpen: (t: DrillTarget) => void;
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
      return <StockScreen companyId={companyId} initialFilter={drill.filter} onBack={onBack} />;
  }
}

/** Signed in, but with nothing here for them.
 *
 * A cashier's permissions are for the till, and the till is a different
 * application. Said plainly, with where to go instead, rather than showing an
 * empty dashboard that looks broken. */
function NoSections() {
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">Nothing here for this account</p>
          <p className="ds-state__body">
            Your role covers the till rather than the back office. Sign in on the
            RawSyst terminal to sell, or ask an owner to widen your permissions
            under Settings &gt; People.
          </p>
        </div>
      </div>
    </main>
  );
}

function NoCompany({ loading }: { loading: boolean }) {
  if (loading) {
    return (
      <main className="dash" aria-busy="true">
        <div className="ds-skeleton" style={{ blockSize: 200 }} />
      </main>
    );
  }
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">No business set up yet</p>
          <p className="ds-state__body">
            The back office reports on a registered business. Finish setup to add
            one and your figures will appear here.
          </p>
        </div>
      </div>
    </main>
  );
}
