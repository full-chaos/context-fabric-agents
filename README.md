# context-fabric-agents

[![liveness](https://github.com/full-chaos/context-fabric-agents/actions/workflows/liveness.yml/badge.svg)](https://github.com/full-chaos/context-fabric-agents/actions/workflows/liveness.yml)
[![ci](https://github.com/full-chaos/context-fabric-agents/actions/workflows/ci.yml/badge.svg)](https://github.com/full-chaos/context-fabric-agents/actions/workflows/ci.yml)

Agent client plugins, skills, and configs for the **Dev Health hosted MCP
server**. Point your agent client at one URL and it can ask engineering
delivery questions and inspect cited evidence — no local server to run.

**Status: released.** Current version: `0.3.1`. See [Install](#install) below, or
[docs/verify-release.md](docs/verify-release.md) to check a release asset's signature first.

## Hosted endpoints

| Environment | MCP endpoint |
|---|---|
| Production | `https://mcp.fullchaos.dev/mcp` |
| Trial | `https://mcp.commanderkeen.dev/mcp` (Cloudflare Access-gated; not reachable from a plain client) |

## Supported clients

Default auth for every client is OAuth discovery (no token to copy; CHAOS-6184
is live on prod, default flipped in CHAOS-6208): a config names the server
URL only, and the client logs in itself on first connect. Bearer
(`ACR_MCP_TOKEN`) stays documented and rendered for headless/CI use — see
[docs/get-a-credential.md](docs/get-a-credential.md). Revisions and pins
are recorded in [`contracts/acr-mcp/compat.json`](contracts/acr-mcp/compat.json);
live/static status is recorded in [`liveness/legs.json`](liveness/legs.json)
and the workflow named below.

| Client | Artifact | Default auth | Headless/CI | Negotiated revision | Checked how |
|---|---|---|---|---|---|
| [Claude Code](plugins/README.md) | Plugin + marketplace (`plugins/`) | OAuth | bearer | `2026-07-28` (`server/discover`) | live-checked, every PR ([`claude-plugin.yml`](.github/workflows/claude-plugin.yml)) |
| [Codex](codex/README.md) | Plugin/skill bundle + `config.toml` (`codex/`) | OAuth | bearer | `2025-06-18` (legacy `initialize`) | live-checked, on `codex/**` changes ([`codex-live.yml`](.github/workflows/codex-live.yml)) |
| [OpenCode v1](opencode/README.md) | Config (`opencode/`) | OAuth | bearer | not yet recorded | static-only; schema-checked live every run against the vendor's own schema ([`ci.yml` job `opencode-schema`](.github/workflows/ci.yml)) |
| [OpenCode v2](opencode/README.md) | Config (`opencode/`) | OAuth | bearer | not yet recorded | static-only; no published schema to check against |
| [Cursor](cursor/README.md) | Config (`cursor/`) | OAuth | bearer | not yet recorded | static-only; no headless client to run in CI |
| [VS Code](vscode/README.md) | Config (`vscode/`) | OAuth | bearer | not yet recorded | static-only; no headless client to run in CI |

"static-only" is a declared, tested state (`liveness/legs.json`), not a
skipped check — see [`liveness/README.md`](liveness/README.md).

## Install

Each client's own README has the full install, get-a-credential, verify,
uninstall, and troubleshooting steps. Quickest path, Claude Code (default:
OAuth discovery, no token to export):

```bash
claude
/plugin marketplace add full-chaos/context-fabric-agents
/plugin install dev-health@dev-health
/mcp
```

Pick `dev-health` and approve at the web consent page. See
[plugins/README.md](plugins/README.md) for the rest, including headless/CI
bearer use, what `claude mcp list` shows, and how to uninstall.

## Usage

Once connected: what `context_for_task` needs, the investigate → clarify
→ result → evidence flow, and the guide resources and prompts every
client can read. See [docs/usage.md](docs/usage.md).

## Headless or remote host

Codex and Claude Code both need a browser on the same machine to sign in.
If you're running on a headless host or over SSH, use
[`login`](docs/login.md) instead: it walks an RFC 8628 device grant, so you
approve from a browser on any other machine.

## Self-hosted

Every config names one URL field. Point it at your own deployment
instead of `mcp.fullchaos.dev` — see [docs/self-hosted.md](docs/self-hosted.md).

## Migrating from acr

If you set up a remote client from the ACR project's own embedded
examples (`docs/examples/mcp-clients/*-remote-*`), see
[docs/migration.md](docs/migration.md) — two fields change, your token
does not.

## Verify a release

Signed tarballs, checksums, cosign signatures, and provenance attestation
per release (CHAOS-6200). See [docs/verify-release.md](docs/verify-release.md).

## Layout

| Path | Purpose |
|---|---|
| `plugins/` | Claude Code plugin |
| `codex/`, `opencode/`, `cursor/`, `vscode/` | Per-client configs |
| `skills/` | One shared skill text |
| `contracts/acr-mcp/` | Snapshot of the live server contract, `compat.json`, drift job ([details](contracts/acr-mcp/README.md)) |
| `liveness/` | Scheduled liveness probes |
| `cmd/`, `internal/` | Go renderer, probes, repository guards |
| `docs/` | Install and usage docs shared across clients ([index](docs/README.md)) |

## Contract pin

`contracts/acr-mcp/snapshot.json` pins the live server contract and a daily job opens a `contract-drift` PR when it changes. A contract widening is acknowledged by a merged snapshot PR before configs or skills use the new member. See [contracts/acr-mcp/README.md](contracts/acr-mcp/README.md).

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md). License: [Apache-2.0](LICENSE).
