# docs

Install and usage docs for the hosted Dev Health MCP server (CHAOS-6206).
Per-client install, credential, verify, uninstall, and troubleshooting
steps live in each client's own README (`plugins/`, `codex/`, `opencode/`,
`cursor/`, `vscode/`); this directory holds the parts that are the same
for every client.

| Doc | Holds |
|---|---|
| [get-a-credential.md](get-a-credential.md) | OAuth is the default (no credential to get); where the headless/CI bearer token comes from |
| [usage.md](usage.md) | `context_for_task` scope, the investigate → clarify → result → evidence flow, guide resources and prompts |
| [self-hosted.md](self-hosted.md) | The one URL field to edit to point a client at your own deployment |
| [migration.md](migration.md) | Moving from the ACR project's embedded `docs/examples/mcp-clients/*-remote-*` examples |
| [verify-release.md](verify-release.md) | `cosign verify-blob` / `gh attestation verify` for a downloaded release asset (CHAOS-6200) |

`verify-release.md` ships with the signed release pipeline (CHAOS-6200,
PR #3, merged). This directory does not duplicate its content.
