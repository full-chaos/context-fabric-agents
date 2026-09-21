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

## [Unreleased] - 0.1.0-dev

- Bootstrap: repository skeleton, license, security policy, baseline CI.
- Signed release pipeline: per-client tarballs, `SHA256SUMS`, cosign keyless signatures, build provenance, version gate.
