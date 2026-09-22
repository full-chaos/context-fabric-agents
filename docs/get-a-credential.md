# Get a credential

Default: no credential to get. Every client's config names the hosted
server's URL only (CHAOS-6184 OAuth discovery, live on prod; default
flipped in CHAOS-6208), and the client logs in itself on first connect: it
gets a `401` naming the authorization server, registers, and opens your
browser to approve. No token to copy, no environment variable to set.

For headless or CI use — no browser to complete a login — every client also
has a bearer variant: one token, in the environment variable
`ACR_MCP_TOKEN`. This page explains where that token comes from. Each
client's own README shows the exact command to set the variable.

## Headless/CI: bearer token

Two ways to get one.

1. **You already use the ACR STDIO CLI (`acr-mcp`).** Run `acr-mcp login`.
   It opens a device-authorization flow: a verification URL and a code.
   Approve it in the browser, and the CLI stores the token for you. Print it
   with the command your OS keyring or credential store provides; do not
   copy it from a terminal scrollback into a config file.
2. **You don't run `acr-mcp` locally.** Ask an operator to mint one with
   `acr-api credentials create` (organization- and repository-scoped, up to
   365 days). They send you the token value out of band (never in a
   ticket, a chat log, or a committed file).

This repository does not ship the `acr-mcp` binary — it ships client
configs only. If you need the STDIO CLI, see the ACR project's own docs.

## Rules for every credential

- Never write a token into a config file, a repository, or a chat message.
  Every bearer config in this repo expands `ACR_MCP_TOKEN` from the
  environment; it never contains a literal token. The OAuth (default)
  configs carry no credential at all.
- A token you did not mint yourself is not yours to keep. If you no longer
  need it, ask the operator to revoke it.
- A leaked or expired token fails closed: the server answers `401` and
  never falls back to a weaker check. See each client's Troubleshooting
  section for the exact error codes.
