package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCheckListenRefusesNonLoopback(t *testing.T) {
	for _, bad := range []string{
		"0.0.0.0:8080", ":8080", "[::]:8080", "localhost:8080", "10.0.0.213:8080",
		"192.168.1.10:8080", "mcp.fullchaos.dev:443", "127.0.0.1", "127.0.0.1:", "", "8.8.8.8:53",
	} {
		if err := CheckListen(bad); err == nil {
			t.Errorf("CheckListen(%q) accepted a non-loopback or malformed address", bad)
		}
	}
	for _, good := range []string{"127.0.0.1:0", "127.0.0.2:18765", "[::1]:0"} {
		if err := CheckListen(good); err != nil {
			t.Errorf("CheckListen(%q): %v", good, err)
		}
	}
}

func TestStartRefusesNonLoopbackBind(t *testing.T) {
	for _, bad := range []string{"0.0.0.0:0", ":0", "[::]:0", "localhost:0"} {
		s, err := Start(Options{Listen: bad, RecordPath: filepath.Join(t.TempDir(), "r.jsonl"), MaxRequests: 1})
		if err == nil {
			s.Close()
			t.Errorf("Start(%q) listened; want refusal", bad)
		}
	}
}

// TestUpstreamIsNotConfigurable: environment variables that could steer an
// HTTP client (proxy variables, plausible names) do not change the upstream,
// and the transport ignores HTTP(S)_PROXY.
func TestUpstreamIsNotConfigurable(t *testing.T) {
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY", "ACR_MCP_URL", "UPSTREAM", "PROXY_UPSTREAM", "MCP_URL"} {
		t.Setenv(k, "http://127.0.0.1:9/")
	}
	s, err := Start(Options{Listen: "127.0.0.1:0", RecordPath: filepath.Join(t.TempDir(), "r.jsonl"), MaxRequests: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Upstream() != "https://mcp.fullchaos.dev" || Upstream != "https://mcp.fullchaos.dev" {
		t.Fatalf("upstream = %q", s.Upstream())
	}
	tr, ok := s.rp.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport %T, want *http.Transport", s.rp.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("transport honours an HTTP proxy setting; the upstream must stay the constant")
	}
	// Rewrite targets the constant host whatever the client asked for.
	in := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/mcp?x=1", nil)
	in.Host = "evil.example"
	out := in.Clone(context.Background())
	s.rp.Rewrite(&httputil.ProxyRequest{In: in, Out: out})
	if out.URL.Scheme != "https" || out.URL.Host != "mcp.fullchaos.dev" || out.Host != "mcp.fullchaos.dev" || out.URL.Path != "/" {
		t.Fatalf("rewritten to %s (Host %s)", out.URL, out.Host)
	}
}

// TestNoSourceReadsEnvironment: neither the package nor the command reads
// the environment, so no variable can select the upstream.
func TestNoSourceReadsEnvironment(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "..", "proxy")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no sources in %s (measurement did not happen): %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, bad := range []string{"os.Getenv", "os.LookupEnv", "os.Environ", "ProxyFromEnvironment"} {
				if strings.Contains(string(b), bad) {
					t.Errorf("%s uses %s", f, bad)
				}
			}
		}
	}
}

const secret = "test-secret-token-must-not-be-recorded"

// fakeUpstream answers like the hosted MCP for the handful of messages the
// tests send, and records what it received.
type fakeUpstream struct {
	mu      sync.Mutex
	hits    int
	hosts   []string
	xff     []string
	authHit []string
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.hits++
	f.hosts = append(f.hosts, r.Host)
	f.xff = append(f.xff, r.Header.Get("X-Forwarded-For"))
	f.authHit = append(f.authHit, r.Header.Get("Authorization"))
	f.mu.Unlock()
	switch {
	case strings.Contains(string(body), `"server/discover"`):
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"supportedVersions":["2026-07-28","2025-11-25","2025-06-18"],"capabilities":{},"instructions":"body-text-must-not-be-recorded"}}`)
	case strings.Contains(string(body), `"initialize"`):
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"serverInfo\":{\"name\":\"acr\",\"version\":\"body-text-must-not-be-recorded\"}}}\n\n")
	case strings.Contains(string(body), `"denied"`):
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		w.WriteHeader(http.StatusUnauthorized)
	default:
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"body-text-must-not-be-recorded"}]}}`)
	}
}

func startTest(t *testing.T, h http.Handler, max int) (*Server, string) {
	t.Helper()
	up := httptest.NewTLSServer(h)
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	rec := filepath.Join(t.TempDir(), "rec.jsonl")
	s, err := start(Options{Listen: "127.0.0.1:0", RecordPath: rec, MaxRequests: max}, u, up.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, rec
}

func post(t *testing.T, endpoint, rev, body string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("X-Custom-Header", "header-text-must-not-be-recorded")
	if rev != "" {
		req.Header.Set("MCP-Protocol-Version", rev)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func TestRecordsMethodRevisionStatusOnly(t *testing.T) {
	up := &fakeUpstream{}
	s, rec := startTest(t, up, 10)
	post(t, s.URL(), "2026-07-28", `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	post(t, s.URL(), "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"x","version":"1"}}}`)
	post(t, s.URL(), "2025-06-18", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if code := post(t, s.URL(), "2025-06-18", `{"jsonrpc":"2.0","id":3,"method":"denied"}`); code != 401 {
		t.Fatalf("denied status %d", code)
	}
	if code := post(t, strings.TrimSuffix(s.URL(), "/mcp")+"/.well-known/oauth-protected-resource", "", `{}`); code != 404 {
		t.Fatalf("other path status %d, want local 404", code)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	recs, err := ReadRecords(rec)
	if err != nil {
		t.Fatal(err)
	}
	want := []Record{
		{Seq: 1, Method: "server/discover", RequestedRevision: "2026-07-28", Revision: "2026-07-28", Status: 200},
		{Seq: 2, Method: "initialize", RequestedRevision: "2025-06-18", Revision: "2025-06-18", Status: 200},
		{Seq: 3, Method: "tools/list", RequestedRevision: "2025-06-18", Revision: "2025-06-18", Status: 200},
		{Seq: 4, Method: "denied", RequestedRevision: "2025-06-18", Revision: "", Status: 401},
	}
	if len(recs) != len(want) {
		t.Fatalf("records %+v, want %d", recs, len(want))
	}
	for i := range want {
		got := recs[i]
		got.LatencyMS = 0
		if got != want[i] {
			t.Errorf("record %d = %+v, want %+v", i, got, want[i])
		}
	}
	raw, _ := os.ReadFile(rec)
	for _, leak := range []string{secret, "Bearer", "Authorization", "header-text-must-not-be-recorded", "body-text-must-not-be-recorded", "clientInfo", "_meta"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("record file contains %q", leak)
		}
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.hits != 4 {
		t.Errorf("upstream saw %d requests, want 4 (other paths must not be forwarded)", up.hits)
	}
	for i, h := range up.hosts {
		if h == strings.TrimPrefix(strings.TrimSuffix(s.URL(), "/mcp"), "http://") {
			t.Errorf("request %d reached upstream with the proxy's Host %q", i, h)
		}
		if up.xff[i] != "" {
			t.Errorf("request %d carried X-Forwarded-For", i)
		}
		if up.authHit[i] != "Bearer "+secret {
			t.Errorf("request %d: Authorization not passed through", i)
		}
	}
}

// A server/discover answer that does not list the requested revision is not
// a negotiation at that revision.
func TestDiscoverWithoutRequestedRevisionRecordsNone(t *testing.T) {
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"supportedVersions":["2025-11-25","2025-06-18"],"capabilities":{}}}`)
	})
	s, rec := startTest(t, up, 5)
	post(t, s.URL(), "2026-07-28", `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`)
	s.Close()
	recs, _ := ReadRecords(rec)
	if len(recs) != 1 || recs[0].RequestedRevision != "2026-07-28" || recs[0].Revision != "" {
		t.Fatalf("records %+v, want requested 2026-07-28 and no negotiated revision", recs)
	}
}

func TestBudgetIsLocal429(t *testing.T) {
	up := &fakeUpstream{}
	s, rec := startTest(t, up, 1)
	post(t, s.URL(), "2025-06-18", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if code := post(t, s.URL(), "2025-06-18", `{"jsonrpc":"2.0","id":3,"method":"prompts/list"}`); code != 429 {
		t.Fatalf("over-budget status %d, want 429", code)
	}
	s.Close()
	recs, _ := ReadRecords(rec)
	if len(recs) != 2 || recs[1].Method != "prompts/list" || recs[1].Status != 429 {
		t.Fatalf("records %+v", recs)
	}
	if up.hits != 1 {
		t.Fatalf("upstream saw %d, want 1", up.hits)
	}
}

func TestRejectsNonPatternValues(t *testing.T) {
	m, r := parseRequest("POST", "2026-07-28; drop", []byte(`{"method":"x\ny Bearer abc","params":{"protocolVersion":"evil"}}`))
	if m != "(invalid method)" || r != "" {
		t.Fatalf("method %q revision %q", m, r)
	}
	m, r = parseRequest("GET", "2026-07-28", nil)
	if m != "GET" || r != "2026-07-28" {
		t.Fatalf("GET: %q %q", m, r)
	}
	m, _ = parseRequest("POST", "", []byte(`[{"jsonrpc":"2.0","method":"notifications/initialized"}]`))
	if m != "notifications/initialized" {
		t.Fatalf("batch: %q", m)
	}
}

// TestRealSDKHandshakeThroughProxy: the go-sdk v1.8.0 client and server (the
// real producers of both handshakes) talk through the proxy; the recording
// must say server/discover at 2026-07-28 and initialize at 2025-06-18.
func TestRealSDKHandshakeThroughProxy(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-acr", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "ping"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	mux := http.NewServeMux()
	mux.Handle("/", handler)

	for _, tc := range []struct{ requested, method string }{
		{"2026-07-28", "server/discover"},
		{"2025-06-18", "initialize"},
	} {
		t.Run(tc.requested, func(t *testing.T) {
			s, rec := startTest(t, mux, 20)
			client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
			cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: s.URL(), DisableStandaloneSSE: true, MaxRetries: -1}, &mcp.ClientSessionOptions{ProtocolVersion: tc.requested})
			if err != nil {
				t.Fatalf("connect through proxy: %v", err)
			}
			if _, err := cs.ListTools(context.Background(), nil); err != nil {
				t.Fatalf("tools/list: %v", err)
			}
			cs.Close()
			s.Close()
			recs, err := ReadRecords(rec)
			if err != nil || len(recs) == 0 {
				t.Fatalf("records %v err %v", recs, err)
			}
			first := recs[0]
			if first.Method != tc.method || first.Revision != tc.requested || first.Status != 200 {
				t.Fatalf("first record %+v, want %s at %s (all: %+v)", first, tc.method, tc.requested, recs)
			}
		})
	}
}
