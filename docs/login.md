# `login`: device-grant sign-in for headless and remote clients

Codex and Claude Code do not request the OAuth device grant for MCP
themselves — they need a working browser on the same machine to finish an
authorization-code login. `login` is the workaround: run it on a headless
host (a remote box, a container, over SSH with no browser), approve it from
a browser on any other machine, and it writes the resulting bearer where the
client reads it. No manual token copy-paste.

It implements RFC 8628 (OAuth 2.0 Device Authorization Grant) against the
Dev Health hosted MCP server's authorization server (CHAOS-6233). It never
guesses the server's endpoints: it discovers them the same way every client
in this repo does, from the target MCP URL's own `401` challenge.

## Install

Download the binary for your OS/arch from a
[release](https://github.com/full-chaos/context-fabric-agents/releases) (see
[verify-release.md](verify-release.md) to check the signature), mark it
executable (`chmod +x context-fabric-agents-login-*`), or run it from a
checkout:

```bash
go run ./cmd/login --client codex
```

## Usage

```
login --client <codex|claude-code|env|stdout> [--mcp-url URL] [--scope "s1 s2"] [--timeout 15m]
```

| Flag | Default | Meaning |
|---|---|---|
| `--client` | (required) | Where the token ends up. See below. |
| `--mcp-url` | `https://mcp.fullchaos.dev/mcp` | The hosted MCP endpoint to sign in to. |
| `--scope` | `context:read evidence:read` | Space-separated OAuth scopes to request. |
| `--timeout` | `15m` | Give up waiting for approval after this long (also bounded by the server's own device-code expiry — `login` never polls past either). |
| `--insecure-loopback` | off | Allow `http://` (never `https://`) for `--mcp-url` and every discovered endpoint, and only on a loopback host (`127.0.0.1`/`::1`/`localhost`) — for pointing `login` at a local dev server. Every other `http://` target is refused: a bearer token is never sent over plain HTTP. |

Run it, then:

1. It prints a URL. Open it in a browser on **any** machine — it does not
   need to be the machine running `login`.
2. Approve the sign-in.
3. `login` polls until it is approved (or denied, or the device code
   expires) and writes the token.

### `--client codex`

Writes `ACR_MCP_TOKEN` to a 0600 file under your config directory and
appends the bearer `[mcp_servers.dev-health]` table to `~/.codex/config.toml`
(or `$CODEX_HOME/config.toml`) — the same table
[`configs/config.bearer.toml`](../codex/configs/config.bearer.toml) ships,
so it stays byte-identical to what `cmd/render` produces. It never
overwrites a `dev-health` table that is already there (you may have edited
it). Before starting Codex, source the env file:

```bash
source ~/.config/context-fabric-agents/login/codex.env   # or $XDG_CONFIG_HOME equivalent
codex
```

### `--client claude-code`

Runs `claude mcp add --transport http dev-health <url> --header 'Authorization: Bearer ${ACR_MCP_TOKEN}'`
if the `claude` CLI is on `PATH` — the same command
[`plugins/README.md`](../plugins/README.md) documents for manual bearer
setup. The header names `ACR_MCP_TOKEN` by reference; the literal token
never reaches Claude Code's config file, only the environment variable
does. `login` also writes `ACR_MCP_TOKEN` to a 0600 env file. If `claude`
is not on `PATH`, it prints the exact command to run yourself instead
(never the token). Either way, source the env file before starting Claude
Code:

```bash
source ~/.config/context-fabric-agents/login/claude-code.env
claude
```

### `--client env`

Writes `ACR_MCP_TOKEN` to a 0600 env file only; no client config is
touched. Use this to wire the token into your own setup.

### `--client stdout`

Prints the bare token on stdout and nothing else — for scripted capture,
e.g. `TOKEN=$(login --client stdout)`. This is the one mode that puts the
token where a script or terminal can see it; every other mode never prints
it.

## What `login` never does

- Never prints the token, except `--client stdout`'s single line.
- Never writes a credential file that is not `0600`, and refuses to write
  through a pre-existing symlink at that path.
- Never polls past the device code's `expires_in` or your `--timeout`,
  whichever comes first.
- Exits non-zero with the exact refusal code the server reports
  (`access_denied`, `expired_token`, `invalid_client`, ...) on any failure —
  never a generic "something went wrong".

## Troubleshooting

| Symptom | Cause |
|---|---|
| `login: discovery failed: unauthenticated POST ... returned 200` | `--mcp-url` points at something that does not require OAuth, or is wrong. |
| `login: device_authorization refused: invalid_client` | Registration did not complete, or a stale `client_id` was reused across an unrelated run. Just re-run `login`; it registers fresh every time. |
| `login: token refused: access_denied` | The approval was denied in the browser. |
| `login: token refused: expired_token` | Nobody approved it before the device code (or `--timeout`) expired. Re-run `login`. |
| `login: timed out waiting for approval` | `--timeout` elapsed first; raise it with `--timeout 30m` or approve faster. |
