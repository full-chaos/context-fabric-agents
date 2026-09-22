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

## [Unreleased] - 0.1.0

- Bootstrap: repository skeleton, license, security policy, baseline CI.
- Signed release pipeline: per-client tarballs, `SHA256SUMS`, cosign keyless signatures, build provenance, version gate.
- Renderer, contract snapshot, Claude Code / Codex / OpenCode / Cursor / VS Code client bundles, liveness L1 probe.
