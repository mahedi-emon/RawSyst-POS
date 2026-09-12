# Branding

The product is **Biz1core**. The tagline is **One Solution for Complete
Business Management.** Both are spelled exactly that way everywhere: a capital
B, the digit one, a lower-case `core`; the tagline carries its full stop,
because it is a sentence.

The product was called RawSyst POS until the rename of 2026-09-12. What that
rename touched, and what it deliberately did not, is the second half of this
document.

---

## 1. Where the name lives

Once. In [`shared/src/brand/brand.ts`](../shared/src/brand/brand.ts):

```ts
PRODUCT_NAME       // 'Biz1core'
PRODUCT_TAGLINE    // 'One Solution for Complete Business Management.'
PRODUCT_TITLE      // 'Biz1core | Complete Business Management'
POS_NAME           // 'Biz1core POS'
CONSOLE_NAME       // 'Biz1core Console'
OWNER              // name, role, website, LinkedIn, GitHub
REPOSITORY_URL
```

Every page title, manifest, logo, footer and About panel reads from there. The
sentences a customer reads in *their own language* still live in the i18n
catalogues — a catalogue can say "Biz1core cannot reach the server" in Arabic,
which a constant cannot — but the brand itself is one string.

The Arabic catalogue writes the wordmark in Latin rather than transliterating
it. That is a decision about this mark: it contains a digit, it has no Arabic
form, and inventing one would print a name on a subscription invoice that the
vendor does not use. Mada becomes مدى because مدى is the scheme's own Arabic
name; Biz1core has none.

## 2. The logo

Three ascending bars with a curve rising clear above them: the figures a
business keeps, and the direction it wants them to go.

It is four shapes, and that is the whole design. A favicon is sixteen CSS
pixels across, and anything with more detail than this is a grey smudge at that
size. There is no gradient, no shadow and no bevel for the same reason. The
curve clears every bar top by at least two units — checked in the geometry, not
eyeballed — so the four shapes stay four shapes.

### On screen: live text, not a picture

[`shared/src/brand/Logo.tsx`](../shared/src/brand/Logo.tsx) draws the mark as
inline SVG and sets the word **Biz1core** as *text*, in the typeface the
application has already loaded. So the wordmark is vector-crisp at 14px in a
collapsed rail and at 40px on a sign-in page, it inherits the text colour of
whatever surface it sits on, it costs no network request, and it produces no
layout shift.

It also removes one whole class of failure: the word cannot go missing,
distorted or misspelled, because it is `PRODUCT_NAME` rendered as text.

```tsx
<Biz1coreLogo />                              // mark + wordmark
<Biz1coreLogo variant="lockup" size={30} />   // mark + wordmark + tagline
<Biz1coreLogo size={23} onDark sub={shop} />  // rail: logo with the shop under it
<Biz1coreMark size={24} />                    // the four shapes alone
```

- `onDark` is for a surface that is dark **whatever the page theme is** — the
  navigation rail is the case. Without it the logo reads the page theme and, in
  light mode, takes the light accents onto a dark rail.
- `variant="lockup"` is the only variant that shows the tagline. Nine words do
  not fit a 248px rail: they wrap to three lines or truncate to "One Solution
  for Comp…", and a tagline nobody can read is worse than no tagline. The
  lockup belongs on the sign-in page and the About page.

### Colour

Two accents, in [`shared/src/brand/brand.css`](../shared/src/brand/brand.css),
used by the logo and by nothing else:

| Token | Light | Dark / on the rail |
|---|---|---|
| `--biz1core-accent` (the "1", and the curve) | `#1f6fe0` | `#7db0ff` |
| `--biz1core-mark-bar` (the three figures) | `#12876a` | `#3fcfa9` |

The word itself is always `currentColor`.

These are deliberately **not** part of either design system's semantic scale.
This repository holds two — the till and the shared screens on a navy-and-blue
palette, the Next.js back office on green-and-brass — and a logo has to sit
correctly on both, on a printed invoice and in a browser tab. An accent that
starts appearing on buttons stops being a brand accent and becomes a third
primary colour, so nothing but the mark may reach for these.

Print turns both accents black: a logo printed in colour on a mono laser makes
both an indistinct grey, and a letterhead wants solid black anyway. Forced
colours turn them to `currentColor`.

### Files

Everything under [`web-next/public/brand/`](../web-next/public/brand/) is
generated from one description of the geometry by
[`scripts/brand-assets.mjs`](../scripts/brand-assets.mjs):

```sh
node scripts/brand-assets.mjs
```

| File | For |
|---|---|
| `biz1core-logo.svg` / `-dark.svg` | Horizontal lockup, light and dark surfaces |
| `biz1core-logo-tagline.svg` / `-dark.svg` | The same with the tagline |
| `biz1core-logo-mono.svg` | Monochrome, print-safe |
| `mark.svg` / `mark-dark.svg` / `mark-mono.svg` | The compact mark |
| `app-icon.svg` | The mark on its tile — the favicon, and the source for every raster |
| `app-icon-maskable.svg` | The same inside a launcher's safe area |
| `../icons/icon-192.png`, `icon-512.png`, `icon-512-maskable.png` | The PWA manifest |
| `../favicon.ico` | `/favicon.ico`, which browsers ask for by name |
| `../../pos/src-tauri/icons/icon.ico` | The till's installer, taskbar and window |

Do not edit those by hand. Change `MARK` in the generator and re-run it, or the
eleven files stop agreeing with each other.

## 3. Where the logo appears

| Surface | Variant |
|---|---|
| Navigation rail (desktop) and drawer (mobile) | `wordmark`, `onDark`, shop name beneath |
| Operator console rail | `wordmark`, `onDark`, "Console" beneath |
| Sign-in, forgot password | `lockup` |
| Till top bar | `wordmark`, `onDark` |
| Shared sign-in (till and legacy back office) | `lockup` |
| Opening screen, error boundary, 404 | `wordmark` |
| About page | `lockup` |
| Browser tab, installed app, Windows installer | `app-icon` |

### Where it deliberately does **not** appear

**Invoices, receipts, statements and every other document a customer takes
away.** Those carry the *shop's* logo, uploaded under Business settings, and
fall back to the shop's own initial. The vendor's mark on a shop's invoice is
the vendor advertising on paper a customer thinks belongs to the shop.

**The developer's name, anywhere inside the workspace.** It is on the sign-in
page and the About page, and nowhere else. A shopkeeper working in their own
software all day does not need the vendor's name under every screen.

## 4. Old identifiers deliberately kept

The rename changed what people read. It did not change identifiers that a
running deployment, a stored backup, a live session, a dashboard or a wire
protocol depends on. Each of these is still spelled the old way, on purpose.

| Identifier | Why it stays |
|---|---|
| `RAWSYST_*` — every environment variable (about 95 of them) | Every existing `.env`, every compose override, every systemd unit and every operator's runbook names them. Renaming would break every deployment on upgrade for no gain a user can see. |
| PostgreSQL database `rawsyst`, owner role `rawsyst`, backup role `rawsyst_backup` | Renaming means re-provisioning every existing database and re-granting row-level security. The names appear only in a DSN. |
| `rawsyst_verify_*`, `rawsyst_restore_*`, `rawsyst_pre_restore_*`, `rawsyst_rolledback_*` | Restore and verification create and then look for databases by these prefixes. A half-finished restore leaves one behind, and a rename would make the rollback path unable to find it. |
| `RawSyst_Backup_*`, `RawSyst_BaseBackup_*` and the object-store prefix `rawsyst` | Every backup already taken is named this way. `backup verify`, `backup check` and `backup restore-file` match on it. Renaming would make existing backups unrestorable, which is the worst possible outcome of a cosmetic change. |
| `cryptMagic = "RAWSYSTB"` in `backup/crypt.go` | The eight-byte magic number at the head of every encrypted backup. `Open` refuses a file that does not begin with it. The rename sweep changed this to `"BIZ1COREB"` — nine bytes, and unreadable by anything that wrote a backup before — and the backup suite failed within the minute. Reverted. |
| `"rawsyst-backup-key\0"` in `backup/crypt.go` | The domain separator mixed into the backup key's fingerprint. It is hashed, so a different string derives a different fingerprint from the same key — and the fingerprint is what an operator compares against the one recorded beside a stored backup to decide whether they hold the right key. Renaming would report the correct key as wrong for every existing backup. Also reverted. |
| Locale cookie `rawsyst_locale` | The chosen language, read by the server so the first painted frame is already in the right language and direction. Renaming resets every reader to English once. |
| Prometheus metric names `rawsyst_*` | Dashboards and alert rules key off metric names. A rename silently breaks every alert and leaves the graphs flat instead of red. |
| Session cookies `rawsyst_refresh`, `rawsyst_csrf` | Renaming signs out every live session at once. Invisible to a user; disruptive for no benefit. |
| WebSocket subprotocol `rawsyst.auth`, LISTEN/NOTIFY channel `rawsyst.live` | Both ends have to agree on the string. Changing it needs a coordinated deploy of every till and browser at the same instant. |
| Compose project name `rawsyst` (`name:` in every `docker-compose*.yml`) | It is the prefix of every volume Docker created — `rawsyst_db-data` among them. Changing it orphans a live deployment's database. |
| `RAWSYST_BACKUP_PREFIX=rawsyst`, `POSTGRES_DB`/`POSTGRES_USER` defaults | Same reasons as the two rows above: they name existing storage. |

Everything else moved. That includes some things that could have been argued
either way and were changed because nothing depends on them yet — this product
has no third-party integrators and no production tenants:

- The Go module path is now `github.com/mahedi-emon/Biz1core/backend`, matching
  the repository it actually lives in.
- The npm scope is `@biz1core/*`.
- The single binary is `biz1core` (`biz1core api`, `biz1core backup`, …),
  entered as `/biz1core` in the images.
- Webhook headers are `X-Biz1core-Event`, `X-Biz1core-Delivery`,
  `X-Biz1core-Signature`, and the user agent is `Biz1core-Webhook/1`. Documented
  in [API conventions](system-design/07-api-conventions.md).
- Docker images are `biz1core/backend`, `biz1core/backup`, `biz1core/web`,
  `biz1core/postgres`.
- The systemd units are `biz1core-backup`, `biz1core-basebackup`,
  `biz1core-drill`, and the hourly check is `biz1core-check.sh`.
- The default JWT issuer is `biz1core-pos`. **A deployment that does not set
  `RAWSYST_JWT_ISSUER` explicitly will reject access tokens issued before the
  upgrade**, so everybody signs in once more. Access tokens are short-lived;
  the alternative was carrying a dead name in every token forever.
- Report exports download as `biz1core-<report>-<date>.csv`.
- The authenticator entry is issued under the issuer `Biz1core`. Existing
  entries keep working — the issuer is a label, not part of the secret — and
  show the old name until they are re-enrolled.
- Two Prometheus **label values** moved, while the metric **names** stayed: the
  scrape job is `biz1core-api` and `DEPLOYMENT_NAME` defaults to `biz1core`.
  Metric names are what alert rules key off and they are untouched, but a
  dashboard that filters on `job="rawsyst-api"` or `deployment="rawsyst"` needs
  its query updated. A deployment that sets `DEPLOYMENT_NAME` explicitly is
  unaffected.
- Migration `0056_document_templates.sql` still names the old product in a SQL
  **comment** describing a default document template. A comment is not data and
  nothing reads it; the alternative is breaking the checksum of a second
  applied migration to reword a sentence nobody sees. `0140` carries the one
  change to actual data — the `system_name` on the PDPL processing-activity
  register — and explains why it could not be an edit to `0096`.

## 5. Owner

Biz1core is owned and developed by **Mahedi Hasan Emon**, Founder, Owner &
Lead Developer.

- <https://mahedihasanemon.site/>
- <https://www.linkedin.com/in/mahediemon/>
- <https://github.com/mahedi-emon>
