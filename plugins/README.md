# plugins

Claude Code plugin and marketplace entry for the hosted Dev Health MCP server (CHAOS-6201).

- Marketplace: `.claude-plugin/marketplace.json` (name `dev-health`, one plugin, `source: ./plugins/dev-health`).
- Plugin: `plugins/dev-health/` = `.claude-plugin/plugin.json`, `.mcp.json` (rendered by `cmd/render`, bearer variant) and `skills/dev-health/SKILL.md` (rendered copy). No hooks, no executables, no LSP or monitors (tests enforce it).
- `plugins/configs/` holds the rendered Claude Code configs for manual use (`claude mcp add ...`).

## Install (v0.1.0, bearer variant)

```
export ACR_MCP_TOKEN=<your token>      # set BEFORE starting Claude Code
claude
/plugin marketplace add full-chaos/context-fabric-agents
/plugin install dev-health@dev-health
```

`claude mcp list` then shows `plugin:dev-health:dev-health` as `Connected`. Claude Code names a plugin's server `plugin:<plugin>:<server>`.

### If `ACR_MCP_TOKEN` is unset

The plugin sends `Authorization: Bearer ${ACR_MCP_TOKEN}`. With the variable unset the header is sent with an empty value, the server answers HTTP 401 `malformed_bearer`, and Claude Code does not fall back to OAuth when an `Authorization` header is configured. `claude mcp list` shows:

```
plugin:dev-health:dev-health: https://mcp.fullchaos.dev/mcp (HTTP) - ✘ Failed to connect — Server rejected the configured Authorization header (HTTP 401). ... {"error":"malformed_bearer",...}
```

Fix: export a valid token and restart Claude Code. The CI `claude-plugin` workflow proves both states on a clean HOME.

The OAuth variant (no header, browser login) follows in CHAOS-6208, after OAuth discovery is live on prod.

## Validation

`claude plugin validate plugins/dev-health --strict` and `claude plugin validate . --strict` run in `.github/workflows/claude-plugin.yml` with the Claude Code version pinned in `ci/claude-code/package.json` (lockfile, installed with `npm ci`). Schema source: https://code.claude.com/docs/en/plugin-marketplaces and https://code.claude.com/docs/en/plugins-reference.
