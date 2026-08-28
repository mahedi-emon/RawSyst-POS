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

## The three scripts, and why there are three

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
They are covered end to end by the Go integration suite against the real
database; what this script checks is the part a browser honestly can, that the
POS loads and reports its pairing state rather than presenting a till that
cannot sell.

### Running them

```
cd backend && go run ./cmd/devseed        # prints a password
cd web && npm run dev                     # localhost:3000
cd pos && npm run dev                     # localhost:5173, optional

RS_PASSWORD=... node e2e/audit.mjs
RS_PASSWORD=... node e2e/layout-probe.mjs
RS_PASSWORD=... RS_POS=http://localhost:5173 node e2e/workflows.mjs
```

`shots.mjs` is the fourth and is not a check: it writes a screenshot of every
screen at every width in both languages, for the judgements the others refuse to
make. Whether a screen is crowded, whether the hierarchy reads, whether the
Arabic mirror actually mirrors — those need eyes, and this is how you get them
in front of a person quickly.
