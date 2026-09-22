# internal/render

Renderer for the hosted (remote, Streamable HTTP) client configs and the skill copies. Ported from the acr `internal/mcpclientfixtures/remote.go` renderer (stdlib only). This package owns the goldens.

```
go run ./cmd/render -write   # regenerate every artifact
go run ./cmd/render -check   # exit 1 on any drift, invalid shape, or ban violation
```

Constants: URL `https://mcp.fullchaos.dev/mcp`, server key `dev-health`, token env var `ACR_MCP_TOKEN`. STDIO is out of scope (it stays in acr).

## Artifacts

| Client | bearer | oauth |
|---|---|---|
| claude-code | `plugins/configs/claude-code.bearer.mcp.json`, `plugins/dev-health/.mcp.json` (same content), `.add.txt` | `plugins/configs/claude-code.oauth.mcp.json`, `.add.txt` |
| codex | `codex/configs/config.bearer.toml` | `codex/configs/config.oauth.toml` |
| opencode v1 | `opencode/configs/opencode.bearer.json` | `opencode/configs/opencode.oauth.json` |
| opencode v2 | `opencode/configs/opencode-v2.bearer.json` | `opencode/configs/opencode-v2.oauth.json` |
| cursor | `cursor/configs/mcp.bearer.json` | `cursor/configs/mcp.oauth.json` |
| vscode | `vscode/configs/mcp.bearer.json` | `vscode/configs/mcp.oauth.json` |

The skill source is `skills/dev-health/SKILL.md`. It is copied byte-for-byte to `<bundle>/skills/dev-health/SKILL.md` for `plugins/dev-health`, `codex`, `opencode`, `cursor` and `vscode`.

`<bundle>/configs/` and `<bundle>/skills/dev-health/` are managed directories: a file there that the renderer does not produce fails `-check`.

## Shapes and their sources (read 2026-09-21)

| Client | Shape | Doc |
|---|---|---|
| Claude Code | `mcpServers.<n>` `{type:"http", url, headers}`; `${VAR}` expansion; OAuth is used when no `Authorization` header is set | https://code.claude.com/docs/en/mcp |
| Codex | `[mcp_servers.<n>]` `url`, `bearer_token_env_var`, `enabled`; `auth` defaults to `oauth` (`codex mcp login`) | https://learn.chatgpt.com/docs/extend/mcp |
| OpenCode v1 | `mcp.<n>` `{type:"remote", url, enabled, headers, oauth}`; `{env:NAME}`; `oauth:false` disables the automatic OAuth attempt | https://opencode.ai/docs/mcp-servers |
| OpenCode v2 | `mcp.servers.<n>` `{type:"remote", url, oauth, protocol, headers}`; `protocol` is `legacy` (default), `auto` or `2026-07-28`; OAuth on by default | https://opencode.ai/v2/docs/mcp-servers |
| Cursor | `mcpServers.<n>` `{url, headers}`; `${env:NAME}`; no `auth` object means dynamic client registration | https://cursor.com/docs/context/mcp |
| VS Code | `servers.<n>` `{type:"http", url, headers}`; `inputs[]` `promptString` with `password:true`, used as `${input:id}` | https://code.visualstudio.com/docs/agents/reference/mcp-configuration |
| Skill | Agent Skills: frontmatter `name` (matches the directory, lowercase/digits/hyphens, max 64) and `description` (1-1024) | https://agentskills.io/specification |

## Validation status (what a real client parser checked, and what not)

`Validate` (used by `-write` and `-check`) is a strict syntax and shape check written here from the docs above. It is not a client parser. Separately, on 2026-09-21 these ran against the real client binaries:

| Client | Result |
|---|---|
| Codex 0.155.1 | `codex mcp list --json` / `mcp get` parsed both TOML variants (`streamable_http`, URL, `bearer_token_env_var` present in bearer only). A broken TOML control failed to load. |
| Claude Code 2.1.278 | `claude mcp add -s project` from each `.add.txt` wrote a `.mcp.json` semantically identical to the matching golden; `claude mcp get` read the bearer golden. |
| OpenCode v1, v2 | NOT validated by a client parser (binary not available). Shape from docs only. `"oauth": {}` on and `"protocol": "auto"` are doc-derived. v1 files (they carry `$schema`) ARE checked live, every CI run, against the vendor's own JSON Schema `https://opencode.ai/config.json` — see `cmd/opencodeschema`, job `opencode-schema` (CHAOS-6203). v2 declares no `$schema` and has no published schema to check. |
| Cursor | NOT validated by a client parser. Shape from docs only. Recorded `static-only` in `liveness/legs.json` (CHAOS-6203). |
| VS Code | NOT validated by a client parser. The oauth variant carries no `oauth` key; the docs describe OAuth as automatic, but the page read does not state the no-key behaviour outright. Recorded `static-only` in `liveness/legs.json` (CHAOS-6203). |
| `claude plugin validate --strict` | Claude Code 2.1.278: passes for `plugins/dev-health` and the root marketplace (CHAOS-6201); an unknown manifest field fails it. Runs in `.github/workflows/claude-plugin.yml`. |

## Bans (enforced by tests and by `-check`)

- No `Authorization` header, bearer wiring, `headers`, `inputs` or credential name in any oauth variant.
- No literal token: no `fcacr_` prefix, no 40+ character opaque run, and `Bearer` is only ever followed by the client's env expansion.
- No STDIO-only key (`command`, `args`, `ACR_API_URL`, `ACR_API_TOKEN`) in a hosted config.
- Bundle directories (`plugins`, `codex`, `opencode`, `cursor`, `vscode`) contain no `hooks/`, `bin/`, `.lsp.json` or `monitors/` at any depth. A plugin manifest may not declare `hooks`, `lspServers`, `monitors` or `bin`, and an `.mcp.json` server may not run a `command` (plan decision D14).
