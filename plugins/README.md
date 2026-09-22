# plugins

Claude Code plugin and marketplace entry for the hosted Dev Health MCP server (CHAOS-6201, default auth flipped to OAuth in CHAOS-6208).

- Marketplace: `.claude-plugin/marketplace.json` (name `dev-health`, one plugin, `source: ./plugins/dev-health`).
- Plugin: `plugins/dev-health/` = `.claude-plugin/plugin.json`, `.mcp.json` (rendered by `cmd/render`, OAuth variant: URL only) and `skills/dev-health/SKILL.md` (rendered copy). No hooks, no executables, no LSP or monitors (tests enforce it).
- `plugins/configs/` holds the rendered Claude Code configs for manual use (`claude mcp add ...`), both variants.

## Install (default: OAuth discovery)

```
claude
/plugin marketplace add full-chaos/context-fabric-agents
/plugin install dev-health@dev-health
```

No token to export. The plugin's `.mcp.json` carries the URL only; Claude Code discovers the authorization server from the hosted server's 401 challenge. `claude mcp list` shows `plugin:dev-health:dev-health` as `! Needs authentication` until you sign in:

```
/mcp
```

pick `dev-health`, and approve the request at the web consent page. After that, `claude mcp list` shows `Connected` with no bearer token anywhere. Claude Code names a plugin's server `plugin:<plugin>:<server>`.

## Headless / CI: bearer variant

OAuth needs a browser. For headless or CI use, add the bearer config directly instead of relying on the plugin's OAuth default:

```
export ACR_MCP_TOKEN=<your token>      # set BEFORE starting Claude Code
claude mcp add --transport http dev-health-bearer https://mcp.fullchaos.dev/mcp \
  --header 'Authorization: Bearer ${ACR_MCP_TOKEN}'
```

(or copy [`configs/claude-code.bearer.mcp.json`](configs/claude-code.bearer.mcp.json) / [`configs/claude-code.bearer.add.txt`](configs/claude-code.bearer.add.txt)). With the variable unset the header carries no valid credential (the exact bytes Claude Code sends are not verified), the server answers HTTP 401 `malformed_bearer`, and Claude Code does not fall back to OAuth when an `Authorization` header is configured:

```
dev-health-bearer: https://mcp.fullchaos.dev/mcp (HTTP) - ✘ Failed to connect — Server rejected the configured Authorization header (HTTP 401). ... {"error":"malformed_bearer",...}
```

Fix: export a valid token and restart Claude Code. The CI `claude-plugin` workflow proves both states on a clean HOME using this bearer path explicitly (CI has no browser, so it cannot complete the plugin's OAuth default).

## Get a credential

See [docs/get-a-credential.md](../docs/get-a-credential.md). Default: no
credential to get — Claude Code logs in itself on first connect (`/mcp`,
OAuth). For headless/CI use (see above), a bearer token in
`ACR_MCP_TOKEN`, from `acr-mcp login` (if you already run the STDIO CLI)
or an operator-minted credential. Set it before you start Claude Code —
Claude Code reads environment variables once, at launch.

## Verify

```
claude mcp list
```

Look for `plugin:dev-health:dev-health` with `✔ Connected`. For detail on
one server:

```
claude mcp get dev-health
```

If you added the config by hand instead of the plugin, `claude mcp add`
renders exactly the same config; either path connects the same way.
`plugins/configs/claude-code.bearer.add.txt` holds the manual command:

<!-- docparity:plugins/configs/claude-code.bearer.add.txt -->
```
claude mcp add --transport http dev-health https://mcp.fullchaos.dev/mcp --header 'Authorization: Bearer ${ACR_MCP_TOKEN}'
```

## Uninstall

Plugin (removes the skill and the MCP entry together):

```
/plugin uninstall dev-health@dev-health
```

Or, without opening the interactive panel:

```
claude plugin uninstall dev-health@dev-health
```

Removing the whole marketplace (`/plugin marketplace remove dev-health`)
also uninstalls every plugin it provided — here, just this one.

If you added the server by hand (no plugin), remove the entry instead:

```
claude mcp remove dev-health
```

`claude mcp list` should no longer show `dev-health` after either path.

## Troubleshooting

The server decides every request on its own bearer, before any MCP method
runs, and fails closed:

| HTTP | `error` | Meaning | Fix |
|---|---|---|---|
| 401 | `missing_bearer` | No `Authorization` header reached the server | Set `ACR_MCP_TOKEN` before starting Claude Code |
| 401 | `malformed_bearer` | The header is present but not a well-formed token (see [above](#if-acr_mcp_token-is-unset)) | Re-export a real token; check for stray quotes or whitespace |
| 401 | `invalid_credential` | The token does not decode, or is expired or revoked | Get a new token ([docs/get-a-credential.md](../docs/get-a-credential.md)) |
| 403 | `insufficient_scope` | The token is valid but not granted for this call | Ask the operator who minted it to widen the grant |
| 429 | `rate_limited` | Per-organization budget exceeded | Wait for the `Retry-After` seconds, then retry |

A `502 upstream_incompatible` or `503 upstream_unavailable` means the
request was never decided — retry later; it is not a credential problem.
See `docs/mcp-sidecar.md` §Remote in the ACR project for the full list.

## Validation

`claude plugin validate plugins/dev-health --strict` and `claude plugin validate . --strict` run in `.github/workflows/claude-plugin.yml` with the Claude Code version pinned in `ci/claude-code/package.json` (lockfile, installed with `npm ci`). Schema source: https://code.claude.com/docs/en/plugin-marketplaces and https://code.claude.com/docs/en/plugins-reference.
