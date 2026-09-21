# Contributing

## Branches

Branch from `main`, named by topic: `<type>/<topic>` (for example `feat/claude-code-plugin`). Never commit to `main`.

## Pull requests

- Title: `<type>(<area>): CHAOS-<n> <short imperative description>`. `type` is one of `feat`, `fix`, `bug`, `enhancement`, `chore`, `docs`. `area` is the code you touched.
- Cite exactly one ticket. If a change spans tickets, split the ticket first.
- Body carries these exact headings (a governance parser reads them literally):
  - `## TEST-EVIDENCE` - what you ran and what it showed.
  - `## RISK-NOTES` - what can go wrong, and what must never land here.
- A PR that depends on another open PR opens stacked on it.
- No agent attribution lines in commits or PR text.

## Rules for this public repo

- No secrets, tokens, absolute local paths or customer data. See [SECURITY.md](SECURITY.md).
- Pin every GitHub Action by full commit SHA. Workflows default to `permissions: {}`. No `pull_request_target`.
- Plugins ship no hooks and no executables. Installs use each client's native path, never a pipe-to-shell.
- A check that did not run must fail, not pass.

## Local checks

```sh
go vet ./...
go test ./...
```
