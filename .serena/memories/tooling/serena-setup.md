# Serena setup state

## Configured 2026-08-15
`.serena/project.yml` now has `languages: [go, sql, typescript, markdown]` (was `[]`, because the project was activated when the repo held only the blueprint markdown and no code).

`gopls v0.23.0` installed to `%USERPROFILE%\go\bin`, and both that and `C:\Program Files\Go\bin` added to the persistent **user** PATH.

## ⚠️ Requires an MCP server restart
The Serena MCP server reads the language list once at process start. Until Claude Code is restarted, symbolic tools fail with:

```
Cannot extract symbols from file ... Active languages: []
```

**First action in a new session:** call `get_symbols_overview` on `backend/internal/platform/db/db.go`. If it returns symbols, Serena is live — use `find_symbol` / `replace_symbol_body` / `find_referencing_symbols` for all code work instead of Read/Edit. If it still errors, the restart has not taken effect.

## Why this matters
Two separate token savings, often confused:
1. **Memories** — already working. The 148 KB blueprint is distilled into `blueprint/*` memories, so no session re-reads it.
2. **Symbolic code tools** — pending restart. Reading one function body instead of a 400-line file.

## Related
[[architecture/decisions]] · [[architecture/phase-plan]]
