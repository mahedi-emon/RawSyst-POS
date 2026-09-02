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
| 16 | Google Stitch | ✅ installed | `stitch-design`, `stitch-build`, `stitch-utilities` from `google-labs-code/stitch-skills` | project | `claude plugin list` → 3 plugins enabled |
| 17 | Vercel Web Design Guidelines | ✅ installed | skill `web-design-guidelines` | project | Loaded and listed this session |
| 18 | Vercel Composition Patterns | ✅ installed | skill `vercel-composition-patterns` | project | Loaded and listed this session |
| 19 | Vercel Building Components | ↔ superseded | Folded into `vercel-composition-patterns`, whose own description is "building flexible component libraries, designing reusable APIs… compound components". There is no separate skill in the repo | project | Skill body read and confirmed |
| 20 | Vercel Next.js Best Practices | ↔ superseded | Folded into `vercel-react-best-practices` — "React **and Next.js** performance optimization guidelines from Vercel Engineering" | project | Skill description confirmed |
| 21 | Vercel Optimize | ✅ installed | skill `vercel-optimize` | project | Loaded and listed this session |
| 22 | Emil design engineering / visual polish | ✅ installed | skill `emil-design-eng` from `emilkowalski/skills`. The prompt did not name a repository; this is the maintained one (★34.6k, pushed 2026-08-21) | project | Installed, `skills-lock.json` pinned |
| 23 | Vercel Design Systems → Agent Skills | ✅ installed | Not a runtime skill — a 6-stage **generator pipeline**. Installed as slash commands: `/ds:interview`, `/ds:extract`, `/ds:usage-analysis`, `/ds:prd`, `/ds:generate`, `/ds:assets`, `/ds:port`, plus `scripts/verify-skills.sh` | project | Files present under `.claude/commands/ds/` |

### 3.2 shadcn/ui

| # | Requested | Status | Notes |
|---|---|---|---|
| 04 | shadcn/ui | ✅ available, **not initialized** | See §4. Reachable for reading; not a dependency of this product |
| — | Official shadcn MCP | ✅ installed | `.mcp.json` → `npx shadcn@latest mcp`, exactly the official configuration. 7 tools, verified live |
| — | shadcn Agent Skill | ✅ installed | skill `shadcn` from `shadcn/ui`, project scope |
| 09 | `Jpisnice/shadcn-ui-mcp-server` | ↔ superseded | See §4.3 |
| 10 | 21st "Magic MCP" | ↔ superseded | `@21st-dev/magic` on npm is stuck at 0.2.2 and is the old branding. The maintained implementation is `@21st-dev/cli` 1.17.0 plus the hosted HTTP MCP at `https://21st.dev/api/mcp`; both are configured. The obsolete package was deliberately not installed |

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

## 5. 21st.dev — working, with one loose end

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

`.mcp.json` carries the entry the CLI wrote:

```json
"21st": {
  "type": "http",
  "url": "https://21st.dev/api/mcp",
  "headers": { "x-api-key": "${API_KEY_21ST}" }
}
```

That reads `API_KEY_21ST` from the environment, and the browser login does not
set it — the CLI's stored session token and an MCP API key are different
credentials. `API_KEY_21ST` is **not** set, so the MCP server itself is
configured but unverified, and is not claimed to work.

If you want the MCP path as well as the CLI path, take a key from
https://21st.dev/mcp and set it in your environment before starting Claude Code
(`API_KEY_21ST`; the CLI also honours `TWENTYFIRST_TOKEN`). Set it in the
environment, not in `.mcp.json` — that file is committed.

### Google Stitch — MCP not configured

The three Stitch **skills** plugins are installed and need nothing. Stitch also
offers a *remote MCP server*, which is a separate thing: its URL is issued per
account through the setup flow at
https://stitch.withgoogle.com/docs/mcp/setup/. No URL was invented and no MCP
entry was written. If you complete that setup, the URL goes into `.mcp.json`
alongside the other two.

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
3. Read the design system — `shared/src/design-system.css` and
   `docs/ui-ux/00-design-system.md` — before inventing a token.
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

- No Tailwind, and no `shadcn add`, without a deliberate decision to reopen §4.
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
| 21st **MCP** not verified | `API_KEY_21ST` is unset, so the HTTP server was never exercised. Recorded as configured, not as working — §5 |
| No dependency added to the product | `git diff` on `package.json` is the `registries` key alone; `package-lock.json` unchanged |

One thing to know about that last row: `npx shadcn mcp init` added `shadcn` to
the root `devDependencies` and grew `package-lock.json` by 127 KB. That was
reverted — the MCP runs through `npx` and does not need the local package.
