# liveness

Scheduled liveness for the hosted MCP (`https://mcp.fullchaos.dev/mcp`).
Workflow: `.github/workflows/liveness.yml` (every 6 h at :41, plus
`workflow_dispatch` with no inputs). Credential runbook: [RUNBOOK.md](RUNBOOK.md).

## Legs (`legs.json`)

`schema_version` is `cfa.liveness.legs.v1`. Each leg has an `id`, a
`description` and a `mode`:

| Mode | Meaning | Extra fields |
| --- | --- | --- |
| `live` | A workflow job probes the live server and writes `results/<id>.json` | `job` (workflow job id, required), `required_steps` (optional) |
| `static-only` | Validated offline in PR CI; no live job, no result file | none |
| `declared-off` | Deliberately not run | `reason` and `issue` (https link on linear.app or github.com), both required |

Unknown fields are an error. A new client leg (CHAOS-6203, CHAOS-6205) adds
an entry here; a `live` entry also adds its job to the workflow and to the
aggregate job's `needs`. A test (`liveness/internal/record`) fails when a
live leg has no job or the aggregator does not need it.

## L1 protocol probe (`probe`, CHAOS-6204)

go-sdk v1.8.0 client. Host and repository (`full-chaos/dev-health-acr`, the
credential's one grant) are compiled constants; the token comes only from
`ACR_MCP_CI_BEARER`. Requests are serial, at least 6 s apart, at most 30 per
run (a run uses about 8). A 429, or a `rate_limit` tool error, is retried
once after 30 s.

| Step | Pass when |
| --- | --- |
| `a_discover` | first request is `server/discover`; negotiated `2026-07-28`; equals the snapshot's pinned negotiation |
| `b_initialize` | first request is `initialize`; negotiated `2025-06-18`; equals the snapshot's pinned negotiation |
| `c_contract` | tool names + input-schema digests, resource names + URIs and prompt names equal `contracts/acr-mcp/snapshot.json` |
| `d_context` | `context_for_task` on the granted repository returns a packet (`context_packet_id`); a tool error (for example `repo_forbidden`) fails |
| `e_unauth` | an unauthenticated POST is 401 with one `Bearer` challenge; a `resource_metadata` parameter must be this host's `/.well-known/oauth-protected-resource` over https |

A missing credential fails steps a–d (red, never a skip). The probe writes
`results/l1.json` on every run: `leg`, `status`, `steps[]`, `negotiated`,
`server_version`, `request_count`. It never records a header, a body or the
credential.

## Aggregator (`aggregate`)

Runs `if: always()` after every leg job, with `toJSON(needs)`. Red when:

- a live leg's job is absent from `needs`, or its result is not `success` (failure, skipped, cancelled);
- a live leg's result file is missing, malformed (strict decode: unknown field, trailing data, wrong schema), claims another leg, is not `pass`, has a step that did not pass, or lacks a `required_steps` entry;
- a job in `needs` is not a declared live leg;
- any other file or directory appears in `results/`.

It writes the job summary table (leg, mode, job, job result, status,
revision, notes). A red run makes the `report` job (the only job with
`issues: write`) open or comment on one open issue labelled
`liveness-failure`. Close that issue once a run is green.

## Deviations

- No `liveness` GitHub environment. The credential is the repository secret
  `ACR_MCP_CI_BEARER` (shared with `contract-drift.yml`). Moving it into an
  environment needs the operator to set the value again
  (`gh secret set ACR_MCP_CI_BEARER --env liveness`); a secret value cannot be
  read back to copy it.
- The trial leg is `declared-off`: trial hosts sit behind Cloudflare Access
  by design (CHAOS-6222).

## Local run

    go run ./liveness/probe -out results/l1.json    # needs ACR_MCP_CI_BEARER
    NEEDS='{"l1":{"result":"success"}}' go run ./liveness/aggregate
