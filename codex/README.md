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

## Get a credential

See [docs/get-a-credential.md](../docs/get-a-credential.md). Default: no
credential to get — the plugin's `config.toml` table uses OAuth
(`codex mcp login dev-health`; CHAOS-6184 is live on prod). For headless
or CI use, export a bearer token in `ACR_MCP_TOKEN` before starting Codex
and use `configs/config.bearer.toml` instead.

**Headless/remote host (no browser on the machine running Codex):**
`codex mcp login` needs a browser on the same host. Run
[`login`](../docs/login.md) instead — `go run ./cmd/login --client codex`
or a downloaded release binary — and approve it from a browser on any
other machine. It writes `ACR_MCP_TOKEN` and wires `config.toml` for you.

## Verify

```
codex mcp list
codex mcp get dev-health
```

<!-- docparity:codex/configs/config.bearer.toml -->
```
# Dev Health hosted MCP server entry for Codex CLI (bearer variant).
# Shape source: https://learn.chatgpt.com/docs/extend/mcp
# Codex sends the value of the named environment variable as
# "Authorization: Bearer <value>" on every request. Export the variable in the
# shell that starts Codex; never write the token into this file. Place this
# table in ~/.codex/config.toml (user scope) or .codex/config.toml (project
# scope, requires trusting the project on first use).

[mcp_servers.dev-health]
url = "https://mcp.fullchaos.dev/mcp"
bearer_token_env_var = "ACR_MCP_TOKEN"
enabled = true
```

`codex mcp list`/`get` read the config file only — they confirm the entry
is registered, not that it connects (Codex has no user-facing command
that proves a live connection; only the `codex app-server` JSON-RPC API
does, which this repo's own CI uses in `codex-live.yml`). To prove it
connects, ask Codex a question that needs the `dev-health` tools.

## Uninstall

Plugin (removes the plugin, its skill, and its MCP entry together —
`codex mcp list` no longer shows `dev-health` afterward):

```
codex plugin remove dev-health@dev-health
```

This exact form (`<plugin>@<marketplace>`) is not stated on the vendor
doc pages this repo cites; it comes from `codex plugin remove --help` and
was executed to confirm it removes the MCP entry, not just the plugin
cache. Removing only the marketplace
(`codex plugin marketplace remove dev-health`) does **not** remove the
plugin's MCP entry — `codex mcp list` keeps showing `dev-health` until
you also run `codex plugin remove`, or the plugin.

Config only: delete the `[mcp_servers.dev-health]` table from
`config.toml` and remove `~/.agents/skills/dev-health/` (or
`.agents/skills/dev-health/` in a repo). `codex mcp list` should no
longer show `dev-health` afterward.

## Troubleshooting

The server decides every request on its own bearer, before any MCP
method runs, and fails closed:

| HTTP | `error` | Meaning | Fix |
|---|---|---|---|
| 401 | `missing_bearer` | No `Authorization` header reached the server | Set `ACR_MCP_TOKEN` before starting Codex |
| 401 | `malformed_bearer` | The header is present but not a well-formed token | Re-export a real token |
| 401 | `invalid_credential` | The token does not decode, or is expired or revoked | Get a new token ([docs/get-a-credential.md](../docs/get-a-credential.md)) |
| 403 | `insufficient_scope` | The token is valid but not granted for this call | Ask the operator who minted it to widen the grant |
| 429 | `rate_limited` | Per-organization budget exceeded | Wait for the `Retry-After` seconds, then retry |

A `502 upstream_incompatible` or `503 upstream_unavailable` means the
request was never decided — retry later. See `docs/mcp-sidecar.md`
§Remote in the ACR project for the full list.

## Facts (verified on Codex 0.155.1)

- Plugin layout confirmed two ways: vendor docs (<https://developers.openai.com/codex/plugins/build> lists `.codex-plugin/plugin.json` as the supported compatibility manifest, and `.agents/plugins/marketplace.json` with `source.path` starting `./`) and an executed run: `codex plugin marketplace add`, `codex plugin add`, then the skill appears as `dev-health:dev-health` in `codex debug prompt-input` and the MCP entry in `codex mcp list`. Newer docs prefer a root `plugin.json` with `$schema`; the `.codex-plugin/` form stays supported.
- Codex 0.155.1 speaks `initialize` at protocol revision 2025-06-18, not 2026-07-28. It works against the hosted server. Never ask for a server change for it (CHAOS-6166).
- OAuth: `auth` defaults to `oauth`; `codex mcp login dev-health` starts sign-in. CHAOS-6184 is live on prod; the plugin's `config.toml` table ships this as the default (CHAOS-6208).
- Codex does not reject unknown keys in `[mcp_servers.*]` (`--strict-config` is refused by `codex mcp`); it does reject wrong types. The repo's own validator (`internal/render`) rejects unknown keys.
- No `codex mcp` subcommand connects: `list` and `get` only read config. Connect-only proof: `codex app-server` JSON-RPC `mcpServerStatus/list` (used by `proof/mcp_status.py`). It needs no LLM key and no OpenAI login.

## Proofs

- `proof/static_check.sh`: run in `ci.yml` job `codex-config`. Clean `CODEX_HOME`, pinned Codex, no credential.
- `proof/mcp_status.py`: run in `codex-live.yml` with the repo secret `ACR_MCP_CI_BEARER`. Never prints the token.
