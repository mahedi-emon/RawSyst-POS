# Serena setup state

**Status as of 2026-08-15: configured and verified working.** `serena project health-check` passes; gopls reports `view_type="GoWork"` with 13 packages.

## Two bugs were introduced and fixed on the same day

### 1. `sql` is not a Serena language — it crashed startup
`.serena/project.yml` was first set to `languages: [go, sql, typescript, markdown]`. **There is no SQL language server in Serena's list**, and the invalid entry made the MCP server fail to start, which presented to the user as "serena failed to run".

Valid values are enumerated in the comment block above the `languages:` key in `project.yml`. Anything not on that list breaks startup. Current setting:

```yaml
languages: [go, typescript, markdown]
```

Migrations are read as plain text. That is fine — the symbolic tools are for Go and TypeScript.

### 2. gopls could not see the module — silent, and worse
`go.mod` lives in `backend/`, not at the repository root, because the repo also holds the Next.js app, the Tauri POS and the docs. Without a workspace file gopls opened the root as an **AdHoc** view with 2 packages and could not resolve anything inside `backend/`.

This one is dangerous because **the language server appears to start correctly**. Serena would have reported healthy while symbol lookup silently returned nothing.

Fixed by `go.work` at the repository root:

```
go 1.26
use ./backend
```

Note that `go build ./...` from the root now fails — the root itself is not a module. Build from `backend/` (which is what the Makefile and CI do) or use `go build ./backend/...`.

## Verifying it actually works

Do not trust "the server started". Check the view type:

```powershell
serena project health-check
# then grep the newest log under .serena/logs/health-checks/
#   want: view_type="GoWork"   and a package count in double digits
#   bad:  view_type="AdHoc"    and packages=2
```

From inside a session, the direct test is:

```
get_symbols_overview  backend/internal/platform/db/db.go
```

Symbols returned means Serena is live — then use `find_symbol`, `replace_symbol_body`, `find_referencing_symbols` for code work instead of Read/Edit. An error means the MCP server has not picked up the config.

## MCP server restart is required after any config change
Serena is launched as `serena start-mcp-server --context claude-code --project-from-cwd`, and reads the project config **once at process start**. Editing `project.yml` mid-session has no effect until Claude Code restarts.

## Two separate token savings, often confused
1. **Memories** — work regardless of language servers. The 148 KB blueprint is distilled into `blueprint/*`, so no session re-reads it. This has been working since the beginning.
2. **Symbolic code tools** — need the above to be right. Reading one function body instead of a 400-line file.

## Cursor launches it differently from Claude Code

Both clients are supported; only the launch line differs.

| Client | Launch |
|---|---|
| Claude Code | `serena start-mcp-server --context claude-code --project-from-cwd` |
| Cursor | `~/.cursor/mcp.json`, `--context ide-assistant --project "D:\RawSyst POS"` |

Three things learned wiring up Cursor on 2026-08-19:

1. **Cursor reads the GLOBAL `~/.cursor/mcp.json`, not the project's `.cursor/mcp.json`.** A project-level file was written first and silently ignored — other MCP servers reloaded while Serena never appeared. Keep exactly one entry, in the global file, or a future Cursor version that does read project config will register the server twice.
2. **The server must be enabled by hand** in Settings > MCP & Integrations. An agent cannot toggle it on, so "config written" is not "server available".
3. **`--context ide-assistant` exposes 19 tools, not 26.** `read_file`, `list_dir`, `find_file`, `search_for_pattern` and `execute_shell_command` are dropped because the IDE supplies them. The symbolic tools and the memories — the two things that actually save tokens — are all present.

### The health check exits 1 even when it passes
`serena project health-check` ends with `UnicodeEncodeError: 'charmap' codec can't encode character '\u2705'`. That is the CLI failing to print a tick to a cp1252 console **after** the check already succeeded. Read the log, not the exit code:

```powershell
# want: view_type="GoWork"  and  'Health check completed successfully'
# bad:  view_type="AdHoc"
Select-String -Path (Get-ChildItem .serena\logs\health-checks\*.log |
  Sort-Object LastWriteTime -Descending | Select-Object -First 1).FullName `
  -Pattern 'view_type|Health check completed'
```

`PYTHONUTF8=1` and `PYTHONIOENCODING=utf-8` are set in the mcp.json `env` block so the same encoding fault cannot take down the server's logging mid-session.

## Related
[[design/index]] · [[code/backend-state]] · [[architecture/decisions]]
