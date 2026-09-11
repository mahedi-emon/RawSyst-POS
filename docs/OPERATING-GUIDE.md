# RawSyst POS — the operating guide

Who does what, at which address, and in what order.

This is written for two people. The **software owner** — you — sells the
product, creates the businesses and holds the subscriptions. A **business
owner** is your client: they run one shop or a chain, and everything inside
their business is theirs alone. The two never see each other's screens, and
that separation is enforced by the server rather than by hidden buttons.

---

## 1. The two addresses

RawSyst is one deployment serving two audiences from two hostnames.

| | Who signs in | What they get |
|---|---|---|
| **The business application** — e.g. `https://app.example.com` | Business owners, managers, cashiers, accountants | The shop: selling, stock, buying, money, people, reports |
| **The control plane** — e.g. `https://console.example.com` | You, the software owner | Businesses, plans, subscriptions, regulatory registry, backups |

Both are configured, not hardcoded. In `.env`:

```
RAWSYST_APP_URL=https://app.example.com          # where a business signs in
RAWSYST_CONSOLE_HOST=console.example.com         # a HOSTNAME, no scheme
RAWSYST_CONSOLE_URL=https://console.example.com  # the same with its scheme
```

**Pick a console hostname that is not guessable from the product name.** Not
publishing the address is half of the protection; the other half is enforced.

### What the separation actually does

Leave `RAWSYST_CONSOLE_HOST` empty and both halves share one origin, which is
how a developer's machine works. Set it and three things happen at once:

- `/platform/*` **pages** answer only on the console hostname and 404 on the
  business one.
- Every other page 404s on the console hostname.
- `/api/v1/platform/*` **routes** are refused on the business hostname by the
  API itself, so the boundary does not depend on the web tier being in front.

404, not 403. A page that answers "forbidden" has confirmed it exists.

If you sign in to the business address with your platform account, the product
tells you where to go instead — and it tells only you. The console's address is
never compiled into a shop's browser bundle.

---

## 2. Creating a business owner

This is the whole handover, start to finish.

### Once per deployment: your own account

Nothing seeds a platform administrator. A product that ships with a known
administrator ships with a known way in, so the first one is created by hand:

```
# In .env, first
RAWSYST_PLATFORM_EMAIL=you@yourcompany.com

docker compose --profile setup run --rm bootstrap
```

It prints a one-time password to the terminal and nowhere else, then refuses to
run again. Sign in at the console address and change it immediately.

Lost it? `bootstrap -recover -email you@yourcompany.com` issues a new one for an
operator who **already exists** and ends every session that account held. It
creates nobody, so it is not a back door.

### Per client: onboard a business

**Console → Businesses → Onboard a business.** One form, one transaction:

1. **The business** — trading name, and the market it trades in. The market is
   `SA`, `BD` or `US`, and it decides the tax rules every sale will be costed
   against. A market whose rules are not in the registry is refused here, with
   its name, rather than at the counter three weeks later.
2. **The base currency** — `SAR`, `BDT` or `USD`. Deliberately a separate
   question from the country: a business in one country may legitimately keep
   its books in another's currency.
3. **The owner** — their name and email address. This becomes the one account
   that can reach the business.
4. **The plan** — tier (`starter`, `professional`, `business`, `enterprise`),
   billing cycle, price, currency, and when it starts and expires.

Press create and the server does all of it or none of it: the tenant, its
limits, its Owner role, and the owner's account. A tenant with no owner is
unreachable and a tenant with no limits has no ceilings, so there is no partial
success worth keeping.

### The handover screen

What comes back is what you give the client:

- **their email address** — their username
- **a temporary password** — shown once, never stored in readable form, must be
  changed at first sign-in
- **the login URL** — the business address, or a plain statement that the
  deployment has not been told its own address, rather than a link going nowhere
- **what you just sold them** — tier, cycle, price, start and expiry, echoed so
  you need not open the billing screen to find out
- **what will happen to the welcome email** — and this is honest. With a mail
  provider configured it says *queued*. Without one it says the password was
  **shown to you and sent to nobody**, so you know to read it out yourself.

To send that email, set both halves of the mail configuration:

```
RAWSYST_RESEND_API_KEY=re_...              # a secret; never commit it
RAWSYST_MAIL_FROM=noreply@yourdomain.com   # a domain verified in Resend
```

Both or neither. Half-configured fails with a sentence naming which half, and
an unverified sender domain fails permanently rather than retrying for hours.

### After the handover

- **Businesses** lists every client, their plan, and where they are in setup.
- **Subscriptions and dunning** is where you change a plan, extend a period,
  suspend or reactivate.
- Every one of these actions is written to the audit trail with your name on it.

---

## 3. What a subscription does

Enforced on every request, computed from the calendar. There is no background
job and no window to wait through.

| State | Reading | Writing |
|---|---|---|
| `active`, `trialing` | yes | yes |
| `past_due` | yes | yes — they are being chased, not cut off |
| `expired`, `suspended` | yes | **no** (402) |
| `deactivated` | **no** (403) | no |

Expired and suspended are deliberately read-only rather than blocked: a shop
that has not paid can still see its own books, export its data and settle up.
Signing out, changing a password, MFA and raising a support ticket keep working
in every state — locking somebody out of the door they would use to fix the
problem is not enforcement.

A business past its `current_period_end` is read-only on the next request.

---

## 4. What a business owner gets

Everything below, subject to their plan. An owner sees all of it; an employee
sees the part their role opens.

### Overview
Dashboard · Waiting for you (approvals)

### Selling
Point of sale · Sales · Returns and exchanges · Exchanges · Orders ·
Deliveries · Till sessions · Promotions

### Products
Products · Arrangement · Barcodes and labels

### Stock
Stock on hand · Movements · Counts and adjustments · Transfers ·
Batches and expiry · Production · Locations · Serial numbers

### Buying
Suppliers · Purchase orders · Goods received · Bills · Supplier payments ·
Goods going back · Requests to buy · Quotes · What is owed

### Customers
Customers · What is owed to you · Loyalty · Wallets and gift cards ·
Portal access

### Money
Expenses · Expense setup · Cash and bank · Moving money · Payments received ·
Bank reconciliation · Journals · Adjustments · Chart of accounts ·
Accounting periods · Instalment plans · Fixed assets · Investors ·
Card providers · Card settlement

### People
Employees · Attendance and leave · Payroll · Commission · Users and roles ·
Roles

### Reports
Financial statements · Tax return · Trends · Workforce · Saved reports

### After sales
Service jobs · Return requests

### Oversight
Compliance · Audit trail · Documents · Privacy · Group companies · Backups

### Settings
Business details · Setup · Tills and devices · E-invoicing · Tax · Security ·
Approvals · Notifications · Integrations · Import data · Plan and billing ·
Support

**The till is separate.** The counter runs a desktop application installed on
the terminal, which sells offline and syncs when the network returns. The
browser screens above are the back office.

---

## 5. How a business owner uses it

### First sign-in

They go to the business address, sign in with the email and temporary password
you gave them, and are made to change the password before anything else.

### Setup, in seven steps

**Settings → Setup** walks them through it in order, and the product remembers
where they stopped:

1. **Business details** — legal name, address, tax registration
2. **Stores** — each branch, with the address that prints on its receipts
3. **Tax** — the registration number and what appears on an invoice
4. **Employees** — the people, before the accounts
5. **Hardware** — tills and printers, each registered as a device
6. **Opening balances** — what they already hold in stock and in the bank
7. **Finished**

They can trade before finishing, but a step skipped shows up later as a figure
that will not reconcile, so the order is worth keeping.

### Creating an employee and granting access

**People → Employees** records the person. **People → Users and roles** gives
them a way in: an email address and a role. The product emails them or shows
you a temporary password, exactly as it did for the owner.

Twelve roles ship with the product:

| Role | What it opens |
|---|---|
| **Owner** | Everything within the business |
| **Branch / Store Manager** | Sales, stock, staff and approvals. Not bank ledgers, not true net profit |
| **Cashier / POS Operator** | Billing, scanning, opening and closing a shift, basic returns. Cost price and margin are always hidden |
| **Accountant** | Accounts, journals, expenses, VAT returns, bank transfers. Cannot edit products or stock |
| **Inventory / Warehouse Keeper** | Goods receipt, transfers, wastage, barcode printing. No pricing, no sales |
| **Purchase Manager** | Purchase requests, orders, supplier correspondence |
| **HR Manager** | Employees, attendance, payroll setup. No sales, no stock |
| **Sales Executive** | Quotations, orders, their own customers |
| **Delivery Staff** | Their assigned delivery orders only |
| **Online Order Manager** | Web and app orders, packing, dispatch |
| **Auditor** | Read-only across everything — for an external accountant |
| **Customer Service** | Customer profiles, tickets, returns. No financial data |

**People → Roles** builds a custom role by ticking permissions, for a shop whose
shape none of the twelve fits.

Read-only means read-only. The screens a role cannot use are not merely hidden:
the API refuses the request. Disabling an employee ends their access on their
next request, not when their token happens to expire.

### A day's trading

**Open the till.** A cashier signs in at the counter and counts the drawer into
the shift. No sale can be rung up before that — otherwise a cash difference
found later belongs to nobody.

**Sell.** Scan or search, take payment in cash, card, wallet or split. The sale
is numbered, chained for tax, and costed at the moment it is made — so gross
profit is measured rather than reconstructed at month end. The receipt prints
from the till on 42 columns of plain text; the browser's receipt screen prints
an HTML copy with the shop's logo, its VAT broken out by rate, and the ZATCA QR
where the market requires one. A second copy of a tax invoice is recorded as a
reprint.

**Close the till.** Count the drawer, and the difference against what the system
expected is recorded against the cashier who counted it.

### Buying, end to end

Request → **purchase order** → **goods received** → **supplier bill** →
**payment**.

Receiving puts stock in at its real cost and raises the accrual the same
moment. The bill discharges that accrual, separates recoverable tax and creates
the payable. The gap between what was ordered, what arrived and what was
invoiced is the three-way match, and it is the entire reason a receipt is not a
bill.

Landed costs — freight, duty, clearance — are allocated across the lines they
belong to, so the cost a product sells against is what it actually cost to get
onto the shelf.

### The books keep themselves

Every event posts as it happens: a sale posts revenue, cost of sales and any
variance; a refund reverses both; a stock adjustment, a production run, an
expense, a payroll run, an asset purchase or disposal, a loyalty accrual, a
wallet top-up, a card settlement, a cash count — each has its posting rule.

The invariant the product holds itself to is that the **stock valuation ties
exactly to the Inventory control account**. Not approximately. Costs are held
to four decimals and posted as the change in the rounded valuation rather than
the rounded change, so the entries telescope and the two agree by construction.

**Reports → Financial statements** produces a trial balance, a profit and loss
and a balance sheet and a cash flow statement from those entries. **Reports → Tax return** produces the
VAT return from the same ledger, not from a parallel calculation.

### Closing a month

**Money → Accounting periods** closes a period. A closed period refuses new
entries, so figures already reported cannot move underneath them.

### If something goes wrong

**Oversight → Audit trail** records who did what, when, from where — including
every one of your own actions in the console against their business.
**Oversight → Backups** holds the backup and restore controls.

---

## 6. Isolation, stated plainly

- Every business is a tenant. 171 tables carry a tenant id, and row-level
  security is **forced** on 170 of them — the one exception is the background
  job queue, whose rows hold only ids and a kind. The database refuses
  cross-tenant reads whatever the application asks for; it is not a `WHERE`
  clause somebody can forget, and the role the API connects as cannot turn a
  policy off.
- A platform administrator is **not** a user of any business. Signing in as one
  does not grant access to a shop's data, and the test suite asserts it.
- A till resolves its own company from its registered device. A terminal cannot
  name a company, so it cannot print another company's letterhead — and both
  might belong to the same tenant, where row-level security would not object.
- Console routes 404 on the business hostname. Business routes 404 on the
  console hostname.

---

## 7. Deployment checklist

Before the first client:

- [ ] `.env` from `.env.example`, every `CHANGE_ME` replaced
- [ ] `RAWSYST_APP_URL`, `RAWSYST_CONSOLE_HOST`, `RAWSYST_CONSOLE_URL` set
- [ ] `RAWSYST_PLATFORM_EMAIL` set, then `bootstrap` run once
- [ ] `RAWSYST_RESEND_API_KEY` and `RAWSYST_MAIL_FROM` set, sender domain
      verified in Resend
- [ ] `API_BIND=127.0.0.1` and `WEB_BIND=127.0.0.1` if a reverse proxy is in
      front — otherwise the application answers on the server's public IP over
      plain HTTP and Cloudflare protects nothing
- [ ] DNS for both hostnames, certificates covering both
- [ ] `docker compose --profile setup run --rm regulatory` reports nothing
      outstanding for the markets you serve
- [ ] a backup taken, and a restore rehearsed — a backup nobody has restored is
      a belief, not a backup

The proxy and Cloudflare settings are in
[`DEPLOYMENT-SPLIT-DOMAINS.md`](DEPLOYMENT-SPLIT-DOMAINS.md). The boundary
tests behind section 6 are in [`ACCESS-BOUNDARIES.md`](ACCESS-BOUNDARIES.md).
