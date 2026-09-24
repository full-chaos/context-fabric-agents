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
TAG=v0.2.0
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

## The mirrored `acr-mcp` assets

Releases here also mirror the `acr-mcp` STDIO binary from the ACR release. These files
are ACR's own, published unmodified, and are signed by ACR's release workflow, not this
one:

| Asset | Meaning |
|---|---|
| `acr-mcp_<version>_<os>_<arch>.tar.gz` / `.zip` | The binary archive |
| `acr-mcp-SHA256SUMS` | SHA-256 of every mirrored `acr-mcp` file (ACR's per-product manifest) |
| `acr-mcp-SHA256SUMS.sigstore.json` | cosign keyless bundle for `acr-mcp-SHA256SUMS` |

The certificate identity is the ACR release workflow, on `main` or a release tag:

```sh
cosign verify-blob acr-mcp-SHA256SUMS \
  --bundle acr-mcp-SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/full-chaos/dev-health-acr/\.github/workflows/release\.yml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+(-(dev|beta)\.[0-9]+)?)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --check acr-mcp-SHA256SUMS
```

These assets carry no `.sig`/`.pem` sidecars and no GitHub attestation from this
repository; the bundle above is the signature.

## Version rule

The tag equals the `version` of every plugin manifest in the tarballs (Claude Code
`plugin.json`, marketplace entries, Codex `plugin.json`). The release fails before
it publishes if any differ. See the release rule in [CHANGELOG.md](../CHANGELOG.md).

## Dry run

The `release` workflow has a manual `workflow_dispatch` run without inputs. It runs the
same verify, build, sign and attest steps and keeps the signed set as a workflow artifact
for 3 days. It publishes no release. The version it checks is the `VERSION` file.
