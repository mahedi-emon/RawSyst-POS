# The web back-office

The same screens as the till, delivered to a browser.

## Why it exists separately

A till is a fixed appliance on a counter, and the Tauri POS is an installed
desktop binary. A back office is a person on whatever they happen to have — a
laptop at the shop, a phone in a taxi, a tablet at home on Sunday. The Tauri
build cannot serve the second case, and that is the whole reason this app is
here.

## Nothing here is a rewrite

The dashboard, the drill-throughs and the buying module all live in
`shared/src` and are imported by **both** this app and the POS, from source
rather than through a build step. One less artefact to keep in step, and — the
actual reason — the two surfaces cannot drift onto different versions of the
same component. A figure that meant one thing on a till and another on a laptop
would destroy trust in both.

`web/` therefore contains a shell and nothing else: an auth boundary, a
navigation bar, section state and company selection. **No business logic lives
here and none may.** Every figure on every screen was computed by the Go
service from the same journal the trial balance reads. If something ever needs
working out on this side, that is a signal the server is missing an endpoint —
not an invitation to put arithmetic in a React component nobody can audit.

## The boundary is enforced by a test

`shared/src/portability.test.ts` reads the package's own source and asserts it
imports nothing from `@tauri-apps`, reaches into neither host application, does
no arithmetic on money, and touches no browser global at module scope. That
last one matters because Next.js evaluates modules on the server during a
build, where `window` throws — and the failure surfaces as an opaque build
error rather than the line that caused it.

It is a structural test rather than a behavioural one, and it earns its place:
a Tauri import compiles perfectly in the POS and breaks the web deploy, usually
at deploy time, because a bundler resolves it happily in development.

## Layout

```
shared/   the screens, the API clients, the design system  — used by both
pos/      the Tauri till: offline queue, catalogue cache, cart, receipt
web/      this app: shell only
```

## Running it

```
npm install            # at the repo root; workspaces link the three together
npm run dev -w @rawsyst/web
```

`NEXT_PUBLIC_API_BASE_URL` points at the Go service, defaulting to
`http://127.0.0.1:8080`. It is public because the browser is what calls it: the
Go service is the security boundary, every route it exposes is authenticated
and permission-gated, and QA gates M7 and M8 prove that server-side.

## What it deliberately does not do

- **No URL router.** Sections are state. These screens carry no deep links
  anybody shares, and a router would be a second navigation model to keep in
  step with the POS's.
- **No server-side rendering of business data.** There is no server-rendered
  version of "what is my cash position right now" that would still be true by
  the time it arrived.
- **No POS.** Selling, returns, exchanges and the receipt stay on the terminal,
  where the offline queue, the local catalogue and the signing gate live.
