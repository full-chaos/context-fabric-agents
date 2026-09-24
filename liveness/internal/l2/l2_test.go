package l2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/full-chaos/context-fabric-agents/liveness/internal/proxy"
	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

const (
	token    = "test-token-" + "never-printed-7f3a"
	proxyURL = "http://127.0.0.1:18765/mcp"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func compat() *Compat {
	return &Compat{Contract: "mcp_tools.v1", Clients: map[string]CompatEntry{
		"claude-code": {ExpectedMethod: "server/discover", ExpectedRevision: "2026-07-28", PinnedVersion: "2.1.278", Status: "confirmed"},
		"codex":       {ExpectedMethod: "initialize", ExpectedRevision: "2025-06-18", PinnedVersion: "0.155.1", Status: "confirmed"},
		"opencode-v2": {ExpectedMethod: "server/discover", ExpectedRevision: "2026-07-28", PinnedVersion: "2.0.13", Status: "confirmed"},
	}}
}

func writeRecords(t *testing.T, recs ...proxy.Record) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rec.jsonl")
	var b bytes.Buffer
	for _, r := range recs {
		line, _ := json.Marshal(r)
		b.Write(append(line, '\n'))
	}
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var (
	discover = proxy.Record{Seq: 1, Method: "server/discover", RequestedRevision: "2026-07-28", Revision: "2026-07-28", Status: 200}
	toolsNew = proxy.Record{Seq: 2, Method: "tools/list", RequestedRevision: "2026-07-28", Revision: "2026-07-28", Status: 200}
	initOld  = proxy.Record{Seq: 1, Method: "initialize", RequestedRevision: "2025-06-18", Revision: "2025-06-18", Status: 200}
	toolsOld = proxy.Record{Seq: 2, Method: "tools/list", RequestedRevision: "2025-06-18", Revision: "2025-06-18", Status: 200}
)

// fakeClient answers commands like a healthy pinned client; tests override
// single answers.
type fakeClient struct {
	answers map[string]func() (string, error)
	calls   []string
	envs    [][]string
}

func (f *fakeClient) run(_ context.Context, _ string, env []string, name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	f.envs = append(f.envs, env)
	for prefix, fn := range f.answers {
		if strings.HasPrefix(key, prefix) {
			return fn()
		}
	}
	return "", errors.New("fake: unexpected command " + key)
}

func out(s string) func() (string, error) { return func() (string, error) { return s, nil } }

func healthy() *fakeClient {
	codexOK := `{"server": "dev-health", "found": true, "toolsError": null, "serverInfo": {"name": "acr", "version": "1"}, "tools": ["context_for_task"], "resources": 3}`
	return &fakeClient{answers: map[string]func() (string, error){
		"claude --version":      out("2.1.278 (Claude Code)\n"),
		"claude mcp add-json":   out("Added http MCP server dev-health to user config\n"),
		"claude mcp list":       out("Checking MCP server health…\n\ndev-health: " + proxyURL + " (HTTP) - ✔ Connected\n"),
		"claude mcp get":        out("dev-health:\n  Scope: User config\n  Status: ✔ Connected\n  Type: http\n"),
		"codex --version":       out("codex-cli 0.155.1\n"),
		"python3 ":              out(codexOK + "\n"),
		"opencode --version":    out("opencode v2.0.13\n"),
		"opencode mcp list":     out("✓ dev-health  connected\n"),
		"opencode service stop": out(""),
	}}
}

func cfg(t *testing.T, client string, f *fakeClient, recs string) Config {
	return Config{
		Client: client, ProxyURL: proxyURL, RecordsPath: recs, RepoRoot: repoRoot(t),
		Compat: compat(), Tools: []string{"context_for_task"}, Token: token, WorkDir: t.TempDir(),
		Run: f.run, Sleep: func(time.Duration) {}, Path: "/usr/bin:/bin",
	}
}

func steps(res *record.Result) map[string]record.Step {
	m := map[string]record.Step{}
	for _, s := range res.Steps {
		m[s.ID] = s
	}
	return m
}

func mustPass(t *testing.T, res *record.Result) {
	t.Helper()
	if res.Status != record.StatusPass {
		t.Fatalf("status %s, steps %+v", res.Status, res.Steps)
	}
	// The aggregator must accept the record as written.
	data, err := res.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := record.DecodeResult(data); err != nil {
		t.Fatalf("aggregator rejects the result: %v", err)
	}
}

func mustFail(t *testing.T, res *record.Result, step, want string) {
	t.Helper()
	if res.Status == record.StatusPass {
		t.Fatalf("status pass; want %s to fail with %q", step, want)
	}
	s := steps(res)[step]
	if s.Status == record.StatusPass || !strings.Contains(s.Detail, want) {
		t.Fatalf("step %s = %+v, want non-pass containing %q (all: %+v)", step, s, want, res.Steps)
	}
}

func TestGreenPerClient(t *testing.T) {
	for client, recs := range map[string][]proxy.Record{
		"claude-code": {discover, toolsNew},
		"codex":       {initOld, toolsOld},
		"opencode-v2": {discover, toolsNew},
	} {
		t.Run(client, func(t *testing.T) {
			f := healthy()
			res := Run(context.Background(), cfg(t, client, f, writeRecords(t, recs...)))
			mustPass(t, res)
			if res.Leg != "l2-"+client || res.RequestCount != len(recs) {
				t.Fatalf("leg %s count %d", res.Leg, res.RequestCount)
			}
			if got := res.Negotiated[recs[0].RequestedRevision]; got != recs[0].Revision {
				t.Fatalf("negotiated %v", res.Negotiated)
			}
		})
	}
}

// Kill proof: the client was pointed past the proxy, so nothing was recorded.
func TestProxyRecordedNothingIsRed(t *testing.T) {
	res := Run(context.Background(), cfg(t, "claude-code", healthy(), writeRecords(t)))
	mustFail(t, res, StepRecorded, "the proxy recorded nothing")
	mustFail(t, res, StepCompat, "no recording")
	// A missing record file is red too.
	res = Run(context.Background(), cfg(t, "claude-code", healthy(), filepath.Join(t.TempDir(), "absent.jsonl")))
	mustFail(t, res, StepRecorded, "proxy records unreadable")
}

// Kill proof: compat.json expects a different revision. Down and up are both red.
func TestCompatChangeUpOrDownIsRed(t *testing.T) {
	down := proxy.Record{Seq: 1, Method: "server/discover", RequestedRevision: "2026-07-28", Revision: "2025-11-25", Status: 200}
	res := Run(context.Background(), cfg(t, "claude-code", healthy(), writeRecords(t, down, toolsNew)))
	mustFail(t, res, StepCompat, "update compat.json")

	c := cfg(t, "codex", healthy(), writeRecords(t, discover, toolsNew)) // codex silently upgraded
	res = Run(context.Background(), c)
	mustFail(t, res, StepCompat, `recorded first method "server/discover" at revision "2026-07-28"; compat.json expects "initialize" at "2025-06-18"`)

	// Same revision, other first method (the client dropped server/discover).
	initNew := proxy.Record{Seq: 1, Method: "initialize", RequestedRevision: "2026-07-28", Revision: "2026-07-28", Status: 200}
	mustFail(t, Run(context.Background(), cfg(t, "claude-code", healthy(), writeRecords(t, initNew, toolsNew))), StepCompat, `recorded first method "initialize" at revision "2026-07-28"`)

	c = cfg(t, "claude-code", healthy(), writeRecords(t, discover, toolsNew))
	e := c.Compat.Clients["claude-code"]
	e.ExpectedRevision = "2025-06-18"
	c.Compat.Clients["claude-code"] = e
	mustFail(t, Run(context.Background(), c), StepCompat, "update compat.json")

	c = cfg(t, "claude-code", healthy(), writeRecords(t, discover, toolsNew))
	e = c.Compat.Clients["claude-code"]
	e.Status = "unconfirmed"
	c.Compat.Clients["claude-code"] = e
	mustFail(t, Run(context.Background(), c), StepCompat, "update compat.json")

	c = cfg(t, "claude-code", healthy(), writeRecords(t, discover, toolsNew))
	delete(c.Compat.Clients, "claude-code")
	mustFail(t, Run(context.Background(), c), StepCompat, "update compat.json: no entry")
}

// Kill proof: a declared live client that is not installed (or not at the pin).
func TestInstallFailureIsRed(t *testing.T) {
	f := healthy()
	f.answers["claude --version"] = func() (string, error) { return "", errors.New(`exec: "claude": executable file not found in $PATH`) }
	res := Run(context.Background(), cfg(t, "claude-code", f, writeRecords(t, discover)))
	mustFail(t, res, StepInstall, "client not installed")
	mustFail(t, res, StepConnect, "not attempted")
	for _, c := range f.calls {
		if strings.HasPrefix(c, "claude mcp") {
			t.Fatalf("connect ran without an installed client: %v", f.calls)
		}
	}

	f = healthy()
	f.answers["codex --version"] = out("codex-cli 0.160.0\n")
	mustFail(t, Run(context.Background(), cfg(t, "codex", f, writeRecords(t, initOld))), StepInstall, "installed 0.160.0, lockfile pins 0.155.1")

	c := cfg(t, "opencode-v2", healthy(), writeRecords(t, discover))
	e := c.Compat.Clients["opencode-v2"]
	e.PinnedVersion = "2.0.12"
	c.Compat.Clients["opencode-v2"] = e
	mustFail(t, Run(context.Background(), c), StepInstall, "update compat.json: pinned_version")
}

func TestMissingCredentialIsRed(t *testing.T) {
	c := cfg(t, "claude-code", healthy(), writeRecords(t, discover))
	c.Token = ""
	mustFail(t, Run(context.Background(), c), StepRender, "credential missing")
}

func TestProxyURLMustBeLoopback(t *testing.T) {
	for _, bad := range []string{"https://mcp.fullchaos.dev", "http://10.0.0.1:18765/mcp", "http://localhost:18765/mcp", "http://127.0.0.1:18765/other"} {
		c := cfg(t, "claude-code", healthy(), writeRecords(t, discover))
		c.ProxyURL = bad
		mustFail(t, Run(context.Background(), c), StepRender, "")
	}
}

func TestConnectFailuresAreRed(t *testing.T) {
	f := healthy()
	f.answers["claude mcp list"] = out("dev-health: " + proxyURL + " (HTTP) - ✘ Failed to connect — HTTP 401\n")
	mustFail(t, Run(context.Background(), cfg(t, "claude-code", f, writeRecords(t, discover))), StepConnect, "claude mcp list")

	f = healthy()
	f.answers["claude mcp get"] = out("dev-health:\n  Status: ✘ Failed to connect\n")
	mustFail(t, Run(context.Background(), cfg(t, "claude-code", f, writeRecords(t, discover))), StepConnect, "claude mcp get")

	// Connected to some other URL (not the proxy) is not a pass.
	f = healthy()
	f.answers["claude mcp list"] = out("dev-health: https://mcp.fullchaos.dev (HTTP) - ✔ Connected\n")
	mustFail(t, Run(context.Background(), cfg(t, "claude-code", f, writeRecords(t, discover))), StepConnect, "claude mcp list")

	f = healthy()
	f.answers["python3 "] = func() (string, error) {
		return `{"found": true, "toolsError": "Auth required"}`, errors.New("exit status 1")
	}
	mustFail(t, Run(context.Background(), cfg(t, "codex", f, writeRecords(t, initOld))), StepConnect, "mcpServerStatus/list")

	f = healthy()
	f.answers["opencode mcp list"] = out("✗ dev-health  failed: Version negotiation failed: the server requires authorization (HTTP 401)\n")
	mustFail(t, Run(context.Background(), cfg(t, "opencode-v2", f, writeRecords(t, discover))), StepConnect, "✗ dev-health")

	f = healthy()
	f.answers["opencode mcp list"] = out("No MCP servers configured\n")
	mustFail(t, Run(context.Background(), cfg(t, "opencode-v2", f, writeRecords(t, discover))), StepConnect, "after 6 attempts")
	if !contains(f.calls, "opencode service stop") {
		t.Error("the OpenCode background service was not stopped")
	}
}

func TestOpenCodeColdServiceIsRetried(t *testing.T) {
	f := healthy()
	n := 0
	f.answers["opencode mcp list"] = func() (string, error) {
		n++
		if n == 1 {
			return "No MCP servers configured\n", nil
		}
		return "✓ dev-health  connected\n", nil
	}
	mustPass(t, Run(context.Background(), cfg(t, "opencode-v2", f, writeRecords(t, discover, toolsNew))))
	if n != 2 {
		t.Fatalf("list ran %d times, want 2", n)
	}
}

func TestRecordedFailuresAreRed(t *testing.T) {
	budget := proxy.Record{Seq: 3, Method: "prompts/list", RequestedRevision: "2026-07-28", Status: 429}
	mustFail(t, Run(context.Background(), cfg(t, "claude-code", healthy(), writeRecords(t, discover, toolsNew, budget))), StepRecorded, "answered 429")
	denied := proxy.Record{Seq: 1, Method: "server/discover", RequestedRevision: "2026-07-28", Status: 401}
	mustFail(t, Run(context.Background(), cfg(t, "claude-code", healthy(), writeRecords(t, denied))), StepRecorded, "first request server/discover answered 401")
}

// The committed bearer configs render with the URL as the only change; the
// token never reaches a file, an argument or the log, and the client runs
// with a fresh, minimal environment.
func TestRenderAndIsolation(t *testing.T) {
	var log bytes.Buffer
	f := healthy()
	f.answers["claude mcp get"] = out("dev-health:\n  Status: ✔ Connected\n  Authorization: Bearer " + token + "\n")
	c := cfg(t, "claude-code", f, writeRecords(t, discover, toolsNew))
	c.Log = &log
	res := Run(context.Background(), c)
	mustPass(t, res)
	var add string
	for _, call := range f.calls {
		if strings.HasPrefix(call, "claude mcp add-json") {
			add = call
		}
	}
	if !strings.Contains(add, `"url":"`+proxyURL+`"`) || !strings.Contains(add, `Bearer ${ACR_MCP_TOKEN}`) || strings.Contains(add, "mcp.fullchaos.dev") {
		t.Fatalf("add-json args = %s", add)
	}
	if strings.Contains(log.String(), token) || !strings.Contains(log.String(), "[REDACTED]") {
		t.Fatal("token not redacted from the client log")
	}
	data, _ := res.Marshal()
	if strings.Contains(string(data), token) {
		t.Fatal("token in the result record")
	}
	for _, env := range f.envs {
		joined := strings.Join(env, "\n")
		for _, bad := range []string{"CLAUDE_CONFIG_DIR=", "ACR_MCP_CI_BEARER=", "GITHUB_TOKEN="} {
			if strings.Contains(joined, bad) {
				t.Fatalf("client env carries %s", bad)
			}
		}
		if !strings.Contains(joined, "ACR_MCP_TOKEN="+token) || !strings.Contains(joined, "HOME="+c.WorkDir) {
			t.Fatalf("client env lacks the token or the temp HOME")
		}
	}

	// Codex: the committed config.toml, URL swapped, lands in the temp CODEX_HOME.
	f = healthy()
	var toml []byte
	f.answers["python3 "] = func() (string, error) {
		for _, kv := range f.envs[len(f.envs)-1] {
			if strings.HasPrefix(kv, "CODEX_HOME=") {
				toml, _ = os.ReadFile(filepath.Join(strings.TrimPrefix(kv, "CODEX_HOME="), "config.toml"))
			}
		}
		return `{"found": true, "toolsError": null, "serverInfo": {"name": "acr"}, "tools": ["context_for_task"]}`, nil
	}
	mustPass(t, Run(context.Background(), cfg(t, "codex", f, writeRecords(t, initOld))))
	committed, _ := os.ReadFile(filepath.Join(repoRoot(t), codexConfig))
	if want := strings.Replace(string(committed), ProdURL, proxyURL, 1); string(toml) != want {
		t.Fatalf("rendered config.toml:\n%s\nwant:\n%s", toml, want)
	}
}

func TestRenderRefusesUnexpectedConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "codex", "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, codexConfig), []byte("url = \"https://elsewhere.example/mcp\"\n"), 0o644)
	if _, err := renderURL(dir, codexConfig, proxyURL); err == nil {
		t.Fatal("a config without the prod URL rendered")
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
