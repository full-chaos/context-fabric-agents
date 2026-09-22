# Verify a release

Every release asset is checksummed, signed with cosign keyless (Sigstore), and has a
GitHub build-provenance attestation. Check the asset before you use it.

Release assets for tag `vX.Y.Z`:

| Asset | Meaning |
|---|---|
| `context-fabric-agents-<client>-X.Y.Z.tar.gz` | One tarball per client (`claude-code`, `codex`, `opencode`, `cursor`, `vscode`) |
| `SHA256SUMS` | SHA-256 of every tarball |
| `<asset>.sig`, `<asset>.pem` | cosign signature and signing certificate for each tarball and for `SHA256SUMS` |

Set these once:

```sh
TAG=v0.1.0
REPO=full-chaos/context-fabric-agents
gh release download "$TAG" --repo "$REPO" --dir release && cd release
```

## 1. Checksums

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

## 2. cosign signature

The certificate identity must be the release workflow of this repository, run for the tag.

```sh
cosign verify-blob \
  --certificate SHA256SUMS.pem \
  --signature SHA256SUMS.sig \
  --certificate-identity "https://github.com/${REPO}/.github/workflows/release.yml@refs/tags/${TAG}" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
```

Repeat for a tarball (replace `SHA256SUMS` in the three places, for example
`context-fabric-agents-codex-${TAG#v}.tar.gz`). Expected output: `Verified OK`.

## 3. GitHub build provenance

```sh
gh attestation verify "context-fabric-agents-codex-${TAG#v}.tar.gz" \
  --repo "$REPO" \
  --signer-workflow "${REPO}/.github/workflows/release.yml"
```

Expected: `Verification succeeded!`.

## Version rule

The tag equals the `version` of every plugin manifest in the tarballs (Claude Code
`plugin.json`, marketplace entries, Codex `plugin.json`). The release fails before
it publishes if any differ. See the release rule in [CHANGELOG.md](../CHANGELOG.md).

## Dry run

The `release` workflow has a manual `workflow_dispatch` run without inputs. It runs the
same verify, build, sign and attest steps and keeps the signed set as a workflow artifact
for 3 days. It publishes no release. The version it checks is the `VERSION` file.
