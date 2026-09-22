package l1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/full-chaos/context-fabric-agents/internal/snapshot"
	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testToken   = "test-token-value-0123456789-abcdef"
	grantedRepo = "acme/granted"
)

type fakeOpts struct {
	unauthStatus    int
	unauthChallenge []string
	noPacketID      bool
	rateLimitOnce   bool
}

type echoIn struct {
	Text string `json:"text" jsonschema:"text to echo"`
}

// newFake runs a real go-sdk server (stateless, like prod) behind a bearer
// check with a context_for_task tool that grants one repository.
func newFake(t *testing.T, o fakeOpts) *httptest.Server {
	t.Helper()
	if o.unauthStatus == 0 {
		o.unauthStatus = http.StatusUnauthorized
	}
	if o.unauthChallenge == nil {
		o.unauthChallenge = []string{"Bearer"}
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake-acr", Version: "9.9.9"}, nil)
	schema := json.RawMessage(`{"type":"object","properties":{"goal":{"type":"string"},"repository":{"type":"object","properties":{"slug":{"type":"string"}}},"budget":{"type":"object"}},"required":["goal"]}`)
	limited := o.rateLimitOnce
	srv.AddTool(&mcp.Tool{Name: "context_for_task", Description: "ctx", InputSchema: schema},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in struct {
				Repository struct {
					Slug string `json:"slug"`
				} `json:"repository"`
			}
			_ = json.Unmarshal(req.Params.Arguments, &in)
			res := &mcp.CallToolResult{}
			if limited {
				limited = false
				res.SetError(errors.New("rate_limit: slow down"))
				return res, nil
			}
			if in.Repository.Slug != grantedRepo {
				res.SetError(errors.New("repo_forbidden: the repository is not authorized for this credential"))
				return res, nil
			}
			packet := map[string]any{"status": "ok", "items": []any{map[string]any{"id": "i1"}}}
			if !o.noPacketID {
				packet["context_packet_id"] = "cp_1"
			}
			res.StructuredContent = map[string]any{"schema_version": "mcp_context_for_task_response.v1", "structured": packet}
			res.Content = []mcp.Content{&mcp.TextContent{Text: "packet"}}
			return res, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "source_evidence", Description: "ev"},
		func(context.Context, *mcp.CallToolRequest, echoIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	srv.AddResource(&mcp.Resource{Name: "guide", URI: "acr://guide"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{}, nil
		})
	srv.AddPrompt(&mcp.Prompt{Name: "investigate", Arguments: []*mcp.PromptArgument{{Name: "question", Required: true}}},
		func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{}, nil
		})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			for _, c := range o.unauthChallenge {
				w.Header().Add("WWW-Authenticate", c)
			}
			w.WriteHeader(o.unauthStatus)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// pin captures the fake's contract the same way the drift job does.
func pin(t *testing.T, ts *httptest.Server) *snapshot.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := snapshot.Capture(ctx, ts.URL, testToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func cfg(ts *httptest.Server, s *snapshot.Snapshot) Config {
	return Config{Endpoint: ts.URL, Token: testToken, TokenName: "ACR_MCP_CI_BEARER", Slug: grantedRepo, Snapshot: s, MaxRequests: 30}
}

func run(t *testing.T, c Config) *record.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := Run(ctx, c)
	data, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := record.DecodeResult(data); err != nil {
		t.Fatalf("probe wrote a result the aggregator rejects: %v", err)
	}
	if strings.Contains(string(data), testToken) {
		t.Fatal("result leaks the credential")
	}
	return r
}

func step(t *testing.T, r *record.Result, id string) record.Step {
	t.Helper()
	for _, s := range r.Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("step %s missing", id)
	return record.Step{}
}

func wantOnlyFailing(t *testing.T, r *record.Result, failing ...string) {
	t.Helper()
	bad := map[string]bool{}
	for _, f := range failing {
		bad[f] = true
	}
	for _, s := range r.Steps {
		if bad[s.ID] == (s.Status == record.StatusPass) {
			t.Errorf("step %s = %s (%s); want failing=%v", s.ID, s.Status, s.Detail, bad[s.ID])
		}
	}
	wantStatus := record.StatusPass
	if len(failing) > 0 {
		wantStatus = record.StatusFail
	}
	if r.Status != wantStatus {
		t.Errorf("status = %s, want %s", r.Status, wantStatus)
	}
}

func TestProbeGreen(t *testing.T) {
	ts := newFake(t, fakeOpts{})
	r := run(t, cfg(ts, pin(t, ts)))
	wantOnlyFailing(t, r)
	if r.Negotiated["2026-07-28"] != "2026-07-28" || r.Negotiated["2025-06-18"] != "2025-06-18" {
		t.Errorf("negotiated = %v", r.Negotiated)
	}
	if r.ServerVersion != "9.9.9" {
		t.Errorf("server version = %q", r.ServerVersion)
	}
	if r.RequestCount < 6 || r.RequestCount > 12 {
		t.Errorf("request count = %d, want a handful", r.RequestCount)
	}
	if d := step(t, r, StepDiscover).Detail; !strings.Contains(d, "via server/discover") {
		t.Errorf("discover detail = %q", d)
	}
	if d := step(t, r, StepInitialize).Detail; !strings.Contains(d, "via initialize") {
		t.Errorf("initialize detail = %q", d)
	}
}

func TestProbeMissingTokenIsRed(t *testing.T) {
	ts := newFake(t, fakeOpts{})
	c := cfg(ts, pin(t, ts))
	c.Token = ""
	r := run(t, c)
	wantOnlyFailing(t, r, StepDiscover, StepInitialize, StepContract, StepContext)
	if d := step(t, r, StepDiscover).Detail; !strings.Contains(d, "ACR_MCP_CI_BEARER missing") {
		t.Errorf("detail = %q", d)
	}
}

func TestProbeSnapshotWithExtraToolIsRed(t *testing.T) {
	ts := newFake(t, fakeOpts{})
	s := pin(t, ts)
	s.Tools = append(s.Tools, snapshot.Tool{Name: "extra_tool", InputSchemaDigest: "sha256:00"})
	r := run(t, cfg(ts, s))
	wantOnlyFailing(t, r, StepContract)
	if d := step(t, r, StepContract).Detail; !strings.Contains(d, "missing live: tool extra_tool") {
		t.Errorf("detail = %q", d)
	}
}

func TestProbeSchemaDigestChangeIsRed(t *testing.T) {
	ts := newFake(t, fakeOpts{})
	s := pin(t, ts)
	s.Tools[0].InputSchemaDigest = "sha256:deadbeef"
	r := run(t, cfg(ts, s))
	wantOnlyFailing(t, r, StepContract)
}

func TestProbeLiveOnlyMemberIsRed(t *testing.T) {
	ts := newFake(t, fakeOpts{})
	s := pin(t, ts)
	s.Prompts = nil
	r := run(t, cfg(ts, s))
	wantOnlyFailing(t, r, StepContract)
	if d := step(t, r, StepContract).Detail; !strings.Contains(d, "not in snapshot: prompt investigate") {
		t.Errorf("detail = %q", d)
	}
}

func TestProbeWrongRevisionExpectationIsRed(t *testing.T) {
	ts := newFake(t, fakeOpts{})
	for _, tc := range []struct {
		requested, step string
	}{{"2026-07-28", StepDiscover}, {"2025-06-18", StepInitialize}} {
		s := pin(t, ts)
		for i := range s.Negotiations {
			if s.Negotiations[i].Requested == tc.requested {
				s.Negotiations[i].Negotiated = "2024-11-05"
			}
		}
		r := run(t, cfg(ts, s))
		wantOnlyFailing(t, r, tc.step)
	}
	s := pin(t, ts)
	s.Negotiations = s.Negotiations[:1]
	r := run(t, cfg(ts, s))
	wantOnlyFailing(t, r, StepInitialize)
}

func TestProbeRepoOutsideGrantIsRed(t *testing.T) {
	ts := newFake(t, fakeOpts{})
	c := cfg(ts, pin(t, ts))
	c.Slug = "acme/not-granted"
	r := run(t, c)
	wantOnlyFailing(t, r, StepContext)
	if d := step(t, r, StepContext).Detail; !strings.Contains(d, "category=repo_forbidden") {
		t.Errorf("detail = %q", d)
	}
}

func TestProbeResultWithoutPacketIsRed(t *testing.T) {
	ts := newFake(t, fakeOpts{noPacketID: true})
	r := run(t, cfg(ts, pin(t, ts)))
	wantOnlyFailing(t, r, StepContext)
}

func TestProbeRetriesOneToolRateLimit(t *testing.T) {
	ts := newFake(t, fakeOpts{rateLimitOnce: true})
	c := cfg(ts, pin(t, ts))
	c.RetryWait = time.Millisecond
	wantOnlyFailing(t, run(t, c))

	ts2 := newFake(t, fakeOpts{rateLimitOnce: true})
	c2 := cfg(ts2, pin(t, ts2))
	c2.RetryWait = 0 // no retry configured: the rate limit is a failure
	r := run(t, c2)
	wantOnlyFailing(t, r, StepContext)
}

func TestProbeUnauthNot401IsRed(t *testing.T) {
	ts := newFake(t, fakeOpts{unauthStatus: http.StatusForbidden})
	r := run(t, cfg(ts, pin(t, ts)))
	wantOnlyFailing(t, r, StepUnauth)
}

func TestProbeUnauthWrongChallengeIsRed(t *testing.T) {
	ts := newFake(t, fakeOpts{unauthChallenge: []string{`Basic realm="x"`}})
	r := run(t, cfg(ts, pin(t, ts)))
	wantOnlyFailing(t, r, StepUnauth)
}

func TestProbeBudgetIsEnforced(t *testing.T) {
	ts := newFake(t, fakeOpts{})
	c := cfg(ts, pin(t, ts))
	c.MaxRequests = 3
	r := run(t, c)
	if r.Status != record.StatusFail || r.RequestCount > 3 {
		t.Fatalf("status %s count %d: budget not enforced", r.Status, r.RequestCount)
	}
}

func TestProbePacesRequests(t *testing.T) {
	ts := newFake(t, fakeOpts{})
	c := cfg(ts, pin(t, ts))
	c.MinInterval = 40 * time.Millisecond
	start := time.Now()
	r := run(t, c)
	wantOnlyFailing(t, r)
	if min := time.Duration(r.RequestCount-1) * c.MinInterval; time.Since(start) < min {
		t.Fatalf("%d requests took %v, want at least %v", r.RequestCount, time.Since(start), min)
	}
}

func TestCheckChallenge(t *testing.T) {
	host := "mcp.example.dev"
	good := []string{
		"Bearer",
		`Bearer resource_metadata="https://mcp.example.dev/.well-known/oauth-protected-resource/mcp"`,
		`bearer realm="mcp", resource_metadata="https://mcp.example.dev/.well-known/oauth-protected-resource"`,
	}
	for _, v := range good {
		if _, err := checkChallenge([]string{v}, host); err != nil {
			t.Errorf("%q rejected: %v", v, err)
		}
	}
	bad := [][]string{
		nil,
		{"Bearer", "Bearer"},
		{`Basic realm="x"`},
		{`Bearer resource_metadata="http://mcp.example.dev/.well-known/oauth-protected-resource"`},
		{`Bearer resource_metadata="https://evil.example/.well-known/oauth-protected-resource"`},
		{`Bearer resource_metadata="https://mcp.example.dev/other"`},
		{`Bearer ???`},
	}
	for _, v := range bad {
		if _, err := checkChallenge(v, host); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestTransportRefusesOtherHosts(t *testing.T) {
	tr := &transport{host: "a.example", token: testToken, p: newPacer(5, 0, 0), next: http.DefaultTransport}
	req, _ := http.NewRequest(http.MethodPost, "https://b.example/mcp", strings.NewReader("{}"))
	if _, err := tr.RoundTrip(req); err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("err = %v", err)
	}
}
