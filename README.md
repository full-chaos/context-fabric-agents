# context-fabric-agents

Agent client plugins, skills and configs for the Context Fabric hosted MCP server.

**Status: not yet released.** Current version: `0.1.0-dev`. Nothing here is installable yet.

## Hosted endpoints

| Environment | MCP endpoint |
|---|---|
| Production | `https://mcp.fullchaos.dev/mcp` |
| Trial | `https://mcp.commanderkeen.dev/mcp` |

## Supported clients

Protocol facts below were observed live on 2026-09-21.

| Client | Version observed | Negotiated MCP revision | Config in this repo |
|---|---|---|---|
| Claude Code | 2.1.278 | 2026-07-28 (`server/discover`) | `plugins/` (planned) |
| Codex | 0.155.1 | 2025-06-18 (legacy `initialize`) | `codex/` (planned) |
| OpenCode | v1 and v2 | not yet recorded | `opencode/` (planned) |
| Cursor | - | not yet recorded | `cursor/` (planned) |
| VS Code | - | not yet recorded | `vscode/` (planned) |

## Authentication

v0.1.0: bearer token from the environment variable `ACR_MCP_TOKEN`. Never put a token in a config file.
OAuth login follows under CHAOS-6184.

## Layout

| Path | Purpose |
|---|---|
| `plugins/` | Claude Code plugin |
| `codex/`, `opencode/`, `cursor/`, `vscode/` | Per-client configs |
| `skills/` | One shared skill text |
| `contracts/acr-mcp/` | Snapshot of the live server contract |
| `liveness/` | Scheduled liveness probes |
| `cmd/`, `internal/` | Go renderer, probes, repository guards |
| `docs/` | Install and usage docs |

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md). License: [Apache-2.0](LICENSE).
