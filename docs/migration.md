# Migrating from the acr-embedded examples

If you set up a remote client from the ACR project's own
`docs/examples/mcp-clients/*-remote-*` files (`claude-code-remote-mcp.json`,
`codex-remote-config.toml`, `cursor-remote-mcp-config.json`,
`opencode-remote-config.json`, `opencode-v2-remote-config.json`), two things
change. Nothing else does.

| | Old (acr examples) | New (this repo) |
|---|---|---|
| Server key | `acr` | `dev-health` |
| URL | placeholder `https://acr-mcp.dev-health.example.com/mcp` | `https://mcp.fullchaos.dev/mcp` |
| Token env var | `ACR_MCP_TOKEN` | `ACR_MCP_TOKEN` (unchanged) |

## What to do

1. Open your client's config (see [self-hosted.md](self-hosted.md) for the
   file and field per client).
2. Rename the server key from `acr` to `dev-health`.
3. If the URL still reads the acr placeholder, point it at
   `https://mcp.fullchaos.dev/mcp` (or your own deployment).
4. Keep `ACR_MCP_TOKEN` set exactly as before. The token itself does not
   change.
5. Re-verify with your client's own command — see the "Verify" section in
   the matching per-client README in this repo.

## Why the rename

The server key names the product, not the internal runtime. ACR is the
implementation; Dev Health is what a user asks for. This repo also has its
own renderer and goldens — it does not read or generate from the acr
repository — so a rename here never desyncs with acr's copy.

## What stays in acr

The ACR project keeps its STDIO bundles (`clients/*`, `acr-mcp serve`) and
`docs/mcp-sidecar.md`. Only the **remote** (hosted, HTTP) client material
moved here. If you use `acr-mcp` as a local STDIO server, this migration
does not apply to you.

The acr repository's own remote-client docs and fixtures point here once
CHAOS-6209 merges; until then both copies exist, and this repo is the
newer, actively maintained one.
