package snapshot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testToken = "test-token-value-0123456789-abcdef"

type echoIn struct {
	Text string `json:"text" jsonschema:"text to echo"`
}

// newServer runs a real go-sdk server (stateless, like prod) behind a bearer
// check, so Capture is exercised over the real protocol paths.
func newServer(t *testing.T, tools []string, withPrompt bool) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake-acr", Version: "9.9.9"}, nil)
	for _, name := range tools {
		mcp.AddTool(srv, &mcp.Tool{Name: name, Description: "tool " + name},
			func(context.Context, *mcp.CallToolRequest, echoIn) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{}, nil, nil
			})
	}
	srv.AddResource(&mcp.Resource{Name: "guide", URI: "acr://guide"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{}, nil
		})
	if withPrompt {
		srv.AddPrompt(&mcp.Prompt{Name: "investigate", Arguments: []*mcp.PromptArgument{{Name: "question", Required: true}, {Name: "window"}}},
			func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{}, nil
			})
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestCaptureRecordsBothPaths(t *testing.T) {
	ts := newServer(t, []string{"alpha", "beta"}, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Capture(ctx, ts.URL, testToken, time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if s.SchemaVersion != "mcp_tools.v1" {
		t.Errorf("schema_version = %q", s.SchemaVersion)
	}
	if s.ServerInfo.Name != "fake-acr" || s.ServerInfo.Version != "9.9.9" {
		t.Errorf("server info = %+v", s.ServerInfo)
	}
	if s.CapturedAt != "2026-09-21T00:00:00Z" {
		t.Errorf("captured_at = %q", s.CapturedAt)
	}
	want := map[string]Negotiation{
		"2026-07-28": {"2026-07-28", "server/discover", "2026-07-28"},
		"2025-06-18": {"2025-06-18", "initialize", "2025-06-18"},
	}
	if len(s.Negotiations) != 2 {
		t.Fatalf("negotiations = %+v", s.Negotiations)
	}
	for _, n := range s.Negotiations {
		if want[n.Requested] != n {
			t.Errorf("negotiation %+v, want %+v", n, want[n.Requested])
		}
	}
	if len(s.Tools) != 2 || s.Tools[0].Name != "alpha" || s.Tools[1].Name != "beta" {
		t.Fatalf("tools = %+v", s.Tools)
	}
	for _, tl := range s.Tools {
		d, _ := Digest(tl.InputSchema)
		if d != tl.InputSchemaDigest || !strings.HasPrefix(d, "sha256:") {
			t.Errorf("tool %s digest %q does not match its schema (%q)", tl.Name, tl.InputSchemaDigest, d)
		}
	}
	if len(s.Resources) != 1 || s.Resources[0].Name != "guide" {
		t.Errorf("resources = %+v", s.Resources)
	}
	if len(s.Prompts) != 1 || len(s.Prompts[0].Arguments) != 2 {
		t.Fatalf("prompts = %+v", s.Prompts)
	}
	if q, w := s.Prompts[0].Arguments[0], s.Prompts[0].Arguments[1]; q.Name != "question" || !q.Required || w.Name != "window" || w.Required {
		t.Errorf("prompt arguments = %+v", s.Prompts[0].Arguments)
	}
}

func TestCaptureMissingTokenIsError(t *testing.T) {
	_, err := Capture(context.Background(), "http://127.0.0.1:1/mcp", "", time.Now())
	if !errors.Is(err, ErrTokenMissing) {
		t.Fatalf("err = %v, want ErrTokenMissing", err)
	}
}

func TestCaptureWrongTokenFailsWithoutLeakingToken(t *testing.T) {
	ts := newServer(t, []string{"alpha"}, false)
	wrong := "wrong-token-value-0123456789-zzzzzz"
	_, err := Capture(context.Background(), ts.URL, wrong, time.Now())
	if err == nil {
		t.Fatal("wrong token must fail")
	}
	if strings.Contains(err.Error(), wrong) {
		t.Fatalf("error leaks the credential: %v", err)
	}
}

func TestRedactRemovesToken(t *testing.T) {
	got := redact(errors.New("boom "+testToken+" tail"), testToken)
	if strings.Contains(got.Error(), testToken) || !strings.Contains(got.Error(), "[REDACTED]") {
		t.Fatalf("redact = %q", got)
	}
}

func TestBearerNeverSentToOtherHost(t *testing.T) {
	called := false
	bt := &bearerTransport{host: "good.example", token: testToken, next: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})}
	req, _ := http.NewRequest("POST", "https://evil.example/mcp", nil)
	if _, err := bt.RoundTrip(req); err == nil || called {
		t.Fatalf("credential went to another host (err=%v called=%v)", err, called)
	}
	if strings.Contains(func() string { _, e := bt.RoundTrip(req); return e.Error() }(), testToken) {
		t.Fatal("refusal message leaks the credential")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRedirectsRefused(t *testing.T) {
	target := newServer(t, []string{"alpha"}, false)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	if _, err := Capture(context.Background(), redir.URL, testToken, time.Now()); err == nil {
		t.Fatal("redirect must not be followed")
	}
}

// Failing-first pair 2 at the library seam: a committed snapshot that lacks
// one live tool is reported as drift.
func TestDroppedToolIsDetectedAgainstLiveServer(t *testing.T) {
	ts := newServer(t, []string{"alpha", "beta"}, true)
	live, err := Capture(context.Background(), ts.URL, testToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	committed, err := Capture(context.Background(), ts.URL, testToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep := Compare(committed, live); rep.Drifted() {
		t.Fatalf("identical captures drifted: %s", rep.Markdown())
	}
	committed.Tools = committed.Tools[:1] // the planted defect: drop beta
	rep := Compare(committed, live)
	if !rep.Drifted() || rep.Severity() != Minor {
		t.Fatalf("dropped tool in committed snapshot (live has it): severity %v", rep.Severity())
	}
	// And the reverse: live lost a tool the snapshot pins.
	live.Tools = live.Tools[:1]
	full, _ := Capture(context.Background(), ts.URL, testToken, time.Now())
	if rep := Compare(full, live); rep.Severity() != Major {
		t.Fatalf("live lost a tool: severity %v, want major", rep.Severity())
	}
}
