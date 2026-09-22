# Cursor

Config file for Cursor to use the hosted Dev Health MCP server
(`https://mcp.fullchaos.dev/mcp`).

Rendered and checked by `internal/render` (`go run ./cmd/render -check`).
Doc sources: [cursor.com/docs/context/mcp](https://cursor.com/docs/context/mcp)
(MCP config), [cursor.com/docs/context/skills](https://cursor.com/docs/context/skills)
(Agent Skills).

## Install

Merge the file's content into `.cursor/mcp.json` (project) or
`~/.cursor/mcp.json` (global) under the `mcpServers` key. Don't overwrite an
existing file; merge in the `dev-health` entry.

| Variant | File |
| --- | --- |
| Bearer (token env var) | [`configs/mcp.bearer.json`](configs/mcp.bearer.json) |
| OAuth (Cursor-driven login) | [`configs/mcp.oauth.json`](configs/mcp.oauth.json) |

- **Bearer**: export `ACR_MCP_TOKEN` in the environment Cursor starts from.
  Cursor expands `${env:ACR_MCP_TOKEN}` itself; the token is never written
  into the config file.
- **OAuth**: no `headers` key at all. Per the cited doc, a server entry with
  no `headers`/auth object means Cursor runs dynamic client registration
  and its own OAuth login on first connect.

## Skill

The shared `dev-health` skill (tool order, evidence handling; see
[`skills/dev-health/SKILL.md`](skills/dev-health/SKILL.md)) is copied into
this bundle at [`skills/dev-health/SKILL.md`](skills/dev-health/SKILL.md).
Per [cursor.com/docs/context/skills](https://cursor.com/docs/context/skills),
Cursor loads project skills from `.cursor/skills/<name>/SKILL.md` (also
`.agents/skills/`, and for compatibility `.claude/skills/` and
`.codex/skills/`) and global skills from `~/.cursor/skills/<name>/SKILL.md`
(also `~/.agents/skills/`). Copy this bundle's `skills/dev-health/`
directory to `.cursor/skills/dev-health/` in your project (or the global
equivalent).

## Install link

The cited doc describes an "Add to Cursor" button on Cursor's own MCP
marketplace entries, but does not publish the deep-link URL format (no
`cursor://...` scheme is documented on that page). No link is included
here: not confirmed.

## Validation

- Every file here is strict JSON (no duplicate keys, no unknown fields, no
  trailing data) and matches the doc-derived shape checks in
  `internal/render/validate.go`. Both run in every PR (`go test ./...`).
- No Cursor client binary parses these files in CI (unlike Codex and Claude
  Code, which do; see `internal/render/README.md`). `legs.json` records
  this leg as `static-only`.
