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
