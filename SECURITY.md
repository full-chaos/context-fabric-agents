# Security policy

## Reporting a vulnerability

Use GitHub private vulnerability reporting: **Security** tab, then **Report a vulnerability**. Do not open a public issue for a security problem. We aim to acknowledge within 5 working days.

## No secrets in this repository

This repository is public. It must never contain:

- credentials, tokens or keys (including `fcacr_*` tokens and literal `Authorization: Bearer <token>` values),
- absolute local paths or machine names,
- customer data or evidence content.

Client configs name only the environment variable `ACR_MCP_TOKEN`. CI runs gitleaks over the full history and a repository test that fails on these patterns. GitHub secret scanning and push protection are on.

If you find a secret here, report it as above. Treat it as leaked: it will be revoked, not only removed.

## Scope

Configs, skills and probes for the Context Fabric hosted MCP. Server-side issues: report through the same path; we route them to the server owners.
