#!/usr/bin/env bash
# CHAOS-6544. Start a LOCAL acr-mcp (HTTP transport, 127.0.0.1 only) so PR and
# push client checks never touch the production MCP host.
#
#   1. download the acr-mcp linux/amd64 asset from this repo's mirror release
#      and check it against the release's acr-mcp-SHA256SUMS;
#   2. start stub_api.py (the loopback stand-in for the hosted ACR API);
#   3. start acr-mcp pointed at the stub;
#   4. export LOCAL_MCP_URL (an HTTPS front on https://localhost, self-signed;
#      Claude Code discovers OAuth metadata over HTTPS only), NODE_EXTRA_CA_CERTS
#      (trust for that cert) and LOCAL_MCP_TOKEN (a well-formed, worthless
#      bearer: it is valid against the stub only) to $GITHUB_ENV.
#
# Env: GH_TOKEN (release download), LOCAL_ACR_PORT (default 18790),
# LOCAL_ACR_DIR (default $RUNNER_TEMP/local-acr), LOCAL_ACR_TAG (default: the
# latest release of this repo). Every request logs to $LOCAL_ACR_DIR/acr-mcp.log.
set -euo pipefail

port="${LOCAL_ACR_PORT:-18790}"
api_port="$((port + 1))"
tls_port="$((port + 2))"
dir="${LOCAL_ACR_DIR:-${RUNNER_TEMP:-/tmp}/local-acr}"
repo="${GITHUB_REPOSITORY:-full-chaos/context-fabric-agents}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mkdir -p "$dir"

tag_args=()
[ -n "${LOCAL_ACR_TAG:-}" ] && tag_args=("$LOCAL_ACR_TAG")
gh release download "${tag_args[@]}" -R "$repo" -D "$dir" --clobber \
  -p 'acr-mcp-SHA256SUMS' -p 'acr-mcp_*_linux_amd64.tar.gz'
(cd "$dir" && grep ' acr-mcp_.*_linux_amd64\.tar\.gz$' acr-mcp-SHA256SUMS | sha256sum -c -)
tar -xzf "$dir"/acr-mcp_*_linux_amd64.tar.gz -C "$dir" acr-mcp

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost \
  -addext 'subjectAltName=DNS:localhost' -keyout "$dir/key.pem" -out "$dir/cert.pem" 2>/dev/null
python3 "$here/stub_api.py" "$api_port" "$tls_port" "$port" "$dir/cert.pem" "$dir/key.pem" > "$dir/stub.log" 2>&1 &
echo "$!" > "$dir/stub.pid"

ACR_MCP_TRANSPORT=http \
ACR_MCP_HTTP_LISTEN="127.0.0.1:$port" \
ACR_API_URL="http://127.0.0.1:$api_port" \
ACR_MCP_AUTHORIZATION_SERVER="https://localhost:$tls_port" \
ACR_MCP_RESOURCE_URL="https://localhost:$tls_port/mcp" \
  "$dir/acr-mcp" serve > "$dir/acr-mcp.log" 2>&1 &
echo "$!" > "$dir/acr-mcp.pid"

for _ in $(seq 1 50); do
  grep -q 'acr-mcp http serving' "$dir/acr-mcp.log" 2>&1 && break
  kill -0 "$(cat "$dir/acr-mcp.pid")" 2>/dev/null || { cat "$dir/acr-mcp.log"; echo "acr-mcp exited" >&2; exit 1; }
  sleep 0.2
done
grep -q 'acr-mcp http serving' "$dir/acr-mcp.log" || { cat "$dir/acr-mcp.log"; echo "acr-mcp did not start" >&2; exit 1; }

secret="$(head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=')"
url="https://localhost:$tls_port/mcp"
echo "local acr-mcp ready at $url"
if [ -n "${GITHUB_ENV:-}" ]; then
  { echo "LOCAL_MCP_URL=$url"; echo "LOCAL_MCP_TOKEN=fcacr_$secret"; echo "NODE_EXTRA_CA_CERTS=$dir/cert.pem"; } >> "$GITHUB_ENV"
else
  echo "export LOCAL_MCP_URL=$url LOCAL_MCP_TOKEN=fcacr_$secret NODE_EXTRA_CA_CERTS=$dir/cert.pem"
fi
