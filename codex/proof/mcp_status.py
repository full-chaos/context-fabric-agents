#!/usr/bin/env python3
"""Connect-only proof for the Dev Health MCP entry in Codex (CHAOS-6202).

Codex has no `codex mcp` subcommand that connects (`list` and `get` only read
config). The `codex app-server` JSON-RPC method `mcpServerStatus/list` makes
Codex itself connect to each configured MCP server and report the connection
state, server info and tool catalog, with no LLM call and no OpenAI login.

Usage: mcp_status.py <server-name> [--expect-tool NAME ...]

Run with CODEX_HOME pointing at a directory holding config.toml. Prints one
redacted JSON summary line. Exit 0 only if tool discovery reported no error, the server
reported its serverInfo, and every expected tool is listed. (runtimeStatus is
null without a thread, so it is printed but not judged.) The bearer token is never read or
printed by this script; Codex reads it from the environment.
"""
import json
import subprocess
import sys
import threading

TIMEOUT_S = 60


def main(argv):
    name = argv[1]
    expect_tools = []
    i = 2
    while i < len(argv):
        if argv[i] == "--expect-tool":
            expect_tools.append(argv[i + 1])
        else:
            print("unknown arg " + argv[i], file=sys.stderr)
            return 2
        i += 2

    proc = subprocess.Popen(
        ["codex", "app-server"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    )
    timer = threading.Timer(TIMEOUT_S, proc.kill)
    timer.start()

    def send(msg):
        proc.stdin.write(json.dumps(msg) + "\n")
        proc.stdin.flush()

    def call(req_id, method, params):
        send({"jsonrpc": "2.0", "id": req_id, "method": method, "params": params})
        while True:
            line = proc.stdout.readline()
            if not line:
                raise RuntimeError("codex app-server closed before answering " + method)
            try:
                msg = json.loads(line)
            except ValueError:
                continue
            if msg.get("id") == req_id and ("result" in msg or "error" in msg):
                if "error" in msg:
                    raise RuntimeError(method + ": " + json.dumps(msg["error"]))
                return msg["result"]

    try:
        init = call(1, "initialize", {
            "clientInfo": {"name": "dev-health-agents-proof", "version": "0"},
        })
        send({"jsonrpc": "2.0", "method": "initialized"})
        found = None
        cursor = None
        while found is None:
            res = call(2, "mcpServerStatus/list", {"cursor": cursor, "detail": "full"})
            for s in res.get("data", []):
                if s.get("name") == name:
                    found = s
            cursor = res.get("nextCursor")
            if found is None and not cursor:
                break
    finally:
        timer.cancel()
        proc.kill()

    if found is None:
        print(json.dumps({"server": name, "found": False}))
        return 1
    tools = sorted(found.get("tools", {}).keys())
    info = found.get("serverInfo") or {}
    summary = {
        "codex_user_agent": init.get("userAgent"),
        "server": name,
        "found": True,
        "runtimeStatus": found.get("runtimeStatus"),
        "authStatus": found.get("authStatus"),
        "serverInfo": {"name": info.get("name"), "version": info.get("version")},
        "toolsError": found.get("toolsError"),
        "tools": tools,
        "resources": len(found.get("resources", [])),
    }
    print(json.dumps(summary))
    ok = (
        found.get("toolsError") is None
        and bool(info.get("name"))
        and bool(expect_tools)
        and all(t in tools for t in expect_tools)
    )
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
