package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A minimal schema equivalent in shape to opencode.ai/config.json's relevant
// slice: an object at the top level with additionalProperties: false, one
// declared "mcp" property that is a free-form object. This is intentionally
// not a copy of the real vendor schema (see the package doc: this repo
// keeps no local pin of it); it exists only to exercise this program's
// fetch/validate/report logic against a local, controlled server.
const testSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "$schema": {"type": "string"},
    "mcp": {"type": "object"}
  }
}`

func serveSchema(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunAcceptsAConformingConfig(t *testing.T) {
	srv := serveSchema(t, testSchema, http.StatusOK)
	p := writeFile(t, `{"$schema": "x", "mcp": {"dev-health": {}}}`)
	var out, errOut bytes.Buffer
	rc := run([]string{"-schema-url", srv.URL, p}, &out, &errOut)
	if rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr: %s", rc, errOut.String())
	}
	if !strings.Contains(out.String(), "OK") {
		t.Errorf("stdout = %q, want it to report OK", out.String())
	}
}

// TestRunRejectsAnUnknownTopLevelKey is the failing-first pair for
// CHAOS-6203: additionalProperties: false at the schema root must reject an
// unplanned top-level key, the same way the real opencode.ai/config.json
// schema does (verified by hand against the live schema; see the PR body).
func TestRunRejectsAnUnknownTopLevelKey(t *testing.T) {
	srv := serveSchema(t, testSchema, http.StatusOK)
	p := writeFile(t, `{"$schema": "x", "mcp": {}, "bogus_top_level_key": true}`)
	var out, errOut bytes.Buffer
	rc := run([]string{"-schema-url", srv.URL, p}, &out, &errOut)
	if rc != 1 {
		t.Fatalf("rc = %d, want 1 (validation failure); stdout: %s", rc, out.String())
	}
	if !strings.Contains(errOut.String(), "FAIL") {
		t.Errorf("stderr = %q, want it to report FAIL", errOut.String())
	}
}

func TestRunRejectsInvalidJSON(t *testing.T) {
	srv := serveSchema(t, testSchema, http.StatusOK)
	p := writeFile(t, `{not json`)
	var out, errOut bytes.Buffer
	rc := run([]string{"-schema-url", srv.URL, p}, &out, &errOut)
	if rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	if !strings.Contains(errOut.String(), "invalid JSON") {
		t.Errorf("stderr = %q, want it to name invalid JSON", errOut.String())
	}
}

// TestRunReportsUnreachableAsExitTwo: exit 2 must be distinct from exit 0.
// The caller (CI) treats 2 as "post a red note, skip", never a silent pass.
func TestRunReportsUnreachableAsExitTwo(t *testing.T) {
	srv := serveSchema(t, "not the schema you are looking for", http.StatusInternalServerError)
	p := writeFile(t, `{"mcp": {}}`)
	var out, errOut bytes.Buffer
	rc := run([]string{"-schema-url", srv.URL, p}, &out, &errOut)
	if rc != 2 {
		t.Fatalf("rc = %d, want 2 (unreachable); stderr: %s", rc, errOut.String())
	}
	if !strings.Contains(errOut.String(), "unreachable") {
		t.Errorf("stderr = %q, want it to say unreachable", errOut.String())
	}
}

func TestRunReportsUnparsableSchemaAsExitTwo(t *testing.T) {
	srv := serveSchema(t, `{not a schema`, http.StatusOK)
	p := writeFile(t, `{"mcp": {}}`)
	var out, errOut bytes.Buffer
	rc := run([]string{"-schema-url", srv.URL, p}, &out, &errOut)
	if rc != 2 {
		t.Fatalf("rc = %d, want 2 (unreachable/unparsable schema); stderr: %s", rc, errOut.String())
	}
}

func TestRunRequiresAtLeastOneFile(t *testing.T) {
	var out, errOut bytes.Buffer
	rc := run([]string{"-schema-url", "http://example.invalid"}, &out, &errOut)
	if rc != 2 {
		t.Fatalf("rc = %d, want 2 (usage error)", rc)
	}
}

// TestRunAgainstTheRealOpenCodeConfigs is a repo-shaped smoke test: it uses
// the same minimal local schema (not the live vendor one; see testSchema's
// doc comment) to confirm every rendered OpenCode artifact is at least
// strict, parseable JSON with only the properties that schema allows. The
// live check against the real https://opencode.ai/config.json runs in CI
// (see .github/workflows/ci.yml, job opencode-schema), never here: this
// keeps `go test ./...` hermetic.
func TestRunAgainstTheRealOpenCodeConfigs(t *testing.T) {
	srv := serveSchema(t, testSchema, http.StatusOK)
	root := repoRoot(t)
	files := []string{
		"opencode/configs/opencode.bearer.json",
		"opencode/configs/opencode.oauth.json",
	}
	var out, errOut bytes.Buffer
	args := []string{"-schema-url", srv.URL}
	for _, f := range files {
		args = append(args, filepath.Join(root, f))
	}
	rc := run(args, &out, &errOut)
	if rc != 0 {
		t.Fatalf("rc = %d, want 0; stderr: %s", rc, errOut.String())
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
