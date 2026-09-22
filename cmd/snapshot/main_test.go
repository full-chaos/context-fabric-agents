package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Failing-first pair 1: an unset credential is a red run with the exact
// message, not a skip.
func TestCaptureWithoutCredentialFails(t *testing.T) {
	t.Setenv(tokenEnv, "")
	var out, errb bytes.Buffer
	code := run([]string{"capture", "-out", filepath.Join(t.TempDir(), "s.json")}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if got := strings.TrimSpace(errb.String()); got != "ACR_MCP_CI_BEARER missing" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestEndpointIsCompiledProdHost(t *testing.T) {
	if endpoint != "https://mcp.fullchaos.dev/mcp" {
		t.Fatalf("endpoint = %q", endpoint)
	}
	// No flag may override it.
	var out, errb bytes.Buffer
	t.Setenv(tokenEnv, "x")
	if code := run([]string{"capture", "-out", "o", "-url", "http://evil"}, &out, &errb); code == 0 {
		t.Fatal("-url must not be accepted")
	}
}

func writeSnap(t *testing.T, dir, name, tools string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	body := `{"schema_version":"mcp_tools.v1","captured_at":"2026-09-21T00:00:00Z","host":"h","server_info":{"name":"n","version":"1"},"negotiations":[],"tools":[` + tools + `],"resources":[],"prompts":[]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDiffExitCodes(t *testing.T) {
	dir := t.TempDir()
	two := `{"name":"a","description_digest":"d","input_schema_digest":"s","input_schema":{}},{"name":"b","description_digest":"d","input_schema_digest":"s","input_schema":{}}`
	one := `{"name":"a","description_digest":"d","input_schema_digest":"s","input_schema":{}}`
	full := writeSnap(t, dir, "full.json", two)
	dropped := writeSnap(t, dir, "dropped.json", one)

	var out, errb bytes.Buffer
	if code := run([]string{"diff", "-old", full, "-new", full}, &out, &errb); code != 0 {
		t.Fatalf("equal snapshots: exit %d", code)
	}
	out.Reset()
	if code := run([]string{"diff", "-old", full, "-new", dropped}, &out, &errb); code != exitDrift {
		t.Fatalf("live lost a tool: exit %d, want %d", code, exitDrift)
	}
	if !strings.Contains(out.String(), "Severity: **major**") || !strings.Contains(out.String(), "`b` removed") {
		t.Fatalf("report = %q", out.String())
	}
	out.Reset()
	if code := run([]string{"diff", "-old", dropped, "-new", full}, &out, &errb); code != exitDrift || !strings.Contains(out.String(), "**minor**") {
		t.Fatalf("committed snapshot dropped a tool: exit %d out %q", code, out.String())
	}
	if code := run([]string{"diff", "-old", filepath.Join(dir, "missing.json"), "-new", full}, &out, &errb); code != 1 {
		t.Fatalf("missing old snapshot must be an error, got %d", code)
	}
}
