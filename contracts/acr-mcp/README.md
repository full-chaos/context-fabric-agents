# contracts/acr-mcp

The pin for the hosted MCP server contract (`mcp_tools.v1`). The live server
is the public contract: this directory records what it served, not what its
source says.

| File | Holds |
|---|---|
| `snapshot.json` | `server_info`, the revision negotiated on each handshake path (`server/discover` at 2026-07-28, `initialize` at 2025-06-18), every tool with its full input schema and a canonical schema digest, resources, prompts and their arguments, and the capture time. Canonical JSON: sorted keys, stable diffs. |
| `compat.json` | Per client: pinned client version, expected negotiated revision, expected first method, and `status` (`confirmed` or `unconfirmed`). Claude Code, Codex and OpenCode v2 (`opencode-v2`) are confirmed; a client stays `unconfirmed` until a run proves it. The L2 liveness matrix (`liveness/README.md`) compares every live client's recorded first method + revision with this file; any change is red. |

## Capture and compare

`cmd/snapshot` uses the go-sdk v1.8.0 client. The host is a compiled
constant, the credential comes only from `ACR_MCP_CI_BEARER`, and nothing
prints headers or bodies. A missing credential exits non-zero with
`ACR_MCP_CI_BEARER missing`.

```sh
go run ./cmd/snapshot capture -out /tmp/new.json                          # needs ACR_MCP_CI_BEARER
go run ./cmd/snapshot diff -old contracts/acr-mcp/snapshot.json -new /tmp/new.json   # exit 10 on drift
```

## Daily drift job

`.github/workflows/contract-drift.yml` captures daily. On a difference it
force-updates the `contract-drift` branch and opens or updates one PR with the
diff. `captured_at` and `server_info.version` (build-specific) are recorded but never drift. Severity:

| Severity | Change |
|---|---|
| `major` | Tool, resource, prompt or prompt argument removed or renamed; input schema tightened (or changed in a way that is not provably a widening); new required prompt argument; negotiated revision changed. Update skills and configs, then cut a major release. |
| `minor` | Something added; a schema widened. |
| `patch` | Description text only. |

## Ask-dev pin rule

A contract widening seen in drift is acknowledged by a **merged snapshot PR**
before any config or skill in this repo uses the new member. Do not reference
a tool, resource, prompt or argument that `snapshot.json` on `main` does not
list.
