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

import { useCallback, useEffect, useMemo, useState } from 'react';

import { LoginScreen } from '@rawsyst/shared/auth/LoginScreen';
import { useAuth } from '@rawsyst/shared/auth/session';
import { listCompanies, type Company } from '@rawsyst/shared/api/companies';
import { Dashboard, type DrillTarget } from '@rawsyst/shared/dashboard/Dashboard';
import { SalesDetailScreen } from '@rawsyst/shared/dashboard/SalesDetailScreen';
import { ExpensesDetailScreen } from '@rawsyst/shared/dashboard/ExpensesDetailScreen';
import { ComplianceScreen } from '@rawsyst/shared/dashboard/ComplianceScreen';
import { StockScreen } from '@rawsyst/shared/dashboard/StockScreen';
import { InvoiceDetailScreen } from '@rawsyst/shared/invoices/InvoiceDetailScreen';
import { PurchasingScreen } from '@rawsyst/shared/purchasing/PurchasingScreen';
import { CustomersScreen } from '@rawsyst/shared/receivables/CustomersScreen';
import { DevicesScreen } from '@rawsyst/shared/devices/DevicesScreen';
import { PeopleScreen } from '@rawsyst/shared/people/PeopleScreen';
import { ShopMark } from '@rawsyst/shared/settings/ShopMark';
import { EgsUnitsScreen } from '@rawsyst/shared/einvoicing/EgsUnitsScreen';
import { OnboardingWizard } from '@rawsyst/shared/onboarding/OnboardingWizard';
import { ExpensesScreen } from '@rawsyst/shared/expenses/ExpensesScreen';
import { SettlementScreen } from '@rawsyst/shared/settlement/SettlementScreen';
import { LanguageSwitch } from '@rawsyst/shared/i18n/LanguageSwitch';
import { useT } from '@rawsyst/shared/i18n/locale';
import { BrandingScreen } from '@rawsyst/shared/settings/BrandingScreen';
import { ProductsArea } from '@rawsyst/shared/catalog/ProductsArea';
import { StockArea } from '@rawsyst/shared/stock/StockArea';
import { AccountingArea } from '@rawsyst/shared/accounting/AccountingArea';
import { AssetsArea } from '@rawsyst/shared/assets/AssetsArea';
import { Icon, type IconName } from '@rawsyst/shared/ui/Icon';
import { ThemeSwitch } from '@rawsyst/shared/ui/ThemeSwitch';

type Section =
  | 'dashboard'
  | 'people'
  | 'buying'
  | 'customers'
  | 'expenses'
  | 'settlement'
  | 'accounting'
  | 'assets'
  | 'stock'
  | 'devices'
  | 'einvoicing'
  | 'setup'
  | 'branding'
  | 'inventory';

export function BackOffice() {
  const { status, me, signOut, client } = useAuth();
  const t = useT();

  const [section, setSection] = useState<Section>('dashboard');
  const [drill, setDrill] = useState<DrillTarget | null>(null);
  // Bumped on every press of a navigation item, and part of the mounted
  // screen's key. Its whole job is to make pressing the CURRENT section mean
  // something: see the click handler below.
  const [sectionNonce, setSectionNonce] = useState(0);

  // Whether the rail shows its labels, and whether the phone's drawer is open.
  //
  // The pin is remembered per device, which is the right scope: the same person
  // wants the labels on a laptop and the width back on a tablet, and the
  // machine is what tells the two apart.
  const [railOpen, setRailOpen] = useState(false);
  const [drawer, setDrawer] = useState(false);

  // Read after mount, never during render: the page is server-rendered and the
  // server has no localStorage, so reading it in `useState` would hydrate to a
  // different tree than it rendered.
  useEffect(() => {
    try {
      setRailOpen(localStorage.getItem('rawsyst.rail') === 'open');
    } catch {
      /* A browser refusing storage is not a reason to fail to render. */
    }
  }, []);

  /* Written when the preference CHANGES, not whenever the state does.
   *
   * This was a second effect keyed on `railOpen`, and effects run in the order
   * they are declared: on mount, the read above scheduled an update and this
   * one then ran with `railOpen` still at its initial `false` and wrote
   * "icons" — erasing the stored preference before the read of it had been
   * applied. The pin worked, persisted, and was forgotten on every reload,
   * which is a worse failure than not persisting at all: it is a control that
   * lies about having remembered.
   *
   * A click is the only thing that changes this preference, so a click is
   * where it is saved. There is no ordering left to get wrong. */
  const togglePin = useCallback(() => {
    setRailOpen((open) => {
      const next = !open;
      try {
        localStorage.setItem('rawsyst.rail', next ? 'open' : 'icons');
      } catch {
        /* As above. */
      }
      return next;
    });
  }, []);

  // Escape closes the drawer, because a drawer over the whole screen with no
  // visible way out is a trap on a phone.
  useEffect(() => {
    if (!drawer) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setDrawer(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [drawer]);
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
  // Bumped when setup creates a company, so the list is re-fetched rather than
  // leaving the owner looking at the empty state they just resolved.
  const [reloadKey, setReloadKey] = useState(0);

  const may = useMemo(
    () => (permission: string) => me?.permissions.includes(permission) ?? false,
    [me],
  );
  const mayReadFigures = may('accounting.view');
  const mayBuy = may('purchasing.view');
  // A Cashier holds customers.view, so this section is reachable from a till
  // login as well as from an owner's. What each of them may DO inside it is
  // decided by the routes, not by the nav.
  const maySeeCustomers = may('customers.view');
  // A store manager holds this too: a till that dies mid-trade cannot wait for
  // an owner to answer their phone.
  const maySeeDevices = may('devices.view');

  // Reading the staff list. Adding somebody needs `identity.create` AND
  // `identity.manage_roles` — the screen asks for those itself, because a
  // person who may keep the list current is not necessarily one who may decide
  // what anybody is allowed to do.
  const maySeePeople = may('identity.view');
  // Card settlement reads with accounting.view. A cashier takes the money and
  // does not decide it has arrived — matching a bank statement to a day's
  // takings is bookkeeping, and the routes refuse them either way.
  const maySeeAccounting = may('accounting.view');
  // Its own verb rather than accounting.view: blueprint A4 gives the Accountant
  // expenses, and a store manager sees what their branch spends without being
  // able to read the ledger it lands in.
  const maySeeExpenses = may('expense.view');
  // Separate from devices.view: an EGS unit carries the VAT registration the
  // invoice chain hangs from, so an accountant reads it and a store manager
  // sees it without being able to create one.
  const maySeeEInvoicing = may('einvoicing.view');
  // Setup reads with identity.view and writes with identity.edit, which is
  // what the four onboarding routes require. It is deliberately not its own
  // permission: an onboarding-only verb would mean every tenant's custom roles
  // needed updating before anybody could finish setup.
  const maySeeSetup = may('identity.view');
  // UI spec §4. catalog.view is what the matrix route requires, and a store
  // manager and an inventory keeper both hold it — the two people who actually
  // reorder stock.
  const maySeeInventory = may('catalog.view');

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
  }, [client, me, tenant, reloadKey]);

  // Land on whichever section this person can actually use. An owner opens on
  // the figures; a purchase manager with no accounting.view opens on buying,
  // rather than on a permission-denied panel they have to navigate out of.
  useEffect(() => {
    if (!me) return;
    if (mayReadFigures) return;
    if (mayBuy) setSection('buying');
    else if (maySeeCustomers) setSection('customers');
    else if (maySeeDevices) setSection('devices');
    else if (maySeeEInvoicing) setSection('einvoicing');
  }, [me, mayReadFigures, mayBuy, maySeeCustomers, maySeeDevices, maySeeEInvoicing]);

  if (status === 'restoring') {
    return (
      <main className="bo__splash" aria-busy="true">
        <p className="ds-muted">{t('bo.signingIn')}</p>
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

  // Grouped, because ten destinations in one list is a list somebody reads
  // every time instead of learning. The groups are the three questions an owner
  // opens this with: how is the business doing, what is the money doing, and
  // how is the shop set up.
  const sections: Array<{
    key: Section;
    label: string;
    icon: IconName;
    group: 'overview' | 'trade' | 'admin';
    shown: boolean;
  }> = [
    {
      key: 'dashboard',
      label: t('nav.dashboard'),
      icon: 'dashboard',
      group: 'overview',
      shown: mayReadFigures,
    },
    {
      key: 'buying',
      label: t('nav.buying'),
      icon: 'buying',
      group: 'trade',
      shown: mayBuy,
    },
    {
      key: 'inventory',
      label: t('nav.inventory'),
      icon: 'inventory',
      group: 'trade',
      shown: maySeeInventory,
    },
    // Beside the catalogue, because they are the two halves of the same
    // question. Inventory is what the shop SELLS — products, variants, prices.
    // Stock is how many of them are in the building, where, and what has
    // happened to them.
    {
      key: 'stock',
      label: t('nav.stock'),
      icon: 'stock',
      group: 'trade',
      shown: maySeeInventory,
    },
    {
      key: 'customers',
      label: t('nav.customers'),
      icon: 'customers',
      group: 'trade',
      shown: maySeeCustomers,
    },
    // Beside Buying and Customers, because it is the third thing money does:
    // what the shop bought, what it is owed, and what it spent.
    {
      key: 'expenses',
      label: t('nav.expenses'),
      icon: 'expenses',
      group: 'trade',
      shown: maySeeExpenses,
    },
    // Between the money screens and the hardware ones, because reconciling a
    // bank statement is bookkeeping rather than administration.
    {
      key: 'settlement',
      label: t('nav.settlement'),
      icon: 'settlement',
      group: 'trade',
      shown: maySeeAccounting,
    },
    // Last but one in Trade, because it is where the others end up: what was
    // bought, sold, owed and spent, added together. It also carries the
    // accounting calendar, which decides whether any of those figures can
    // still move.
    {
      key: 'accounting',
      label: t('nav.accounting'),
      icon: 'accounting',
      group: 'trade',
      shown: maySeeAccounting,
    },
    // PART K puts Assets at the top level, and the two registers under it are
    // read at different moments from the accounts: an asset register when
    // something goes missing, an investor register when somebody asks what
    // they own.
    {
      key: 'assets',
      label: t('nav.assets'),
      icon: 'assets',
      group: 'trade',
      shown: maySeeAccounting,
    },
    // First in Administration, because it is the first thing a newly
    // onboarded shop needs: A5 hands the Owner a business and A6 is how they
    // staff it.
    {
      key: 'people',
      label: t('nav.people'),
      icon: 'people',
      group: 'admin',
      shown: maySeePeople,
    },
    {
      key: 'devices',
      label: t('nav.devices'),
      icon: 'devices',
      group: 'admin',
      shown: maySeeDevices,
    },
    {
      key: 'einvoicing',
      label: t('nav.einvoicing'),
      icon: 'einvoicing',
      group: 'admin',
      shown: maySeeEInvoicing,
    },
    {
      key: 'setup',
      label: t('nav.setup'),
      icon: 'setup',
      group: 'admin',
      shown: maySeeSetup,
    },
    // I2. Reads with identity.view and writes with identity.edit, the same
    // pair the rest of company settings carries.
    {
      key: 'branding',
      label: t('nav.branding'),
      icon: 'branding',
      group: 'admin',
      shown: maySeeSetup,
    },
  ];
  const visible = sections.filter((s) => s.shown);
  const groups: Array<{ key: 'overview' | 'trade' | 'admin'; label: string }> = [
    { key: 'overview', label: t('nav.overview') },
    { key: 'trade', label: t('nav.trade') },
    { key: 'admin', label: t('nav.administration') },
  ];

  const current = visible.find((s) => s.key === section);

  // The company whose books are on screen, for the bar. Trade name where a
  // shop has one, because that is what its own staff call it.
  const here = companies?.find((c) => c.id === activeCompany);
  const hereName = here ? here.trade_name || here.legal_name : null;

  function go(key: Section) {
    setSection(key);
    // Leaving a section clears the drill, so returning starts at the top
    // rather than three levels into last week.
    setDrill(null);
    // Pressing the section you are ALREADY in takes you back to its top.
    // Without this the click did nothing at all: the section value was
    // unchanged, so React kept the mounted screen and its private state, and a
    // buyer halfway through a new purchase order who reached for "Buying" —
    // which is what everybody reaches for — stayed on the form. The only way
    // out was a back control they had to notice first.
    //
    // Each screen owns its own sub-view, so there is nothing here to reset;
    // bumping a key remounts the screen, which returns every one of them to
    // its index at once.
    setSectionNonce((n) => n + 1);
    // And on a phone the drawer closes behind you, because a navigation that
    // stays open over the screen it just opened is one tap of housekeeping
    // per move.
    setDrawer(false);
  }

  return (
    <div className="bo">
      {/* The scrim exists only while the drawer is open, and only on a phone
          — where the rail is over the page rather than beside it. */}
      {drawer && (
        <button
          type="button"
          className="bo__scrim"
          aria-label={t('nav.closeMenu')}
          onClick={() => setDrawer(false)}
        />
      )}

      <nav
        className={`bo__rail${railOpen ? ' bo__rail--open' : ''}${drawer ? ' bo__rail--drawer' : ''}`}
        aria-label={t('nav.sections')}
      >
        {/* The SHOP's name and logo, not the vendor's.
            A shopkeeper looking at their own till software has no reason to be
            reminded whose product it is on every screen. `RawSyst` remains the
            fallback for a tenant that has no company yet, which is the state
            during onboarding. */}
        <div className="bo__railhead">
          <ShopMark
            companyId={activeCompany}
            name={hereName}
            version={reloadKey}
          />
        </div>

        <div className="bo__nav">
          {groups.map((g) => {
            const inGroup = visible.filter((x) => x.group === g.key);
            if (inGroup.length === 0) return null;
            return (
              <div key={g.key}>
                <p className="bo__group">{g.label}</p>
                {inGroup.map((x) => (
                  <button
                    key={x.key}
                    type="button"
                    className={`bo__link${section === x.key ? ' bo__link--on' : ''}`}
                    aria-current={section === x.key ? 'page' : undefined}
                    // The label again, for the collapsed rail where the words
                    // are not on screen. A native title is the one tooltip
                    // that works before any JavaScript has run.
                    title={x.label}
                    onClick={() => go(x.key)}
                  >
                    <Icon name={x.icon} />
                    <span className="bo__linklabel">{x.label}</span>
                  </button>
                ))}
              </div>
            );
          })}
        </div>

        <div className="bo__railfoot">
          {/* Styled as a rail link because that is what it looks like, and
              marked as not one because that is what it is. Every harness that
              walks the rail enumerates `.bo__link`; without the modifier the
              last "section" each of them visited was sign-out, which ended the
              session — so the Arabic and Bangla passes found no rail at all and
              reported eleven screens clean by never having looked at them. */}
          <button
            type="button"
            className="bo__link bo__link--signout"
            title={t('nav.signOut')}
            onClick={() => void signOut()}
          >
            <Icon name="signout" />
            <span className="bo__linklabel">{t('nav.signOut')}</span>
          </button>
        </div>
      </nav>

      <div className="bo__screen">
        <header className="bo__bar">
          <button
            type="button"
            className="bo__iconbtn bo__menu"
            aria-label={t('nav.openMenu')}
            aria-expanded={drawer}
            onClick={() => setDrawer(true)}
          >
            <Icon name="menu" size={20} />
          </button>

          {/* The rail is pinned open or left to collapse, and the choice is
              remembered. An owner on a 1280px laptop wants the labels; the
              same person on a 1024px tablet wants the width back. */}
          <button
            type="button"
            className="bo__iconbtn bo__pin"
            aria-label={t('nav.toggleMenu')}
            aria-pressed={railOpen}
            onClick={togglePin}
          >
            <Icon name="menu" size={20} />
          </button>

          {/* Which company, then which section.
            *
            * The section name alone repeated the page's own heading eighty
            * pixels below it, which reads as a stutter rather than as
            * orientation. The company is the thing the bar can say that the
            * page cannot: a tenant with two companies needs to know which
            * books they are looking at before they read a figure, and a tenant
            * with one has never been told the name at all. */}
          <nav className="bo__crumbs" aria-label={t('nav.sections')}>
            {hereName && <span className="bo__crumb">{hereName}</span>}
            {hereName && (
              <span className="bo__crumbsep" aria-hidden="true">
                <Icon name="chevron" size={14} />
              </span>
            )}
            <h1 className="bo__title">{current?.label ?? 'RawSyst'}</h1>
          </nav>

          <div className="bo__baractions">
            {/* Only when there is a genuine choice. A picker offering one
                option is a control that cannot be used. */}
            {companies && companies.length > 1 && (
              <label className="app__company">
                <span className="ds-visually-hidden">{t('nav.company')}</span>
                <select
                  className="field__input"
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

            <ThemeSwitch />
            <LanguageSwitch />
          </div>
        </header>

      {/* The key is what makes a press of the current section take effect. It
        * changes when the section changes, which React would have done anyway,
        * AND when the same section is pressed again, which it would not. */}
        <div className="bo__body" key={`${section}:${sectionNonce}`}>
      {visible.length === 0 ? (
        <NoSections />
      ) : section === 'setup' && maySeeSetup ? (
        <OnboardingWizard onFinished={() => setReloadKey((n) => n + 1)} />
      ) : !activeCompany ? (
        // A tenant with no company used to dead-end here on a panel that told
        // them to finish setup and gave them no way to do it. The wizard IS
        // that way, so it is what they land on.
        companies === null ? (
          <NoCompany loading />
        ) : maySeeSetup ? (
          <OnboardingWizard onFinished={() => setReloadKey((n) => n + 1)} />
        ) : (
          <NoCompany loading={false} />
        )
      ) : section === 'buying' && mayBuy ? (
        <PurchasingScreen companyId={activeCompany} />
      ) : section === 'customers' && maySeeCustomers ? (
        <CustomersScreen companyId={activeCompany} />
      ) : section === 'expenses' && maySeeExpenses ? (
        <ExpensesScreen companyId={activeCompany} />
      ) : section === 'settlement' && maySeeAccounting ? (
        <SettlementScreen companyId={activeCompany} />
      ) : section === 'people' && maySeePeople ? (
        <PeopleScreen />
      ) : section === 'devices' && maySeeDevices ? (
        <DevicesScreen companyId={activeCompany} />
      ) : section === 'einvoicing' && maySeeEInvoicing ? (
        <EgsUnitsScreen companyId={activeCompany} />
      ) : section === 'branding' && maySeeSetup ? (
        <BrandingScreen
          companyId={activeCompany}
          onLogoChanged={() => setReloadKey((n) => n + 1)}
        />
      ) : section === 'inventory' && maySeeInventory ? (
        <ProductsArea companyId={activeCompany} />
      ) : section === 'stock' && maySeeInventory ? (
        <StockArea companyId={activeCompany} />
      ) : section === 'accounting' && maySeeAccounting ? (
        <AccountingArea companyId={activeCompany} />
      ) : section === 'assets' && maySeeAccounting ? (
        <AssetsArea companyId={activeCompany} />
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
      </div>
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
          // Back goes to the dashboard rather than to the day it was opened
          // from: this screen is reachable from more than one place, and a
          // Back that guesses wrong is worse than one that is predictable.
          onBack={onBack}
          onOpenInvoice={(invoiceId) => onOpen({ screen: 'invoice', invoiceId })}
        />
      );
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
  const t = useT();
  return (
    <main className="dash">
      <div className="ds-panel">
        <div className="ds-state">
          <p className="ds-state__title">{t('bo.nothingForAccount')}</p>
          <p className="ds-state__body">{t('bo.tillRoleBody')}</p>
        </div>
      </div>
    </main>
  );
}

function NoCompany({ loading }: { loading: boolean }) {
  const t = useT();
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
          <p className="ds-state__title">{t('till.noBusiness')}</p>
          <p className="ds-state__body">{t('bo.noBusinessBody')}</p>
        </div>
      </div>
    </main>
  );
}
