# Changelog

All notable changes to this repository are recorded here. Versioning: semver `vX.Y.Z`.
The plugin `version` in every client bundle equals the release tag; the release
workflow fails when they differ.

## Release rule

Choose the bump from the change to the hosted MCP surface that client configs and skills depend on:

| Change | Bump |
|---|---|
| Tool added | minor |
| Tool removed or renamed | major |
| Input schema tightened (a call that worked before can now fail) | major |
| Client config shape changed | major |
| Default auth changed (for example bearer to OAuth discovery) | major |
| Skill text, docs, other compatible fix | patch |

Pre-releases use a suffix (`v0.0.0-rc.1`) and publish as GitHub pre-releases.
Verify a release with [docs/verify-release.md](docs/verify-release.md).

The `VERSION` file names the version this repo will ship next. Every plugin manifest
(Claude Code `plugin.json`, Codex `plugin.json`) must equal it — the release workflow's
version gate checks the release tag against these manifests directly; the `workflow_dispatch`
dry run checks `VERSION` instead, so keep `VERSION` in step with the manifests or the dry
run goes red for no code reason.

**Pre-1.0 convention:** this repo has not cut `v1.0.0` yet, so a change the table above
calls "major" bumps the minor digit instead (`0.x.0 -> 0.(x+1).0`), per semver's own
"anything may change" rule for `0.y.z`. The bump becomes a real major (`1.0.0+`) once the
repo has shipped a `v1.0.0` release.

## [Unreleased] - 0.3.0

- `cmd/login`: a headless/remote device-grant login helper (RFC 8628), released as a
  compiled binary per os/arch (`linux/amd64`, `linux/arm64`, `darwin/amd64`,
  `darwin/arm64`) alongside the client-bundle tarballs. Discovers the authorization
  server from the target MCP endpoint's own 401 challenge, registers a public client,
  starts a device authorization, polls for approval, and writes the bearer where Codex
  or Claude Code reads it (`--client codex|claude-code`), or to a file only
  (`--client env`) or stdout (`--client stdout`, for scripting). See
  [docs/login.md](docs/login.md). New user-facing artifact: `0.2.0 -> 0.3.0` per the
  release rule above (tool/capability added).

## [Unreleased] - 0.2.0

- Liveness L3: OAuth discovery chain probe (unauthenticated 401 -> resource_metadata ->
  protected-resource metadata -> authorization-server metadata), `results/l3.json` into the
  fail-loud aggregator. `l3-register` (daily dynamic-client-registration proof) is
  `declared-off` until CHAOS-6191 (idle client purge) lands.
- **Default auth flipped to OAuth discovery** for every client that supports it (Claude
  Code plugin `.mcp.json`, Codex, OpenCode v2, Cursor, VS Code), now that CHAOS-6184 is
  live on prod. Bearer variants stay documented and rendered for headless/CI use. Per the
  release rule above and the pre-1.0 convention, this is `0.1.0 -> 0.2.0`.

## [Unreleased] - 0.1.0

- Bootstrap: repository skeleton, license, security policy, baseline CI.
- Signed release pipeline: per-client tarballs, `SHA256SUMS`, cosign keyless signatures, build provenance, version gate.
- Renderer, contract snapshot, Claude Code / Codex / OpenCode / Cursor / VS Code client bundles, liveness L1 probe.
