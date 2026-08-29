# Browser audit

An authenticated walk of the back office in a real browser, at three device
sizes, in both languages.

## Why this exists

Three classes of defect are invisible to unit tests, to a typecheck, and to
reading the source. All three have shipped in this repository:

- a stylesheet the browser app never imported, so primary navigation rendered
  as bare text buttons on desktop while every unit test passed
- English left on an Arabic screen in places a source scan structurally cannot
  see — text passed as a JSX attribute, prose containing an inline element,
  labels under the length floor
- controls too small to tap, and pages that scroll sideways

The most recent run found 69 of the third kind on tablets, which were
comfortable at 390px and comfortable at 1440px and awkward only on the device
most likely to be propped up on a counter.

## Running it

```sh
# 1. a database with the dev shop in it
cd backend && go run ./cmd/devseed        # prints an email and password
cd backend && go run ./cmd/api            # :8080

# 2. the back office
cd web && npm run dev                     # :3000

# 3. the audit
RS_PASSWORD='<the password devseed printed>' node e2e/audit.mjs
```

It exits non-zero when it finds something, so it can gate a build.

| variable | default |
|---|---|
| `RS_WEB` | `http://localhost:3000` |
| `RS_EMAIL` | `owner@example.test` |
| `RS_PASSWORD` | *required* |

## What it checks

| check | applies to |
|---|---|
| page scrolls sideways | every device |
| tap target under 44px | phone and tablet, measured on the label when one wraps the control |
| input a screen reader cannot name | every device |
| `dir=rtl` | Arabic |
| English left on the product's own chrome | Arabic |
| console errors | every device |
| requests returning 400 or worse | every device |

## What it deliberately does not check

Anything cosmetic. It reports facts a machine can be sure about. Whether a
layout *looks* right is a judgement, and a test that guessed at it would either
pass on ugly screens or fail on good ones.

Two things it gets right that a naive version does not, both learned by getting
them wrong first:

- **The tap target is the label** when one wraps the control. A 13px checkbox
  inside a 44px label is fine; measuring the input alone condemns a perfectly
  usable control.
- **The page overflows only when `body.scrollWidth` exceeds `clientWidth`.** An
  element extending past the viewport inside a clipped ancestor is not an
  overflow — the visually-hidden table header is off-screen on purpose, so
  screen readers keep the column associations.

It also emulates touch rather than merely resizing the window. The tap rules
ask `pointer: coarse`, and a resize does not change what the browser reports as
its pointer, so a resize-only audit measures a mouse-sized layout and calls a
phone comfortable.

## The scripts, and why there are several

They answer different questions and none of them substitutes for another.

| Script | Question |
|---|---|
| `audit.mjs` | Is anything *measurably* wrong? Sideways scroll, tap targets under 44px, inputs a screen reader cannot name, missing `dir=rtl`, English on Arabic chrome, console errors, failed requests. 54 section/language/device combinations. |
| `layout-probe.mjs` | Does the layout hold together? Text overflowing its own box, an element escaping a parent that is not clipping on purpose, a column header read from the opposite edge to the cells beneath it. |
| `workflows.mjs` | Does it **work**? Signs in, is refused with a wrong password, opens the order list, the new-order form, the bills, a bill's three-way match evidence, the supplier ageing, a customer statement, settlement, inventory, the dashboard and a drill-through, terminals, e-invoicing, branding and setup. |

The first two never press a button that changes anything, so between them they
prove the app RENDERS and prove nothing about whether it works. That is what
the third is for.

### What `workflows.mjs` deliberately does not cover

Sale, return, exchange and the shift live in the Tauri POS, which holds its
device credential in the OS keystore through the Rust shell. A plain browser has
no keystore, so a browser-run POS cannot pair and therefore cannot sell — see
`pos/src/offline/credential.ts`, which refuses rather than falling back. Driving
those flows here would mean weakening the custody model to make a test pass.
What this script checks is the part a browser honestly can: that the POS loads
and reports its pairing state rather than presenting a till that cannot sell.

`tauri.mjs` is where that stops being a gap.

## `tauri.mjs` — the till, driven as the installed application

The fourth script and the only one that is not a browser. `tauri-driver` proxies
WebDriver to the real WebView2 control inside the real packaged binary, so the
Rust shell underneath is the real shell: `terminal_keystore_available()` answers
true here and false in a browser, which is the exact difference that makes a
browser unable to test a till.

It walks the whole counter — pair, sign in, open the shift, sell, look a receipt
up, refund, exchange, take a card payment, sell through an outage, drop cash to
the safe, and close blind with a deliberate five-riyal shortfall — and reads
every result back **through the API**, never off the screen that asked for it.

```sh
cargo install tauri-driver --locked
# msedgedriver matching the installed WebView2 runtime
cd pos && npm run build && npx tauri build --no-bundle
cd backend && go run ./cmd/devseed && go run ./cmd/api

RS_PASSWORD=... RS_EDGEDRIVER=C:/path/to/msedgedriver.exe node e2e/tauri.mjs
```

`npx tauri build --no-bundle` and not `cargo build --release`: the latter leaves
the binary pointing at the dev server, so the window comes up blank and every
assertion below fails for a reason that has nothing to do with the product.

| variable | default |
|---|---|
| `RS_POS_EXE` | `pos/src-tauri/target/release/rawsyst-pos.exe` |
| `RS_EDGEDRIVER` | whatever `tauri-driver` finds on `PATH` |
| `RS_API` | `http://127.0.0.1:8080` |
| `RS_SHOTS` | off; a directory writes a PNG of each screen as the run reaches it |
| `RS_PASSWORD` | *required* |

`RS_SHOTS` is how the till's screens get in front of a person. `shots.mjs`
photographs the back office and cannot photograph this: a browser has no
keystore, so it stops at the pairing screen and the counter, the shift and the
returns screen are unreachable. Setting it turns the run that already asserts
they work into the run that shows you them.

It found the counter running off the right of a 1280px till, with the Total and
the Mada button outside the screen; a refund reporting a primary key where the
credit note's number belonged; the mode switch on the returns screen laid out
as two payment-sized buttons; the shift panel a hundred pixels left of centre
with every figure right-aligned away from its own label; and the whole till in
English on an Arabic terminal.

### What it has found

Every one of these is a defect that shipped, that no unit test could see, and
that a browser could not have reached — the two halves are in different
languages in different directories and each is correct on its own.

| Defect | Where it was fixed |
|---|---|
| The API's CORS allow-list held the two dev-server origins and not the till's, so every installed terminal failed sign-in with "Sign-in did not complete." | `backend/cmd/api/main.go`, pinned by `cors_test.go` |
| The POS defaulted to `http://localhost:8080`, which its own CSP `connect-src` does not name | `pos/src/main.tsx` |
| `terminal_sign_in` did not send `X-Client-Kind: native`, so the server answered in a shape the till rejected | `pos/src-tauri/src/enrolment.rs` |
| No Tauri v2 capability file, so the ACL denied every `plugin:sql` command and the till had no local storage | `pos/src-tauri/capabilities/default.json` |
| The SQLite schema was one string split on `;`, and a comment containing a semicolon broke it mid-statement | `pos/src/offline/sqlite.ts` |
| A receipt carries the document UUID; every sales route takes the invoice id, which the till never learns — so **no sale made at a till could be found to return** | `GET /api/v1/pos/sales/lookup`, `pos/src/pos/ReturnsScreen.tsx` |
| Every server refusal was classified as a dead network, so a wrong pairing code told the cashier to check the connection and promised the code was still valid | `pos/src/offline/credential.ts` |

### A note on the enrolment rate limit

The run deliberately submits a wrong code first, and the server allows five
misses per quarter hour from one address. Runs closer together than that will
wait for the limit, and the failure says so. The limiter is in memory, so
restarting the API clears it.

### Running them

```
cd backend && go run ./cmd/devseed        # prints a password
cd web && npm run dev                     # localhost:3000
cd pos && npm run dev                     # localhost:5173, optional

RS_PASSWORD=... node e2e/audit.mjs
RS_PASSWORD=... node e2e/layout-probe.mjs
RS_PASSWORD=... RS_POS=http://localhost:5173 node e2e/workflows.mjs
```


## Four source lints

Neither needs a browser, a database or a running server. Both exist because
they found something the type checker, the unit tests and a browser walk all
looked straight past.

```
node e2e/classes.mjs
node e2e/strings.mjs
node e2e/tokens.mjs
node e2e/currency.mjs
node e2e/pos-spec.mjs
```

`classes.mjs` compares the class names the components use against the names the
stylesheets define. A missing rule is not a build error, not a type error and
not a runtime error — it is simply a screen that looks wrong to whoever opens
it. It found `.dialog`, `.dialog__backdrop` and `.dialog__body`, which meant the
one dialog in the product that asks somebody to type a name before doing
something irreversible rendered as unstyled markup over an invisible backdrop.

`strings.mjs` finds user-facing English that never reaches the catalogue: prose
passed as a prop, and template literals that stitch English around a value. It
found forty-one, including every empty state on the purchasing screens and the
`aria-label` on each numeric input of a purchase order — which said "Quantity
billed on line 3" in English to a screen reader whatever language the shop had
chosen, to the one reader with nothing else to go on.

`tokens.mjs` finds a colour, a spacing step or a radius written into a
stylesheet where a design token already names it. A design system is only a
system while every screen reaches for the same names; the moment one file writes
`#1f6feb` and another writes `var(--brand)`, the two drift on the next change
and nobody finds out until somebody opens both screens side by side. It found 55
— all in the till, which had never been moved onto the shared scale.

`currency.mjs` asks a per-screen question: does this screen show money and never
say which currency? Repeating the code on every cell of a column is noise and no
commercial system does it, so a bare `money(x)` is not itself a defect — a
screen where EVERY figure is bare is. It found five, two of which had no
currency in the API at all.

`pos-spec.mjs` is different from the others: it reads the numbers out of
`pos/src/styles.css` and compares them with the "Non-negotiables" table in
`docs/ui-ux/01-screen-specs.md`. Three were wrong by 4px, 4px and 3px. Nobody
would have caught any of them by looking, which is the whole argument for
checking them: a requirement specified precisely, implemented approximately, and
then left to drift.

The first three report rather than fail — some class names are built at runtime,
some strings genuinely are the same in every language, and a one-off value is
sometimes right. `pos-spec.mjs` exits non-zero, because a number the spec fixes
either matches or does not.

## The screenshots

`shots.mjs` is not a check: it writes a screenshot of every screen at every
width in every language, for the judgements the others refuse to make. Whether
a screen is crowded, whether the hierarchy reads, whether the Arabic mirror
actually mirrors — those need eyes, and this is how you get them in front of a
person quickly.

```
RS_PASSWORD=... RS_WIDTH=desktop RS_LANGS=en,ar,bn RS_OUT=shots node e2e/shots.mjs
```

`RS_LANGS` takes locale codes, not the names in the menu. Passing "العربية"
through an environment variable came back re-encoded, the option never matched,
and the run produced a second set of screenshots that were quietly identical to
the English ones.

## Getting data onto the screens

```
RS_PASSWORD=... node e2e/seed-trade.mjs
```

A dev database has one company, one store, four products and no history, so
every screen the other scripts walk was empty — and every judgement about the
dense screens was being made against an empty state. This puts customers with
credit limits, suppliers, three purchase orders in three different states, a
delivery, a bill and a week of expenses into the shop.

Everything goes through the real HTTP API with a real owner's token, so what
lands in the database is data the product could actually have produced. Nothing
is inserted behind the business rules' back, and nothing here is asserted on —
it is a tool, like `shots.mjs`, not a test.

It found two things on its first run that an empty screen could not have shown:
the navigation rail had lost its labels at desktop width, and the dashboard's
short panel left 270px of grey below itself once the panel beside it had rows.
