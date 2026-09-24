# OpenCode

Config files for OpenCode to use the hosted Dev Health MCP server
(`https://mcp.fullchaos.dev`). OpenCode has two config generations with
different shapes; pick the file that matches your OpenCode version.

Rendered and checked by `internal/render` (`go run ./cmd/render -check`).
Doc sources: [opencode.ai/docs/mcp-servers](https://opencode.ai/docs/mcp-servers)
(v1), [opencode.ai/v2/docs/mcp-servers](https://opencode.ai/v2/docs/mcp-servers)
(v2), [opencode.ai/docs/skills/](https://opencode.ai/docs/skills/) (Agent
Skills).

## Install

Merge the file's content into your OpenCode config (project: `opencode.json`
or `opencode.jsonc` at the repo root; global: `~/.config/opencode/opencode.json`).
Don't just copy the file over an existing config — merge the `mcp` key.

| OpenCode generation | OAuth (default, server-driven login) | Bearer (headless/CI, token env var) |
| --- | --- | --- |
| v1 | [`configs/opencode.oauth.json`](configs/opencode.oauth.json) | [`configs/opencode.bearer.json`](configs/opencode.bearer.json) |
| v2 | [`configs/opencode-v2.oauth.json`](configs/opencode-v2.oauth.json) | [`configs/opencode-v2.bearer.json`](configs/opencode-v2.bearer.json) |

- **OAuth (default)**: no token, no `headers` key; v2 additionally sets
  `"oauth": {}`. OpenCode discovers the authorization server from the hosted
  server's 401 challenge and runs its own login flow.
- **Bearer (headless/CI)**: export `ACR_MCP_TOKEN` in the environment
  OpenCode starts from. OpenCode expands `{env:ACR_MCP_TOKEN}` itself; the
  token is never written into the config file. The bearer variant sets
  `"oauth": false` so OpenCode does not also try to auto-detect OAuth.

v2 additionally sets `"protocol": "auto"` (negotiate the server's revision
rather than pin one).

## Skill

The shared `dev-health` skill (tool order, evidence handling; see
[`skills/dev-health/SKILL.md`](skills/dev-health/SKILL.md)) is copied into
this bundle at [`skills/dev-health/SKILL.md`](skills/dev-health/SKILL.md).
Per [opencode.ai/docs/skills/](https://opencode.ai/docs/skills/), OpenCode
loads project skills from `.opencode/skills/<name>/SKILL.md` (also
`.claude/skills/` and `.agents/skills/`) and global skills from
`~/.config/opencode/skills/<name>/SKILL.md`. Copy this bundle's
`skills/dev-health/` directory to `.opencode/skills/dev-health/` in your
project (or the global equivalent).

## Get a credential

See [../docs/get-a-credential.md](../docs/get-a-credential.md). Default:
no credential to get — OpenCode logs in itself on first connect (OAuth).
For headless/CI use, a bearer token in `ACR_MCP_TOKEN`, set before you
start OpenCode.

## Verify

```
opencode mcp list
```

Per [opencode.ai/docs/mcp-servers](https://opencode.ai/docs/mcp-servers)
it prints each server's connection state (for example `✓ dev-health
connected`). The in-app `/mcps` view lists, connects, and disconnects
servers too. This repo's CI does not run a live OpenCode client (no
binary in CI; see [Validation](#validation) below) — verify with your own
installed OpenCode.

<!-- docparity:opencode/configs/opencode.bearer.json -->
```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "dev-health": {
      "type": "remote",
      "url": "https://mcp.fullchaos.dev",
      "enabled": true,
      "oauth": false,
      "headers": {
        "Authorization": "Bearer {env:ACR_MCP_TOKEN}"
      }
    }
  }
}
```

## Uninstall

Remove the `dev-health` entry from `mcp` (v1) or `mcp.servers` (v2) in
your config file, and delete `.opencode/skills/dev-health/` (or the
global equivalent). For v2, the vendor docs state deleting the server
entry as the removal step; for v1, no dedicated remove command is
documented — setting `"enabled": false` disables it without deleting the
entry, or delete the entry yourself. `opencode mcp list` should no longer
show `dev-health` afterward.

## Troubleshooting

The server decides every request on its own bearer, before any MCP
method runs, and fails closed:

| HTTP | `error` | Meaning | Fix |
|---|---|---|---|
| 401 | `missing_bearer` | No `Authorization` header reached the server | Set `ACR_MCP_TOKEN` before starting OpenCode |
| 401 | `malformed_bearer` | The header is present but not a well-formed token | Re-export a real token |
| 401 | `invalid_credential` | The token does not decode, or is expired or revoked | Get a new token ([../docs/get-a-credential.md](../docs/get-a-credential.md)) |
| 403 | `insufficient_scope` | The token is valid but not granted for this call | Ask the operator who minted it to widen the grant |
| 429 | `rate_limited` | Per-organization budget exceeded | Wait for the `Retry-After` seconds, then retry |

A `502 upstream_incompatible` or `503 upstream_unavailable` means the
request was never decided — retry later. See `docs/mcp-sidecar.md`
§Remote in the ACR project for the full list.

## Install link

No confirmed one-click "Add to OpenCode" link format exists on the cited
doc pages: not confirmed.

## Validation

- Every file here is strict JSON (no duplicate keys, no unknown fields, no
  trailing data) and matches the doc-derived shape checks in
  `internal/render/validate.go`. Both run in every PR (`go test ./...`).
- The v1 files additionally validate live against the vendor's own JSON
  Schema, `https://opencode.ai/config.json`, fetched fresh on every CI run
  (job `opencode-schema` in `.github/workflows/ci.yml`; tool
  `cmd/opencodeschema`). If that schema is unreachable, the job posts a
  `::warning::` and does not fail the build — never a silent pass. v2 does
  not declare a `$schema` and has no published schema to check against.
- No OpenCode client binary parses these files in CI (unlike Codex and
  Claude Code, which do; see `internal/render/README.md`). `legs.json`
  records this leg as `static-only`.
