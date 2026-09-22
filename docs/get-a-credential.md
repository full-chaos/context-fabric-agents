# Get a credential

Every client here needs one bearer token, in the environment variable
`ACR_MCP_TOKEN`. This page explains where the token comes from. Each
client's own README shows the exact command to set the variable.

## Today: bearer token

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
  Every config in this repo expands `ACR_MCP_TOKEN` from the environment;
  it never contains a literal token.
- A token you did not mint yourself is not yours to keep. If you no longer
  need it, ask the operator to revoke it.
- A leaked or expired token fails closed: the server answers `401` and
  never falls back to a weaker check. See each client's Troubleshooting
  section for the exact error codes.

## After OAuth ships

<!-- CHAOS-6184 (OAuth 2.1 discovery + PKCE login) is approved but not yet
     deployed on prod; CHAOS-6208 switches these configs to the OAuth
     variant once it is. Do not claim this as available today. -->

Once CHAOS-6184 is live on `mcp.fullchaos.dev`, a client that supports MCP
authorization logs in by itself: it gets a `401` naming the authorization
server, registers, and opens your browser to approve. No token to copy, no
environment variable to set. Until then, and always for headless or CI use,
use the bearer token above.
