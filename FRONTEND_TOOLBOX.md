# RawSyst — Frontend/UI Toolbox

| | |
|---|---|
| **Last updated** | 2026-09-02 |
| **Scope of this document** | What UI tooling is available to an agent working in this repository, where it came from, and what state it is in |
| **What this document is not** | A set of rules about which tool to use for which job. There are none, deliberately — see §7 |

---

## 1. What this is, and why it is shaped this way

RawSyst has a hand-built design system. `shared/src/design-system.css` is 2,290
lines of plain CSS custom properties with no Tailwind, no CSS-in-JS and no
component library underneath it; `web/app/back-office.css` is another 4,287
lines on top of it, and `pos/src/styles.css` a further 1,119. Both front ends
import the design system first and then their own sheet. Type is self-hosted
through `@fontsource` because the till is an offline-first Tauri app and a shop
whose broadband is down must not lose its typeface.

That is a real design system, arrived at deliberately, and nothing in this
toolbox replaces it. The toolbox exists so that an agent doing UI work in this
repository can *reach* the wider ecosystem — read how Magic UI implements a
number ticker, check a layout against Vercel's Web Interface Guidelines, ask
GSAP how to sequence a timeline properly — and then write the result in
RawSyst's own idiom.

The two consequences worth stating up front:

- **shadcn/ui is available as a source to read from, not as a dependency.**
  Running `shadcn init` would install Tailwind, create a second `components/ui`
  tree and give this product two visual languages. See §4.
- **Nothing here is mandatory.** A tool being installed is not a reason to use
  it. See §7.

---

## 2. Where each piece lives

Four different mechanisms, because the ecosystem uses four different mechanisms.
They are not interchangeable and the difference matters when someone clones this
repo on another machine.

| Mechanism | Stored in | Committed? | Reproduced by |
|---|---|---|---|
| **Claude Code plugins** | `.claude/settings.json` (`enabledPlugins`, `extraKnownMarketplaces`); content cached under `~/.claude/plugins/` | yes (the declaration) | clone + restart Claude Code |
| **Agent Skills** (`skills.sh` format) | `.claude/skills/<name>/`. The eight installed by `npx skills` are pinned in `skills-lock.json`; the seven `21st-*` ones were installed by the 21st CLI and are pinned in `.21st/skills.lock`, which is gitignored | yes (the files, all fifteen) | already present; `npx skills experimental_install` restores the eight |
| **This repository's own skill** | `.claude/skills/rawsyst-design-system/` | yes | Nothing to restore — it is written here, not fetched. It is the only skill in the tree with no upstream, and the only one you should edit |
| **MCP servers** | `.mcp.json` | yes | clone + restart Claude Code |
| **Slash commands** | `.claude/commands/ds/` | yes (the files) | already present |

Serena's MCP entry is **not** in `.mcp.json`. It is configured at user scope in
`~/.claude.json` and was left exactly as it was found.

---

## 3. Status of every requested tool

Legend: **✅ installed** · **✅ available** (reachable, no install needed) ·
**⚠️ credential** (configured, blocked on a secret) · **↔ superseded**
(capability present through a different maintained implementation) ·
**📖 reference** (a resource to read, not a thing to install)

### 3.1 Design and frontend skills

| # | Requested | Status | Current implementation | Scope | Verified |
|---|---|---|---|---|---|
| 06 | Anthropic `frontend-design` | ✅ already present | `frontend-design@claude-plugins-official` — the current official marketplace, not the bare `anthropics/skills` clone | user | Skill listed and loadable this session |
| 07 | UI UX Pro Max | ✅ installed | `ui-ux-pro-max@ui-ux-pro-max-skill` v2.13.0 (`nextlevelbuilder/ui-ux-pro-max-skill`) | project | `claude plugin list` → enabled |
| 08 | design-taste-frontend | ✅ installed | `taste-skill@taste-skill` (`Leonxlnx/taste-skill`). The bundle's default skill is the directory `taste-skill`, whose `npx skills` install name is `design-taste-frontend`; same artefact, two names | project | `claude plugin list` → enabled |
| 11 | Vercel React Best Practices | ✅ installed | skill `vercel-react-best-practices` | project | Loaded and listed this session |
| 12 | GSAP Skills | ✅ installed | `gsap-skills@gsap-skills` v1.0.0 — 8 skills (core, timeline, scrolltrigger, plugins, react, frameworks, performance, utils) | project | `claude plugin list` → enabled |
| 13 | Motion / Framer | ✅ installed | `motion-framer@claude-design-skillstack` (`freshtechbro/claudedesignskills`) | project | `claude plugin list` → enabled |
| 14 | Convex Agent Skills | ✅ installed (entry point) | skill `convex` — the repo's own documented top-level router into the other 31 Convex skills | project | Loaded and listed this session |
| 15 / 20 | Vercel React Native | ✅ installed | skill `vercel-react-native-skills` | project | Loaded and listed this session |
| 16 | Google Stitch | ✅ installed + MCP connected | `stitch-design`, `stitch-build`, `stitch-utilities` from `google-labs-code/stitch-skills`, plus the hosted MCP at `https://stitch.googleapis.com/mcp`, `X-Goog-Api-Key` auth — §5 | project | `claude plugin list` → 3 plugins enabled; `claude mcp list` → `stitch: ✔ Connected`; `tools/list` → 15 tools |
| 17 | Vercel Web Design Guidelines | ✅ installed | skill `web-design-guidelines` | project | Loaded and listed this session |
| 18 | Vercel Composition Patterns | ✅ installed | skill `vercel-composition-patterns` | project | Loaded and listed this session |
| 19 | Vercel Building Components | ↔ superseded | Folded into `vercel-composition-patterns`, whose own description is "building flexible component libraries, designing reusable APIs… compound components". There is no separate skill in the repo | project | Skill body read and confirmed |
| 20 | Vercel Next.js Best Practices | ↔ superseded | Folded into `vercel-react-best-practices` — "React **and Next.js** performance optimization guidelines from Vercel Engineering" | project | Skill description confirmed |
| 21 | Vercel Optimize | ✅ installed | skill `vercel-optimize` | project | Loaded and listed this session |
| 22 | Emil design engineering / visual polish | ✅ installed | skill `emil-design-eng` from `emilkowalski/skills`. The prompt did not name a repository; this is the maintained one (★34.6k, pushed 2026-08-21) | project | Installed, `skills-lock.json` pinned |
| 23 | Vercel Design Systems → Agent Skills | ✅ installed | Not a runtime skill — a 6-stage **generator pipeline**. Installed as slash commands: `/ds:interview`, `/ds:extract`, `/ds:usage-analysis`, `/ds:prd`, `/ds:generate`, `/ds:assets`, `/ds:port`, plus `scripts/verify-skills.sh` | project | Files present under `.claude/commands/ds/` |
| — | **RawSyst's own design system, as a skill** | ✅ generated | `rawsyst-design-system` — 11 files under `.claude/skills/`, produced by running the §23 pipeline against this repository. See §9 | project | `verify.mjs` passes: 0 undefined classes, 0 undefined tokens, 0 missing paths |

### 3.2 shadcn/ui

| # | Requested | Status | Notes |
|---|---|---|---|
| 04 | shadcn/ui | ✅ available, **not initialized** | See §4. Reachable for reading; not a dependency of this product |
| — | Official shadcn MCP | ✅ installed | `.mcp.json` → `npx shadcn@latest mcp`, exactly the official configuration. 7 tools, verified live |
| — | shadcn Agent Skill | ✅ installed | skill `shadcn` from `shadcn/ui`, project scope |
| 09 | `Jpisnice/shadcn-ui-mcp-server` | ↔ superseded | See §4.3 |
| 10 | 21st "Magic MCP" | ↔ superseded | `@21st-dev/magic` on npm is stuck at 0.2.2 and is the old branding. The maintained implementation is `@21st-dev/cli` 1.17.0 plus the hosted HTTP MCP at `https://21st.dev/api/mcp`; both are configured, and the endpoint answers 401 rather than failing to resolve. The obsolete package was deliberately not installed |

### 3.3 Component sources

| # | Source | Status | How it is reached |
|---|---|---|---|
| 01 | 21st.dev | ✅ installed, signed in | CLI authenticated as `mahedi-emon`, plus 7 first-party skills. The HTTP MCP is configured but still wants its own key — §5 |
| 02 | Skiper UI | ✅ available | Built into the shadcn CLI as `@skiper-ui`: `npx shadcn@latest search @skiper-ui`, `view @skiper-ui/skiper40`. **Not** reachable through the MCP — see §4.4 |
| 03 | UIverse | 📖 reference | https://uiverse.io/ answers 403 to programmatic requests and publishes no registry or MCP. Browse it in a browser and adapt what is useful by hand |
| 04 | shadcn/ui | ✅ available | `@shadcn` — built into both CLI and MCP, no configuration |
| 05 | Magic UI | ✅ available | `@magicui`, declared in `package.json` → `registries`. Works through both CLI and MCP |

### 3.4 Command-line tools

| Tool | Version | Where | Purpose |
|---|---|---|---|
| `@21st-dev/cli` | 1.17.0 | global (`npm i -g`) | `21st search / add / generate / review / skills` |
| `shadcn` | 4.19.1 | `npx`, not a dependency | registry search/view, and the MCP server |
| `skills` | 1.5.23 | `npx` | installs and pins agent skills |
| `plugins` | 1.3.4 | `npx` | Stitch's documented Claude Code install path |

---

## 4. shadcn/ui — the decision, and why

> **Reopened 2026-09-04, for `web-next/` only.** The frontend rebuild brief
> mandates Tailwind and shadcn primitives, so §4 was reopened deliberately.
> What follows is now the reasoning for the two *frozen* front ends rather
> than for the whole repository.
>
> **What changed:** `web-next/` uses Tailwind 4 and a `components.json`, and
> takes `@radix-ui/react-slot` plus the CVA variant pattern from shadcn. It
> still does **not** run `shadcn add`: every primitive in
> `web-next/src/components/ui/` is written for RawSyst against RawSyst tokens
> in `web-next/src/styles/globals.css`. The point of §4.2 — that this product
> must not look like a default shadcn application — is unchanged, and is why
> the palette, the type scale and the table conventions are its own.
>
> **What did not change:** `web/`, `pos/` and `shared/src/design-system.css`
> have no Tailwind and are not to acquire any. §4.1 below is still true of
> them, and the `rawsyst-design-system` skill (§9) remains the authority for
> work in those trees. See `IMPLEMENTATION_PROGRESS.md` §0.2, decision F6.

### 4.1 What was found

There is no Tailwind anywhere in this repository. `grep -rn "tailwind"` across
`shared/`, `web/`, `pos/`, `e2e/` and every `package.json` returns nothing.
There is no `components.json`, no `tailwind.config.*`, and no `lib/utils.ts`
with a `cn()` helper. `web/app/layout.tsx` imports two plain stylesheets and
that is the whole styling stack.

### 4.2 What was therefore *not* done

`shadcn init` was **not** run, on either front end.

Running it would install Tailwind and its PostCSS chain, rewrite the global
stylesheet, create a `components/ui` tree written in utility classes, and add a
`cn()` helper. RawSyst would then have two design systems: 2,290 lines of
documented custom properties, and a second one expressed in Tailwind tokens that
agrees with none of it. Every future component would have to pick a side. The
prompt that commissioned this toolbox asked for exactly the opposite — "do not
create duplicate component systems", "do not overwrite the existing project
architecture", "extend the existing system".

The capability is not lost by declining to initialize it. Everything an agent
actually wants from shadcn here — read the accessibility semantics of a real
combobox, see how a data table handles column pinning, copy the keyboard
handling of a command menu — comes from *reading* registry items, and reading
works with no `components.json` and no dependencies:

```bash
npx shadcn@latest search @shadcn -q "data table"
npx shadcn@latest view @shadcn/data-table      # full source of every file
npx shadcn@latest docs button
```

The MCP does the same from inside a session. What does not work is
`npx shadcn add`, which would write Tailwind-flavoured components into a
project that cannot render them. **Do not run `shadcn add` in this repository**
unless the Tailwind question has been deliberately reopened and settled.

### 4.3 Why `Jpisnice/shadcn-ui-mcp-server` was not installed

Section 9 of the commissioning prompt asked for the official shadcn MCP to be
inspected first and preferred if it now covers the capability. It does. The
official server exposes `search_items_in_registries`,
`view_items_in_registries` (which returns complete file contents),
`get_item_examples_from_registries`, `get_add_command_for_items`,
`list_items_in_registries`, `get_project_registries` and `get_audit_checklist` —
all seven verified live over stdio.

`@jpisnice/shadcn-ui-mcp-server` (v2.0.0, last pushed 2026-05-16) retains one
capability the official server does not have: fetching component source for
**Svelte, Vue and React Native**, and for the Radix-vs-Base-UI split. RawSyst's
front ends are both React, so that capability has no consumer here, and the
server wants a `GITHUB_PERSONAL_ACCESS_TOKEN` to avoid GitHub rate limits.

Installing it would have added a second MCP server answering the same questions
as the first, with a credential attached. If RawSyst ever grows a Svelte or Vue
surface, the command is:

```bash
npx @jpisnice/shadcn-ui-mcp-server --framework svelte
```

### 4.4 Why `@skiper-ui` is not in `package.json`

Skiper UI is built into the shadcn CLI, and searching it works today with no
configuration. It publishes individual items at
`https://skiper-ui.com/r/{name}.json` but serves **no** registry index —
`/r/registry.json` is a 404. Declaring `@skiper-ui` under `registries` therefore
*overrides* the CLI's working built-in with a URL template that cannot answer a
search, and `npx shadcn search @skiper-ui` starts failing. That was tested, and
it does exactly that.

So Skiper is reached through the CLI, which works, and not through the MCP,
which cannot. `@magicui` is declared because Magic UI *does* serve an index, and
declaring it makes the MCP work without breaking the CLI. Both were verified
after the change.

---

## 5. The two hosted MCPs, and what is actually proved

### What works

`21st login` was completed in a browser during setup. The account is
`mahedi-emon`, free tier, and the token lives at `~/.config/21st/auth.json` —
user scope, outside this repository, so nothing secret is committed. Verified:

```
21st whoami   → Logged in as mahedi-emon
21st usage    → Tier: free · free component-code retrievals 2/2 remaining today
21st search "dashboard" → 8 real results
```

`21st skills install --agent claude` then added seven first-party skills at
project scope, all of them under `.claude/skills/` and committed:
`21st-cli-use`, `21st-ai`, `21st-registry`, `21st-design-sync`, `21st-ui-build`,
`21st-ui-explore`, `21st-ui-review`. These drive the authenticated CLI directly,
which is how 21st is actually reached from a session — **the MCP is not required
for any of it.**

Note the free-tier ceiling: search and publishing are unlimited, component-code
retrievals are capped at two a day, and 21st AI generation needs credits. That
cap is a good reason not to reach for 21st reflexively.

### The loose end: the HTTP MCP wants its own key

`.mcp.json` carries the entry the CLI wrote, with one edit:

```json
"21st": {
  "type": "http",
  "url": "https://21st.dev/api/mcp",
  "headers": { "x-api-key": "${API_KEY_21ST:-}" }
}
```

The `:-` was added deliberately. A bare `${VAR}` that is unset makes Claude Code
refuse to start that server, which is a *broken* entry rather than an
unauthenticated one; the default form means the server always loads and simply
answers 401 until a key arrives.

The browser login does not set that variable — **the CLI's stored session token
and an MCP API key are different credentials.** `API_KEY_21ST` is not set here.

What *was* verified, without exposing anything: the endpoint is live and the
transport in the config is correct. `POST https://21st.dev/api/mcp` with an MCP
`initialize` body answers **HTTP 401** — a real server refusing an
unauthenticated caller, not a DNS failure or a wrong path. The server itself is
therefore **configured and reachable but not authenticated**, and no tool call
through it is claimed to work.

To finish it, take a key from https://21st.dev/mcp and set `API_KEY_21ST` in
your environment before starting Claude Code (the CLI also honours
`TWENTYFIRST_TOKEN`). **Set it in the environment, never in `.mcp.json`** — that
file is committed.

### Google Stitch — MCP connected

The endpoint is a **fixed public host**, not a per-account URL:
`https://stitch.googleapis.com/mcp`, authenticated by an `X-Goog-Api-Key`
header. That is what Stitch's own documented setup uses
(https://stitch.withgoogle.com/docs/mcp/setup/, linked from the
`google-labs-code/stitch-skills` README), and it is what the installed skills
read — `upload-to-stitch` looks for "the `X-Goog-Api-Key` header" and an
`--api-url` "defaulting to `https://stitch.googleapis.com`".

```json
"stitch": {
  "type": "http",
  "url": "https://stitch.googleapis.com/mcp",
  "headers": { "X-Goog-Api-Key": "${STITCH_API_KEY}" }
}
```

**The `:-` default is deliberately absent, and that is the whole fix.** This
entry previously read `${STITCH_API_KEY:-}`. With `STITCH_API_KEY` unset that
expands to an *empty* header value, and Claude Code reads an empty credential
as "no credential configured" — so instead of connecting it began an OAuth
handshake: it fetched
`https://stitch.googleapis.com/.well-known/oauth-protected-resource/mcp`
(HTTP 200, `authorization_servers: ["https://accounts.google.com/"]`), then
Google's authorization-server metadata, which has **no `registration_endpoint`**
because Google does not implement RFC 7591 Dynamic Client Registration. Claude
Code gave up with:

```
stitch: ✘ Failed to connect — Incompatible auth server: does not support
dynamic client registration
```

That error was never about Stitch being unreachable. Written without the `:-`,
an unset variable makes Claude Code omit the header rather than blank it, the
OAuth path is never entered, and the config warning names the real problem:
`Missing environment variables: STITCH_API_KEY`.

Verified after the change: `claude mcp list` → **`stitch: ✔ Connected`**, and
`tools/list` returns **15 tools** — `create_project`, `get_project`,
`delete_project`, `list_projects`, `list_screens`, `get_screen`,
`generate_screen_from_text`, `edit_screens`, `generate_variants`,
`upload_design_md`, `create_design_system`,
`create_design_system_from_design_md`, `update_design_system`,
`list_design_systems`, `apply_design_system`. Handshake and discovery need no
key at all.

**Tool calls will need a key.** Get one from
https://stitch.withgoogle.com/settings → **API Keys** → **Create API Key**, then
set it in your environment before starting Claude Code — never in `.mcp.json`,
which is committed:

```powershell
setx STITCH_API_KEY "<the key>"   # user-scope, persists; reopen the terminal
```

There is no OAuth, no client ID and no `gcloud` login in Stitch's supported
Claude Code setup; the API key is the whole mechanism.

---

## 6. Reproducing this on another machine

`.claude/skills/`, `.claude/commands/`, `.mcp.json`, `skills-lock.json` and the
`.claude/settings.json` declarations are all committed, so a clone plus a Claude
Code restart gets almost everything. Two things are not reproduced by the clone:

```bash
# Stitch — installed through its own documented CLI, which registers the
# marketplace in the user-level plugin index rather than in project settings.
npx plugins add google-labs-code/stitch-skills --scope project --target claude-code

# 21st — global CLI, then a per-user browser login
npm install -g @21st-dev/cli@latest
21st login
```

The two hosted MCP servers also need their keys set per machine, in the
environment rather than in any file: `STITCH_API_KEY` from
https://stitch.withgoogle.com/settings and `API_KEY_21ST` from
https://21st.dev/mcp. Both servers connect without them; only tool *calls*
fail.

The seven `21st-*` skill files themselves ARE committed under `.claude/skills/`,
so they need no reinstall; only the login is per-machine. `.21st/skills.lock`,
the 21st CLI's own install record for them, is gitignored: it writes absolute
machine paths and install timestamps, so it would churn on every machine, and
the skills it pins are committed anyway.

Stitch's repository keeps its marketplace manifest at `.agents/plugins/marketplace.json`,
not at `.claude-plugin/marketplace.json`, so `claude plugin marketplace add` cannot
read it directly — that was tried and it fails. `npx plugins add` synthesises the
manifest, which is why Stitch's own README documents that command for Claude Code.

---

## 7. There are no task-to-tool rules

This is the part that is easy to get wrong when a toolbox gets large, so it is
stated plainly: **there is no mapping from a kind of work to a tool.** Not
"shadcn for forms", not "21st for cards", not "GSAP for animation", not "Stitch
for anything". Rules like those would be worse than having no toolbox, because
they replace judgement with a lookup.

What is expected instead, for any UI task:

1. Understand what the shop, the cashier or the accountant actually needs.
2. Read the existing implementation with Serena before writing anything.
3. Read the design system. The `rawsyst-design-system` skill is the fast path —
   it is this product's own system, extracted from the source, and it is almost
   always the *only* thing a UI task needs. `shared/src/design-system.css` and
   `docs/ui-ux/00-design-system.md` remain the authority behind it.
4. Reuse a RawSyst component if one exists. This is usually the answer.
5. Reach outside only when something out there is materially better than what
   can be written directly, and adapt it into RawSyst's idiom rather than
   importing its idiom.
6. Prefer the smallest effective combination. Most tasks need none of this.

A note specific to this product: RawSyst is an ERP and POS. Its screens are read
in columns of currency, under fluorescent light, by someone with a queue. Speed,
density, keyboard flow, correct Arabic RTL and honest states beat visual
novelty every time. Several tools in this box are built for marketing sites and
will happily suggest a hero animation for a stock-take screen. That judgement is
yours to make, not theirs.

### Things that must not happen

- No Tailwind, and no `shadcn add`, in `web/`, `pos/` or `shared/`. That rule
  was reopened for `web-next/` on 2026-09-04 and for nothing else — see the note
  at the top of §4.
- No Convex in the architecture. The `convex` skill is a reference, not an
  invitation.
- No React Native dependencies in `web/` or `pos/`. The skill exists for a
  future native surface.
- No animation library added to satisfy a skill. GSAP and Motion are knowledge,
  not dependencies.
- Nothing imported from UIverse, Skiper, Magic UI or 21st that drags Tailwind,
  a utility runtime or a new font in with it.

---

## 8. Verified, and how

| Claim | Evidence |
|---|---|
| 10 plugins enabled, 7 at project scope | `claude plugin list` |
| 15 agent skills present and loadable | Claude Code listed every one of them in-session immediately after install |
| 7 `/ds:*` slash commands registered | Listed in-session as `ds:interview`, `ds:extract`, `ds:usage-analysis`, `ds:prd`, `ds:generate`, `ds:assets`, `ds:port` |
| shadcn MCP responds and exposes 7 tools | JSON-RPC `initialize` + `tools/list` over stdio against `npx shadcn@latest mcp` |
| shadcn MCP can search a third-party registry | `search_items_in_registries` on `@magicui` returned 4 matches for "number ticker" |
| `.mcp.json` valid, both servers present, none lost | `node -e "require('./.mcp.json')"` → `[ 'shadcn', '21st' ]` |
| Serena's user-scope MCP untouched | `~/.claude.json` re-read; unchanged |
| shadcn CLI reaches @shadcn, @magicui, @skiper-ui | `search` and `view` run against all three |
| Declaring `@skiper-ui` breaks CLI search | Tested, observed, reverted |
| 21st CLI authenticated and answering | `21st whoami` → "Logged in as mahedi-emon"; `21st usage` → free tier, 2/2 retrievals left; `21st search "dashboard"` → 8 results |
| 21st **MCP** reachable, not authenticated | `POST https://21st.dev/api/mcp` with an `initialize` body → **HTTP 401**. The URL and transport are right; the key is absent. No tool call through it is claimed to work |
| Stitch MCP connected, not just reachable | `claude mcp list` → `stitch: ✔ Connected`. `initialize` → `StatelessServer`, protocol 2024-11-05; `tools/list` → **15 tools**. Tool *calls* will need `STITCH_API_KEY` |
| The Stitch DCR failure was caused by the empty header, not by the endpoint | Same config with a non-empty `STITCH_API_KEY` in the environment → `✔ Connected`; with `${STITCH_API_KEY:-}` and the variable unset → `Incompatible auth server: does not support dynamic client registration`. `https://accounts.google.com/.well-known/openid-configuration` has no `registration_endpoint` |
| Stitch's auth method is an API key, per its own docs | `google-labs-code/stitch-skills` README points at https://stitch.withgoogle.com/docs/mcp/setup/; `upload-to-stitch/SKILL.md` reads the key "from the `X-Goog-Api-Key` header" |
| No secret in any committed file | Both hosted servers read their key from the environment (`${STITCH_API_KEY}`, `${API_KEY_21ST:-}`). `~/.config/21st/auth.json` is user scope, outside the repo |
| 16 project skills, every frontmatter `name` matching its directory | Checked across `.claude/skills/*/SKILL.md` |
| The RawSyst design-system skill describes code that exists | `node .claude/skills/rawsyst-design-system/verify.mjs` → 0 undefined classes, 0 undefined custom properties, 0 missing paths, against 820 classes and 70 properties |
| No dependency added to the product | `git diff` on `package.json` is the `registries` key alone; `package-lock.json` unchanged |

One thing to know about that last row: `npx shadcn mcp init` added `shadcn` to
the root `devDependencies` and grew `package-lock.json` by 127 KB. That was
reverted — the MCP runs through `npx` and does not need the local package.

---

## 9. RawSyst's own design system, as a skill

The §23 pipeline exists to turn a design system into something an agent can read
without guessing. RawSyst has a real one — 2,290 lines of custom properties and
class primitives in `shared/src/design-system.css`, plus 1,243 more of screen
furniture in `dashboard.css` — and nothing in the toolbox knew about it. That was
the largest remaining gap: every other tool here teaches an agent about somebody
*else's* system.

`.claude/skills/rawsyst-design-system/` is the result. Eleven files:

```
SKILL.md                              pre-flight, the hard rules, a routing matrix
verify.mjs                            the drift check
references/tokens.md                  every custom property, both themes, what each is for
references/layout-and-type.md         type scale, breakpoints, tap targets, the shell
references/components.md              the class catalogue + the React helpers
references/i18n-rtl.md                Arabic, Bangla, bidi, money, dates
references/rules.md                   guardrails, anti-patterns, the checks
references/patterns/tables.md
references/patterns/forms.md
references/patterns/buttons-and-actions.md
references/patterns/panels-and-dialogs.md
references/patterns/states.md
```

### How it was produced

The `/ds:*` pipeline is written for a packaged React component library — an npm
package with per-component TypeScript interfaces and an export map to validate
imports against. RawSyst is not that: it is a CSS class system with a handful of
React helpers. So the pipeline's *discipline* was followed — scope decided
first, facts extracted mechanically from source, a closed structure, then
generation, then verification — while the artefacts were shaped to what RawSyst
actually is. Its literal Stage 2 (per-component `api.md` from a TS interface)
has no subject here.

Scoping decisions, made from repository evidence rather than by interview, since
the source could answer every question the interview asks:

| Decision | Answer | Why |
|---|---|---|
| Short name | `rawsyst-design-system` | |
| Output | `.claude/skills/`, not `skills/` | It has to be discoverable by Claude Code, which is the point |
| Scope | All four stylesheets, `shared/src/ui/`, `shared/src/i18n/`, `dashboard/DetailScreen.tsx` | These are what a UI change actually touches |
| Categories | Tokens · layout/type · catalogue · five patterns · i18n · rules | Follows how the CSS is already sectioned, not an imported taxonomy |
| Component granularity | One file per *pattern*, not per class | There is no component library to enumerate. 126 classes in the system file are primitives, not components |

Every fact was read out of the source. The two places a plausible-looking
example was written from memory rather than from the code — a `t()` call with an
invented key, and `useRemote` given a dependency array it does not take — were
caught by re-reading the source and corrected before commit.

### Keeping it true

A design-system skill that has drifted is worse than none: an agent will write
markup for a system that no longer exists, confidently. `verify.mjs` checks
every class name, custom property and repository path the skill states in
backticks against the actual stylesheets and the actual tree, and reads the
legitimate no-rule naming hooks out of `stylesheetCoverage.test.ts` rather than
keeping a second list that could disagree with the first.

```bash
node .claude/skills/rawsyst-design-system/verify.mjs
```

Run it after changing the design system, and after changing the skill.

### What it deliberately does not do

It documents the system as it is. It does not propose a new one, it does not
introduce Tailwind or shadcn components, and it does not describe any visual
language other than RawSyst's. Where the CSS explains *why* a rule exists — and
roughly half of `design-system.css` is that explanation — the skill carries the
reason, because the reason is what stops the next agent 'simplifying' a rule
that exists because a specific screen broke.
