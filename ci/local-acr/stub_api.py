#!/usr/bin/env python3
"""Loopback stand-in for the hosted ACR API (CHAOS-6544).

PR and push CI must never reach the production MCP host, so client checks run
against a local acr-mcp (the released, cosign-verified binary from this repo's
mirror release). acr-mcp resolves every caller by asking the hosted API for
GET /api/v1/agent-context/capabilities with the caller's bearer. This stub
answers that one route, for any well-formed bearer, with a capabilities.v1
document that clears acr-mcp's compatibility gate. It binds 127.0.0.1 only
and never logs a header.
"""
import http.client
import json
import re
import ssl
import sys
import threading
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer, ThreadingHTTPServer

CAPS_PATH = "/api/v1/agent-context/capabilities"
SCHEMAS = [
    "mcp_context_for_task_request.v1", "mcp_context_for_task_response.v1",
    "mcp_source_evidence_request.v1", "mcp_source_evidence_response.v1",
    "context_packet_request.v1", "context_packet.v1", "context_packet_item.v1",
    "evidence_ref.v1", "expanded_evidence.v1",
    "mcp_investigate_question_request.v1", "mcp_investigate_question_response.v1",
    "mcp_investigation_result_request.v1", "mcp_investigation_result_response.v1",
    "context_fabric_investigation_request.v1", "context_fabric_investigation_result.v1",
    "context_fabric_answer_projection.v1",
    "mcp_record_episode_request.v1", "mcp_record_episode_response.v1",
    "agent_episode_create.v1", "agent_episode.v1",
]


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        if self.path != CAPS_PATH or not self.headers.get("Authorization", "").startswith("Bearer "):
            self.send_response(404 if self.path != CAPS_PATH else 401)
            self.end_headers()
            return
        body = json.dumps({
            "schema_version": "capabilities.v1",
            "service": "dev-health-acr",
            "service_version": "local-stub",
            "minimum_sidecar_version": "0.1.0",
            "supported_schema_versions": SCHEMAS,
            "enabled_tools": ["context_for_task", "source_evidence", "investigate_question", "investigation_result"],
            "entitlements": {"agent_context_runtime": True},
            "permissions": {"context_read": True, "evidence_read": True, "episode_write": False},
            "limits": {"max_items": 20, "max_output_tokens": 4000, "max_serialized_bytes": 262144, "requests_per_minute": 600},
            "generated_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        }).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class TLSFront(BaseHTTPRequestHandler):
    """HTTPS front on https://localhost:<port>: Claude Code discovers OAuth
    metadata over HTTPS only. Passes every request through to the plain-HTTP
    acr-mcp, and answers the authorization-server metadata itself (there is no
    real authorization server here; nothing ever completes a login)."""

    upstream_port = 0
    base = ""

    def log_message(self, *_):
        pass

    def _as_metadata(self):
        base = TLSFront.base
        body = json.dumps({
            "issuer": base,
            "authorization_endpoint": base + "/authorize",
            "token_endpoint": base + "/token",
            "registration_endpoint": base + "/register",
            "response_types_supported": ["code"],
            "grant_types_supported": ["authorization_code"],
            "code_challenge_methods_supported": ["S256"],
            "token_endpoint_auth_methods_supported": ["none"],
        }).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _proxy(self):
        if self.path.startswith(("/.well-known/oauth-authorization-server", "/.well-known/openid-configuration")):
            return self._as_metadata()
        # Only the MCP endpoint and OAuth discovery documents are proxied; any
        # other path (or one carrying control characters) is refused.
        if not re.fullmatch(r"/(mcp|\.well-known/oauth-protected-resource(/mcp)?)", self.path):
            self.send_response(404)
            self.send_header("Connection", "close")
            self.end_headers()
            return
        length = int(self.headers.get("Content-Length") or 0)
        data = self.rfile.read(length) if length else None
        conn = http.client.HTTPConnection("127.0.0.1", TLSFront.upstream_port, timeout=60)
        headers = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "connection")}
        conn.request(self.command, self.path, body=data, headers=headers)
        resp = conn.getresponse()
        self.send_response(resp.status)
        for k, v in resp.getheaders():
            if k.lower() not in ("transfer-encoding", "connection", "content-length") and not re.search(r"[\r\n]", k + v):
                self.send_header(k, v)
        self.send_header("Connection", "close")
        self.end_headers()
        while True:
            chunk = resp.read1(65536)
            if not chunk:
                break
            self.wfile.write(chunk)
            self.wfile.flush()
        conn.close()

    do_GET = do_POST = do_DELETE = do_OPTIONS = _proxy


if __name__ == "__main__":
    # usage: stub_api.py <api-port> [<tls-port> <acr-mcp-port> <cert> <key>]
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18791
    if len(sys.argv) > 2:
        tls_port, mcp_port, cert, key = int(sys.argv[2]), int(sys.argv[3]), sys.argv[4], sys.argv[5]
        TLSFront.upstream_port = mcp_port
        TLSFront.base = "https://localhost:%d" % tls_port
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.minimum_version = ssl.TLSVersion.TLSv1_2
        ctx.load_cert_chain(cert, key)
        front = ThreadingHTTPServer(("127.0.0.1", tls_port), TLSFront)
        front.socket = ctx.wrap_socket(front.socket, server_side=True)
        threading.Thread(target=front.serve_forever, daemon=True).start()
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()
