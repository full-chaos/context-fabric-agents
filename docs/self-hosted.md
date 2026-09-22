# Point at a self-hosted server

Every config in this repo names one URL field. Change that one field to
point a client at your own deployment instead of
`https://mcp.fullchaos.dev/mcp`. Nothing else in a config needs to change.

| Client | File | Field |
|---|---|---|
| Claude Code | `.mcp.json` (plugin) or your own config | `mcpServers.dev-health.url` |
| Codex | `config.toml` | `[mcp_servers.dev-health]` → `url` |
| OpenCode v1 | `opencode.json` | `mcp.dev-health.url` |
| OpenCode v2 | `opencode.json` | `mcp.servers.dev-health.url` |
| Cursor | `.cursor/mcp.json` | `mcpServers.dev-health.url` |
| VS Code | `.vscode/mcp.json` | `servers.dev-health.url` |

The server key stays `dev-health` in every example; rename it if you like,
it is not part of the protocol. `ACR_MCP_TOKEN` still names the
environment variable that holds your token for that deployment — it is a
client-side name, not tied to any one server.

If your deployment serves a different base path than `/mcp`, include it in
the URL (for example `https://acr-mcp.internal.example.com/mcp`). See the
ACR project's `deploy/README.md` for how to run the server itself; this
repo only ships client configs.
