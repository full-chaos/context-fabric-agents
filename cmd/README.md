# cmd

Go entrypoints.

| Command | Purpose |
|---|---|
| `render` | `-write` regenerates the client configs and skill copies; `-check` fails on drift, invalid shape, or a ban violation. See [internal/render](../internal/render/README.md). |
| `snapshot` | `capture -out FILE` records the live hosted MCP contract (credential from `ACR_MCP_CI_BEARER`); `diff -old A -new B` compares two captures (exit 10 on drift). See [contracts/acr-mcp](../contracts/acr-mcp/README.md). |

Liveness probes are added under CHAOS-6204.
