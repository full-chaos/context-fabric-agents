# liveness

Scheduled liveness for the hosted MCP (`https://mcp.fullchaos.dev`).
Workflows: `.github/workflows/liveness.yml` (L1 + L3; every 6 h at :41) and
`.github/workflows/liveness-l2.yml` (L2; every 6 h at 03:11, 09:11, 15:11,
21:11 UTC, offset 3.5 h from L1), both with `workflow_dispatch` (no inputs).
Credential runbook: [RUNBOOK.md](RUNBOOK.md).

## Legs (`legs.json`)

`schema_version` is `cfa.liveness.legs.v1`. Each leg has an `id`, a
`description` and a `mode`:

| Mode | Meaning | Extra fields |
| --- | --- | --- |
| `live` | A workflow job probes the live server and writes `results/<id>.json` | `workflow` (file under `.github/workflows`, required), `job` (workflow job id, required), `required_steps` (optional) |
| `static-only` | Validated offline in PR CI; no live job, no result file | none |
| `declared-off` | Deliberately not run | `reason` and `issue` (https link on linear.app or github.com), both required |

Unknown fields are an error. A new client leg (CHAOS-6203, CHAOS-6205) adds
an entry here; a `live` entry also adds its job to the workflow it names and
to that workflow's aggregate `needs`. A test (`liveness/internal/record`)
fails when a live leg has no job, the aggregator does not need it, or the
aggregator needs a job no leg declares.

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

## L2 real-client connect matrix (`proxy`, `l2`, CHAOS-6205)

One job per client, run one after another: `l2-claude-code`, `l2-codex`,
`l2-opencode-v2`. Each job:

1. installs the pinned client with `npm ci` from its lockfile
   (`ci/claude-code`, `ci/codex`, `ci/opencode`);
2. starts `liveness/proxy`, a reverse proxy that listens on a loopback IP
   only (a host name or any other address is refused) and forwards `/mcp`
   to `https://mcp.fullchaos.dev`, a compiled constant (no flag, no
   environment variable, HTTP proxy variables ignored). Per request it
   appends one JSON line: `seq`, `method` (JSON-RPC method),
   `requested_revision`, `revision` (negotiated), `status`, `latency_ms`.
   It never records a header, a body or a token. A request cap (12, 8, 8:
   28 per run) and 2 s spacing keep the run inside the per-org rate budget;
   an over-cap request gets a local 429;
3. runs `liveness/l2`: render the committed bearer config with only the URL
   changed to the proxy, run the client's connect-only command in a fresh
   HOME with a minimal environment, then judge the recording.

| Client | Connect-only command (no LLM call) |
| --- | --- |
| Claude Code | `claude mcp add-json dev-health <plugins/configs/claude-code.bearer.mcp.json entry> --scope user`, then `claude mcp list` (line `dev-health: <proxy> (HTTP) - ✔ Connected`) and `claude mcp get dev-health` (`Status: ✔ Connected`) |
| Codex | `codex app-server` `mcpServerStatus/list` via `codex/proof/mcp_status.py`, with `codex/configs/config.bearer.toml` as `$CODEX_HOME/config.toml`; every snapshot tool must be listed (Codex has no connect-only `codex mcp` command) |
| OpenCode v2 | `opencode mcp list` in a project holding `opencode/configs/opencode-v2.bearer.json` (line `✓ dev-health  connected`). The CLI asks a background service that connects asynchronously, so the first list on a cold service can be empty: the list is retried (6 × 5 s) and the service is stopped afterwards |

OpenCode v1, Cursor and VS Code stay `static-only` (no non-LLM connect
command in CI). Agent-driven legs (`claude -p`, `codex exec`) are out of
scope: no LLM keys in CI (plan Decision 4).

| Step | Pass when |
| --- | --- |
| `a_install` | `<client> --version` equals the `ci/<client>/package.json` pin, and `compat.json` `pinned_version` equals the pin |
| `b_render` | credential present; proxy URL is `http://<loopback IP>:<port>/mcp`; the committed bearer config names the prod URL exactly once and renders |
| `c_connect` | the connect-only command reports Connected (see table) |
| `d_recorded` | the proxy recorded at least one request, the first one 2xx, and no 429 or 5xx |
| `e_compat` | the recorded first method + negotiated revision equal `contracts/acr-mcp/compat.json` for the client (`status` `confirmed`). Any change, down **or** up, is red with `update compat.json` |

Negotiated revision: `initialize` → `result.protocolVersion`;
`server/discover` → the requested `MCP-Protocol-Version` when
`result.supportedVersions` lists it; other requests → the revision header.

A client upgrade is a PR that bumps `ci/<client>` (package.json + lock) and
`compat.json` together; `liveness/internal/l2` tests fail when the pin,
lock, `compat.json`, `legs.json` and `liveness-l2.yml` disagree. Same-repo
PRs that touch these inputs run the matrix too (a red PR run does not open
the failure issue).

## L3 OAuth discovery chain (`liveness/l3`, CHAOS-6208)

Unauthenticated: no credential, no consent, no token. Endpoint is the
compiled constant `https://mcp.fullchaos.dev`.

| Step | Pass when |
| --- | --- |
| `a_unauth` | an unauthenticated POST is 401 with one `Bearer` challenge whose `resource_metadata` parameter is `https://<host>/.well-known/oauth-protected-resource...` |
| `b_prm` | that URL returns 200; `resource` equals the probed endpoint; `authorization_servers` is non-empty |
| `c_asmeta` | the first authorization server's metadata (`<issuer>/.well-known/oauth-authorization-server`, RFC 8414) returns 200; `code_challenge_methods_supported` contains `S256`; `registration_endpoint` is present (DCR advertised; CIMD, CHAOS-6192, is not live) |

`results/l3.json` follows the same result schema as L1. Kill proof: point the
probe at a path with no PRM (no 401 challenge at all) and every step is red
(`liveness/internal/l3/probe_test.go: TestProbePointedAtAPathWithoutPRM`; a
live run reproduces this against a real path with no OAuth wiring).

The daily proof that dynamic registration actually returns 201
(`POST /register`) is a **separate** leg, `l3-register`, `declared-off` until
CHAOS-6191 (idle client purge) lands — registering a client every 6 h with no
purge would leak an ever-growing set of dead clients. It is never folded into
`l3`'s required steps: a `declared-off` leg carries its own reason and issue
link in `legs.json`, so its absence is visible, not silent.

## Aggregator (`aggregate`)

Runs `if: always()` after every leg job, with `toJSON(needs)` and
`-workflow <file>`: it judges only the live legs of that workflow and lists
the others. Red when:

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
- The `l3-register` leg is `declared-off`: daily dynamic-client registration
  waits on CHAOS-6191 (idle client purge).

## Local run

    go run ./liveness/probe -out results/l1.json    # needs ACR_MCP_CI_BEARER
    go run ./liveness/l3 -out results/l3.json        # no credential
    NEEDS='{"l1":{"result":"success"},"l3":{"result":"success"}}' go run ./liveness/aggregate -workflow liveness.yml

    # L2, one client (pinned client on PATH; needs ACR_MCP_CI_BEARER)
    go run ./liveness/proxy -listen 127.0.0.1:18765 -record /tmp/rec.jsonl &
    go run ./liveness/l2 -client claude-code -proxy-url http://127.0.0.1:18765/mcp -records /tmp/rec.jsonl
