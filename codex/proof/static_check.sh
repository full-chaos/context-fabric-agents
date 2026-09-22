#!/usr/bin/env bash
# Static proof for the Codex bundle (CHAOS-6202). No network to the MCP host,
# no credential. Runs the pinned Codex CLI on a clean CODEX_HOME and checks:
#   1. both config.toml variants parse (Python tomllib) and Codex loads them;
#   2. `codex mcp list/get` show the dev-health server, with the bearer env var
#      only in the bearer variant;
#   3. the repo marketplace registers, the plugin installs, and its MCP entry
#      and skill are present in the install cache;
#   4. failing-first: a planted wrong-typed key is REJECTED by Codex, and a
#      planted syntax error is rejected by tomllib.
# Requires `codex` on PATH. Exit non-zero on the first failed expectation.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d "${HOME}/codex-static.XXXXXX")"
trap 'rm -rf "$work"' EXIT
export HOME="$work/home"
mkdir -p "$HOME"

fail() { echo "FAIL: $*" >&2; exit 1; }
newhome() { local d; d="$(mktemp -d "$work/ch.XXXXXX")"; echo "$d"; }

echo "codex version: $(codex --version)"

for v in bearer oauth; do
  python3 - "$root/codex/configs/config.$v.toml" <<'PY'
import sys, tomllib
with open(sys.argv[1], "rb") as f:
    d = tomllib.load(f)
t = d["mcp_servers"]["dev-health"]
assert t["url"] == "https://mcp.fullchaos.dev/mcp", t
PY
  ch="$(newhome)"
  cp "$root/codex/configs/config.$v.toml" "$ch/config.toml"
  out="$(CODEX_HOME="$ch" codex mcp get dev-health 2>&1)" || fail "$v: codex mcp get failed: $out"
  echo "--- $v: codex mcp get"; echo "$out"
  echo "$out" | grep -q 'url: https://mcp.fullchaos.dev/mcp' || fail "$v: url missing"
  if [ "$v" = bearer ]; then
    echo "$out" | grep -q 'bearer_token_env_var: ACR_MCP_TOKEN' || fail "bearer: env var missing"
  else
    echo "$out" | grep -q 'bearer_token_env_var: -' || fail "oauth: unexpected bearer env var"
  fi
  CODEX_HOME="$ch" codex mcp list 2>&1 | grep -q '^dev-health ' || fail "$v: mcp list lacks dev-health"
done

# Marketplace + plugin install.
ch="$(newhome)"
export CODEX_HOME="$ch"
codex plugin marketplace add "$root" >/dev/null 2>&1 || fail "marketplace add failed"
codex plugin add dev-health@dev-health 2>&1 | tee "$work/add.txt"
plugin_root="$(sed -n 's/^Installed plugin root: //p' "$work/add.txt")"
[ -f "$plugin_root/skills/dev-health/SKILL.md" ] || fail "plugin cache lacks the skill"
cmp "$plugin_root/skills/dev-health/SKILL.md" "$root/skills/dev-health/SKILL.md" || fail "skill differs from skills/dev-health/SKILL.md"
codex mcp list 2>&1 | tee "$work/plugin-list.txt"
grep -q '^dev-health .*https://mcp.fullchaos.dev/mcp' "$work/plugin-list.txt" || fail "plugin MCP entry not listed"
# The skill must be model-visible: `codex debug prompt-input` renders the skill list.
timeout 90 codex debug prompt-input "hi" >"$work/prompt.json" 2>&1 || fail "codex debug prompt-input failed"
grep -q 'dev-health:dev-health' "$work/prompt.json" || fail "skill dev-health:dev-health not in the model-visible skill list"
echo "skill dev-health:dev-health is listed in the model-visible prompt input"
unset CODEX_HOME

# Failing-first pair: planted defects must be rejected.
ch="$(newhome)"
printf '[mcp_servers.dev-health]\nurl = "https://mcp.fullchaos.dev/mcp"\nbearer_token_env_var = 123\n' > "$ch/config.toml"
if CODEX_HOME="$ch" codex mcp get dev-health >"$work/planted.txt" 2>&1; then
  cat "$work/planted.txt"; fail "planted wrong-typed key was ACCEPTED by Codex"
fi
grep -q 'expected a string' "$work/planted.txt" || { cat "$work/planted.txt"; fail "planted key rejected for an unexpected reason"; }
echo "planted wrong-typed key rejected by Codex: $(grep 'invalid type' "$work/planted.txt")"

printf '[mcp_servers.dev-health\nurl = "x"\n' > "$work/broken.toml"
if python3 -c 'import sys,tomllib; tomllib.load(open(sys.argv[1],"rb"))' "$work/broken.toml" 2>/dev/null; then
  fail "planted syntax error was ACCEPTED by tomllib"
fi
echo "planted syntax error rejected by tomllib"
echo "OK"
