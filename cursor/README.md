# Cursor

Config file for Cursor to use the hosted Dev Health MCP server
(`https://mcp.fullchaos.dev/mcp`).

Rendered and checked by `internal/render` (`go run ./cmd/render -check`).
Doc sources: [cursor.com/docs/context/mcp](https://cursor.com/docs/context/mcp)
(MCP config), [cursor.com/docs/context/skills](https://cursor.com/docs/context/skills)
(Agent Skills).

## Install

Merge the file's content into `.cursor/mcp.json` (project) or
`~/.cursor/mcp.json` (global) under the `mcpServers` key. Don't overwrite an
existing file; merge in the `dev-health` entry.

| Variant | File |
| --- | --- |
| Bearer (token env var) | [`configs/mcp.bearer.json`](configs/mcp.bearer.json) |
| OAuth (Cursor-driven login) | [`configs/mcp.oauth.json`](configs/mcp.oauth.json) |

- **Bearer**: export `ACR_MCP_TOKEN` in the environment Cursor starts from.
  Cursor expands `${env:ACR_MCP_TOKEN}` itself; the token is never written
  into the config file.
- **OAuth**: no `headers` key at all. Per the cited doc, a server entry with
  no `headers`/auth object means Cursor runs dynamic client registration
  and its own OAuth login on first connect.

## Skill

The shared `dev-health` skill (tool order, evidence handling; see
[`skills/dev-health/SKILL.md`](skills/dev-health/SKILL.md)) is copied into
this bundle at [`skills/dev-health/SKILL.md`](skills/dev-health/SKILL.md).
Per [cursor.com/docs/context/skills](https://cursor.com/docs/context/skills),
Cursor loads project skills from `.cursor/skills/<name>/SKILL.md` (also
`.agents/skills/`, and for compatibility `.claude/skills/` and
`.codex/skills/`) and global skills from `~/.cursor/skills/<name>/SKILL.md`
(also `~/.agents/skills/`). Copy this bundle's `skills/dev-health/`
directory to `.cursor/skills/dev-health/` in your project (or the global
equivalent).

## Get a credential

See [../docs/get-a-credential.md](../docs/get-a-credential.md). Today: a
bearer token in `ACR_MCP_TOKEN`, set before you start Cursor.

## Verify

<!-- docparity:cursor/configs/mcp.bearer.json -->
```json
{
  "mcpServers": {
    "dev-health": {
      "url": "https://mcp.fullchaos.dev/mcp",
      "headers": {
        "Authorization": "Bearer ${env:ACR_MCP_TOKEN}"
      }
    }
  }
}
```

Cursor has no CLI. Check the connection state in **Customize** → MCP, or
in the Output panel's "MCP Logs". This repo's CI does not run a live
Cursor client (no headless runner exists) — verify with your own
installed Cursor.

## Uninstall

Toggle the server off in **Customize**, or delete the `dev-health` entry
from `.cursor/mcp.json` (project) or `~/.cursor/mcp.json` (global) by
hand — both are vendor-documented removal paths. Also delete
`.cursor/skills/dev-health/` (or the global equivalent) if you copied the
skill.

## Troubleshooting

The server decides every request on its own bearer, before any MCP
method runs, and fails closed:

| HTTP | `error` | Meaning | Fix |
|---|---|---|---|
| 401 | `missing_bearer` | No `Authorization` header reached the server | Set `ACR_MCP_TOKEN` before starting Cursor |
| 401 | `malformed_bearer` | The header is present but not a well-formed token | Re-export a real token |
| 401 | `invalid_credential` | The token does not decode, or is expired or revoked | Get a new token ([../docs/get-a-credential.md](../docs/get-a-credential.md)) |
| 403 | `insufficient_scope` | The token is valid but not granted for this call | Ask the operator who minted it to widen the grant |
| 429 | `rate_limited` | Per-organization budget exceeded | Wait for the `Retry-After` seconds, then retry |

A `502 upstream_incompatible` or `503 upstream_unavailable` means the
request was never decided — retry later. See `docs/mcp-sidecar.md`
§Remote in the ACR project for the full list.

## Install link

The cited doc describes an "Add to Cursor" button on Cursor's own MCP
marketplace entries, but does not publish the deep-link URL format (no
`cursor://...` scheme is documented on that page). No link is included
here: not confirmed.

## Validation

- Every file here is strict JSON (no duplicate keys, no unknown fields, no
  trailing data) and matches the doc-derived shape checks in
  `internal/render/validate.go`. Both run in every PR (`go test ./...`).
- No Cursor client binary parses these files in CI (unlike Codex and Claude
  Code, which do; see `internal/render/README.md`). `legs.json` records
  this leg as `static-only`.
