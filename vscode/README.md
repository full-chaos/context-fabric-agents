# VS Code

Config file for VS Code (GitHub Copilot Chat MCP support) to use the hosted
Dev Health MCP server (`https://mcp.fullchaos.dev/mcp`).

Rendered and checked by `internal/render` (`go run ./cmd/render -check`).
Doc sources:
[code.visualstudio.com/docs/agents/reference/mcp-configuration](https://code.visualstudio.com/docs/agents/reference/mcp-configuration)
and
[code.visualstudio.com/docs/copilot/chat/mcp-servers](https://code.visualstudio.com/docs/copilot/chat/mcp-servers)
(MCP config),
[code.visualstudio.com/docs/agent-customization/agent-skills](https://code.visualstudio.com/docs/agent-customization/agent-skills)
(Agent Skills).

## Install

Merge the file's content into `.vscode/mcp.json` (workspace) under the
`servers` key. Don't overwrite an existing file; merge in the `dev-health`
entry and, for the bearer variant, the `inputs` entry.

| Variant | File |
| --- | --- |
| Bearer (password-prompt input) | [`configs/mcp.bearer.json`](configs/mcp.bearer.json) |
| OAuth (VS Code-driven login) | [`configs/mcp.oauth.json`](configs/mcp.oauth.json) |

- **Bearer**: the file declares one `inputs` entry, `acr-mcp-token`, a
  `promptString` with `password: true`. VS Code prompts for the token the
  first time it connects and expands `${input:acr-mcp-token}` into the
  header itself; the token is never written into the config file.
- **OAuth**: no `inputs`, no `headers` key. VS Code discovers the
  authorization server from the hosted server's 401 challenge and runs its
  own login flow.

## Skill

The shared `dev-health` skill (tool order, evidence handling; see
[`skills/dev-health/SKILL.md`](skills/dev-health/SKILL.md)) is copied into
this bundle at [`skills/dev-health/SKILL.md`](skills/dev-health/SKILL.md).
Per
[code.visualstudio.com/docs/agent-customization/agent-skills](https://code.visualstudio.com/docs/agent-customization/agent-skills),
VS Code loads project skills from `.github/skills/<name>/SKILL.md` (also
`.claude/skills/` and `.agents/skills/`) and personal skills from
`~/.copilot/skills/<name>/SKILL.md` (also `~/.claude/skills/` and
`~/.agents/skills/`). Copy this bundle's `skills/dev-health/` directory to
`.github/skills/dev-health/` in your project (or the personal equivalent).

## Get a credential

See [../docs/get-a-credential.md](../docs/get-a-credential.md). Today: a
bearer token. VS Code prompts for it the first time it connects (the
`acr-mcp-token` input) — you don't export an environment variable
yourself for this client.

## Verify

<!-- docparity:vscode/configs/mcp.bearer.json -->
```json
{
  "inputs": [
    {
      "type": "promptString",
      "id": "acr-mcp-token",
      "description": "Dev Health MCP token (the value of ACR_MCP_TOKEN)",
      "password": true
    }
  ],
  "servers": {
    "dev-health": {
      "type": "http",
      "url": "https://mcp.fullchaos.dev/mcp",
      "headers": {
        "Authorization": "Bearer ${input:acr-mcp-token}"
      }
    }
  }
}
```

Run **MCP: List Servers** from the Command Palette, or check the
Extensions view's "MCP SERVERS - INSTALLED" section. This repo's CI does
not run a live VS Code client (no headless runner exists) — verify with
your own installed VS Code.

## Uninstall

Right-click the server in the Extensions view and choose uninstall, or
delete the `dev-health` entry (and its `inputs` entry, for the bearer
variant) from `.vscode/mcp.json` (workspace) or your user configuration
(**MCP: Open User Configuration**) — both are vendor-documented removal
paths. Also delete `.github/skills/dev-health/` (or the personal
equivalent) if you copied the skill.

## Troubleshooting

The server decides every request on its own bearer, before any MCP
method runs, and fails closed:

| HTTP | `error` | Meaning | Fix |
|---|---|---|---|
| 401 | `missing_bearer` | No `Authorization` header reached the server | Re-enter the token when VS Code prompts |
| 401 | `malformed_bearer` | The stored value is not a well-formed token | Clear the stored input and re-enter a real token |
| 401 | `invalid_credential` | The token does not decode, or is expired or revoked | Get a new token ([../docs/get-a-credential.md](../docs/get-a-credential.md)) |
| 403 | `insufficient_scope` | The token is valid but not granted for this call | Ask the operator who minted it to widen the grant |
| 429 | `rate_limited` | Per-organization budget exceeded | Wait for the `Retry-After` seconds, then retry |

A `502 upstream_incompatible` or `503 upstream_unavailable` means the
request was never decided — retry later. See `docs/mcp-sidecar.md`
§Remote in the ACR project for the full list.

## Install link

The cited docs describe installing via the Extensions view (`@mcp` filter),
the `MCP: Add Server`/`MCP: Install Server from Manifest` commands, or the
CLI `--add-mcp` flag, but neither page publishes a `vscode:mcp/install` URI
scheme or an "Add to VS Code" badge format. No link is included here: not
confirmed.

## Validation

- Every file here is strict JSON (no duplicate keys, no unknown fields, no
  trailing data) and matches the doc-derived shape checks in
  `internal/render/validate.go`. Both run in every PR (`go test ./...`).
- No VS Code client binary parses these files in CI (unlike Codex and
  Claude Code, which do; see `internal/render/README.md`). `legs.json`
  records this leg as `static-only`.
