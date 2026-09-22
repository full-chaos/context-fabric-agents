# codex

Codex bundle for the Dev Health hosted MCP server (`https://mcp.fullchaos.dev/mcp`). CHAOS-6202.

| Path | What | Owner |
| --- | --- | --- |
| `.codex-plugin/plugin.json` | Plugin manifest (`skills`, `mcpServers`) | hand-written, tested by `bundle_test.go` |
| `.mcp.json` | Plugin MCP entry: URL only, OAuth default | hand-written |
| `skills/dev-health/SKILL.md` | Agent Skills file | copied by `go run ./cmd/render -write` from `skills/dev-health/SKILL.md` |
| `configs/config.bearer.toml`, `configs/config.oauth.toml` | `config.toml` tables | rendered by `cmd/render` |
| `proof/` | CI proofs run against a pinned Codex CLI | hand-written |

The marketplace file is `../.agents/plugins/marketplace.json` (repo root, so `owner/repo` works). It lists one plugin, `dev-health`, with local source `./codex`.

## Install

Plugin (skill + MCP entry):

```
codex plugin marketplace add full-chaos/context-fabric-agents
codex plugin add dev-health@dev-health
codex mcp login dev-health
```

Config only, no plugin: copy a table from `configs/` into `~/.codex/config.toml`, and copy `skills/dev-health/` into `~/.agents/skills/dev-health/` (or `.agents/skills/dev-health/` in a repo).

Bearer variant (headless, CI): use `configs/config.bearer.toml` and export `ACR_MCP_TOKEN` in the shell that starts Codex. The plugin entry has no credential. Not tested: plugin and `config.toml` both defining `dev-health`. Use one or the other.

## Facts (verified on Codex 0.155.1)

- Plugin layout confirmed two ways: vendor docs (<https://developers.openai.com/codex/plugins/build> lists `.codex-plugin/plugin.json` as the supported compatibility manifest, and `.agents/plugins/marketplace.json` with `source.path` starting `./`) and an executed run: `codex plugin marketplace add`, `codex plugin add`, then the skill appears as `dev-health:dev-health` in `codex debug prompt-input` and the MCP entry in `codex mcp list`. Newer docs prefer a root `plugin.json` with `$schema`; the `.codex-plugin/` form stays supported.
- Codex 0.155.1 speaks `initialize` at protocol revision 2025-06-18, not 2026-07-28. It works against the hosted server. Never ask for a server change for it (CHAOS-6166).
- OAuth: `auth` defaults to `oauth`; `codex mcp login dev-health` starts sign-in. OAuth on prod needs CHAOS-6184 live there.
- Codex does not reject unknown keys in `[mcp_servers.*]` (`--strict-config` is refused by `codex mcp`); it does reject wrong types. The repo's own validator (`internal/render`) rejects unknown keys.
- No `codex mcp` subcommand connects: `list` and `get` only read config. Connect-only proof: `codex app-server` JSON-RPC `mcpServerStatus/list` (used by `proof/mcp_status.py`). It needs no LLM key and no OpenAI login.

## Proofs

- `proof/static_check.sh`: run in `ci.yml` job `codex-config`. Clean `CODEX_HOME`, pinned Codex, no credential.
- `proof/mcp_status.py`: run in `codex-live.yml` with the repo secret `ACR_MCP_CI_BEARER`. Never prints the token.
