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

The supplied Biz1core artwork is the source of truth: the **Biz1core** wordmark
in a heavy geometric sans, the **1** in blue, and a **teal growth leaf sweeping
over the B with three ascending bars at its foot**. The tagline sits beneath.

It arrived as a raster image. [`scripts/brand-assets.mjs`](../scripts/brand-assets.mjs)
traces it into one scalable drawing and then writes every file the product needs
from that one drawing — so the rail, the sign-in page, the browser tab, the home
screen and the Windows installer are the *same* logo rather than a set of
similar ones.

### How the wordmark became paths

The artwork sets the name in a heavy geometric sans. **Poppins ExtraBold** is
the closest open face to it — the circular `o`/`c`/`e`, the flat-cut `z`, the
flagged `1` with no foot — and the generator converts it to **outlines**. The
tagline is **IBM Plex Sans Medium**, which is both the neutral grotesk the
artwork uses and the typeface the product already loads.

Both are SIL Open Font License. The two TTFs and their licences are vendored at
[`shared/src/brand/fonts/`](../shared/src/brand/fonts/) so the logo is
reproducible from a clean checkout.

Outlines rather than live text, deliberately. A logo set as text is a *different
logo* on a machine without the typeface, and it cannot be a favicon or a Windows
icon at all. As paths it is the same eight letterforms at 14px in a rail and at
96px on the About page — and the word cannot go missing or be misspelled,
because it is not a string being rendered. A first attempt did use live text in
the product's own face, and that is exactly what made it not the supplied logo.

### The coordinate space

The artwork's own: cap height 260, baseline at `y = 290`, the B starting at
`x = 60`. Every constant in the generator was measured against it, and
`shared/src/brand/logo-paths.ts` — generated, never hand-edited — carries the
traced paths plus the metrics each variant crops to.

### Variants

```tsx
<Biz1coreLogo height={25} onDark />              // leaf + bars + "Biz1core"
<Biz1coreLogo variant="lockup" height={82} />    // the above + the tagline
<Biz1coreLogo variant="wordmark" height={24} />  // "Biz1core" with no mark
<Biz1coreMark height={24} onDark />              // the B with its leaf and bars
```

- `height` is the only size control. Every other dimension is derived, because
  the whole logo is one drawing in one space — nothing can drift out of
  proportion or be stretched.
- `onDark` is for a surface that is dark **whatever the page theme is**. The
  navigation rail is the case in both themes, and the navy wordmark is invisible
  on it.
- `mono` draws the whole logo in `currentColor`, for a print header or a
  single-colour context.
- `variant="mark"` is a **crop of the official logo** — the B of the wordmark
  with the leaf and bars over it — not a separate symbol. Every square icon in
  the product is generated from it.
- `variant="lockup"` is the only variant that shows the tagline. Nine words do
  not fit a 248px rail: they wrap to three lines or truncate to "One Solution
  for Comp…". The lockup belongs on the sign-in page and the About page, where
  it renders at 82–96px and the tagline is actually readable.

### Colour

Five values, read off the artwork, in
[`shared/src/brand/brand.css`](../shared/src/brand/brand.css) — used by the logo
and by nothing else:

| Token | Light | Dark / on the rail |
|---|---|---|
| `--biz1core-word` — "Biz" and "core" | `#16213e` | `#f2f5f9` |
| `--biz1core-accent` — the "1" | `#1273e6` | `#5aa2ff` |
| `--biz1core-leaf` — the growth curve | `#2bb89a` | `#3ed0ae` |
| `--biz1core-bar` — the three bars | `#1fa483` | `#35c39c` |
| `--biz1core-tag` — the tagline | `#3c4a63` | `#c3cddd` |

The dark row is not the light row darkened. Navy on a dark rail is invisible and
the blue and teal go muddy, so each is lifted to the step that carries against
the darkest surface in the product.

These are deliberately **not** part of either design system's semantic scale.
This repository holds two — the till and the shared screens on navy and blue,
the Next.js back office on green and brass — and the logo has to sit correctly
on both, on a printed invoice and in a browser tab. So the logo carries its own
colours, and nothing else may reach for them: an accent that starts appearing on
buttons stops being a brand accent and becomes a third primary colour.

Print turns the whole logo black — colour on a mono laser makes the blue and the
teal into the same indistinct grey, and a letterhead wants solid black anyway.
Forced colours turn it to `currentColor`.

### Files

Everything is generated. Re-run after any change to the constants:

```sh
node scripts/brand-assets.mjs
```

| File | For |
|---|---|
| `shared/src/brand/logo-paths.ts` | What the React component renders |
| `web-next/public/brand/biz1core-logo.svg` / `-dark` / `-mono` | Horizontal logo |
| `…/biz1core-logo-tagline.svg` / `-dark` | With the tagline |
| `…/biz1core-wordmark.svg` / `-dark` | Wordmark only, no mark |
| `…/biz1core-mark.svg` / `-dark` / `-mono` | The compact mark, transparent |
| `…/app-icon.svg`, `app-icon-maskable.svg` | The mark on its tile — the source for every raster |
| `…/og-image.svg`, `og-image.png` | The 1200×630 social card |
| `web-next/public/icons/icon-{16…512}.png` | Fourteen sizes, plus 192 and 512 maskable |
| `web-next/public/favicon.ico` | 16/32/48, which browsers ask for by name |
| `pos/src-tauri/icons/icon.ico` | 16–256, for the Windows installer and taskbar |
| `pos/src-tauri/icons/{32,128,256,512}.png`, `icon.png` | The Tauri bundle |
| `pos/public/favicon.ico`, `pos/public/brand/` | The till in a browser during development |
| `web/public/icons/`, `web/public/favicon.ico` | The dead previous back office, kept in step so no stale mark survives anywhere |

Do not edit any of them by hand. Change the constants at the top of the
generator and re-run it, or the files stop agreeing with each other and with the
component — which is the one failure a single source of truth exists to prevent.

## 3. Where the logo appears

| Surface | Variant | Drawn at |
|---|---|---|
| Navigation rail, desktop | `full`, `onDark`, shop name beneath | 25px |
| Navigation drawer, mobile | the same component | 25px |
| Operator console rail and drawer | `full`, `onDark`, "Console" beneath | 25px |
| Sign-in page | `lockup`, `onDark` | 82px |
| Forgot password | `lockup`, `onDark` | 82px |
| Change password | reached from sign-in; the card carries no second lockup | — |
| Shared sign-in (the till, and the previous back office) | `lockup` | 78px |
| Till top bar | `full`, `onDark` | 24px |
| Opening screen | `full` | 30px |
| Error boundary, 404 | `full` | 30px |
| About page | `lockup` | 96px |
| A business with no logo of its own, during onboarding | `mark`, `onDark` | 26px |
| Browser tab, home screen, installed app, Windows installer | `app-icon` | 16–512px |
| A pasted link (Open Graph, Twitter card) | `og-image` | 1200×630 |

There is no public landing page and no registration page: the root redirects
to the workspace the session belongs in, and accounts are provisioned by an
operator rather than self-registered. Two-factor is a step inside the sign-in
form, not a page of its own, so it sits under the sign-in lockup. If any of
those three is built later it takes `lockup`.

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
