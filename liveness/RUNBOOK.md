# CI credential runbook (CHAOS-6196)

No secret values live here. Names and dates only.

## What exists

| Item | Kind | Purpose |
| --- | --- | --- |
| `ACR_MCP_CI_BEARER` | Actions secret | Bearer for the hosted MCP (`context:read`, `evidence:read`, one repository grant, 90-day expiry) |
| `ACR_MCP_URL` | Actions variable | `https://mcp.fullchaos.dev` |
| `ACR_MCP_TRIAL_URL` | Actions variable | `https://mcp.commanderkeen.dev/mcp` |
| `GH_CI_TOKEN` | Actions secret | GitHub token for CI jobs that need the GitHub API |

No LLM keys are stored in CI. Only unauthenticated and bearer connect checks run.

## Mint (operator, on the prod host)

`acr-api credentials create --org-id <org> --name ci-context-fabric-agents-liveness --repository-scope <one repo> --scope context:read,evidence:read --expires-at <now+90d> --actor <who> --json`

Write stdout (the token) to a mode-600 file, pipe it to `gh secret set ACR_MCP_CI_BEARER --repo full-chaos/context-fabric-agents`, then delete the file. Never print it.

## Rotate (every 90 days; open the reminder issue at day 75)

1. `acr-api credentials rotate` (overlap 15 minutes at most).
2. Set the new value with `gh secret set ACR_MCP_CI_BEARER`.
3. Dispatch the `liveness` workflow and confirm it is green.
4. Confirm the old credential is revoked or expired.

## Revoke

`acr-api credentials revoke`. The next authenticated call must return 401. Run the `liveness` workflow: it must go red.

## GitHub token risk

`GH_CI_TOKEN` was minted from a user login. It carries that user's scopes and can act as that user. Replace it with a fine-grained token or a GitHub App installation token limited to this repository. Tracked as a follow-up under CHAOS-6183.
