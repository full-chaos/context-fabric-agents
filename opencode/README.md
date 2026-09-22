# OpenCode

Config files for OpenCode to use the hosted Dev Health MCP server
(`https://mcp.fullchaos.dev/mcp`). OpenCode has two config generations with
different shapes; pick the file that matches your OpenCode version.

Rendered and checked by `internal/render` (`go run ./cmd/render -check`).
Doc sources: [opencode.ai/docs/mcp-servers](https://opencode.ai/docs/mcp-servers)
(v1), [opencode.ai/v2/docs/mcp-servers](https://opencode.ai/v2/docs/mcp-servers)
(v2), [opencode.ai/docs/skills/](https://opencode.ai/docs/skills/) (Agent
Skills).

## Install

Merge the file's content into your OpenCode config (project: `opencode.json`
or `opencode.jsonc` at the repo root; global: `~/.config/opencode/opencode.json`).
Don't just copy the file over an existing config — merge the `mcp` key.

| OpenCode generation | Bearer (token env var) | OAuth (server-driven login) |
| --- | --- | --- |
| v1 | [`configs/opencode.bearer.json`](configs/opencode.bearer.json) | [`configs/opencode.oauth.json`](configs/opencode.oauth.json) |
| v2 | [`configs/opencode-v2.bearer.json`](configs/opencode-v2.bearer.json) | [`configs/opencode-v2.oauth.json`](configs/opencode-v2.oauth.json) |

- **Bearer**: export `ACR_MCP_TOKEN` in the environment OpenCode starts
  from. OpenCode expands `{env:ACR_MCP_TOKEN}` itself; the token is never
  written into the config file. The bearer variant sets `"oauth": false` so
  OpenCode does not also try to auto-detect OAuth.
- **OAuth**: no token, no `headers` key. OpenCode discovers the
  authorization server from the hosted server's 401 challenge and runs its
  own login flow.

v2 additionally sets `"protocol": "auto"` (negotiate the server's revision
rather than pin one).

## Skill

The shared `dev-health` skill (tool order, evidence handling; see
[`skills/dev-health/SKILL.md`](skills/dev-health/SKILL.md)) is copied into
this bundle at [`skills/dev-health/SKILL.md`](skills/dev-health/SKILL.md).
Per [opencode.ai/docs/skills/](https://opencode.ai/docs/skills/), OpenCode
loads project skills from `.opencode/skills/<name>/SKILL.md` (also
`.claude/skills/` and `.agents/skills/`) and global skills from
`~/.config/opencode/skills/<name>/SKILL.md`. Copy this bundle's
`skills/dev-health/` directory to `.opencode/skills/dev-health/` in your
project (or the global equivalent).

## Install link

No confirmed one-click "Add to OpenCode" link format exists on the cited
doc pages: not confirmed.

## Validation

- Every file here is strict JSON (no duplicate keys, no unknown fields, no
  trailing data) and matches the doc-derived shape checks in
  `internal/render/validate.go`. Both run in every PR (`go test ./...`).
- The v1 files additionally validate live against the vendor's own JSON
  Schema, `https://opencode.ai/config.json`, fetched fresh on every CI run
  (job `opencode-schema` in `.github/workflows/ci.yml`; tool
  `cmd/opencodeschema`). If that schema is unreachable, the job posts a
  `::warning::` and does not fail the build — never a silent pass. v2 does
  not declare a `$schema` and has no published schema to check against.
- No OpenCode client binary parses these files in CI (unlike Codex and
  Claude Code, which do; see `internal/render/README.md`). `legs.json`
  records this leg as `static-only`.
