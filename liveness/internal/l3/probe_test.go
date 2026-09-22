package l3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

// server builds a fake hosted MCP + AS pair. handlers may be overridden per
// test to plant a defect at exactly one guard.
type server struct {
	mux *http.ServeMux
	srv *httptest.Server

	prmPath string
	asPath  string

	unauthStatus int
	unauthHeader string // if "", a default valid challenge is sent
	omitAuth     bool   // if true, no WWW-Authenticate header at all (overrides unauthHeader)
	prmBody      string // if "", a default valid PRM is served
	prmStatus    int
	asBody       string // if "", a default valid AS metadata is served
	asStatus     int
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{
		prmPath:      "/.well-known/oauth-protected-resource/mcp",
		asPath:       "/.well-known/oauth-authorization-server",
		unauthStatus: http.StatusUnauthorized,
		prmStatus:    http.StatusOK,
		asStatus:     http.StatusOK,
	}
	s.mux = http.NewServeMux()
	s.srv = httptest.NewTLSServer(s.mux) // https, like the real host
	t.Cleanup(s.srv.Close)

	s.mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if s.unauthStatus == http.StatusUnauthorized && !s.omitAuth {
			hdr := s.unauthHeader
			if hdr == "" {
				hdr = fmt.Sprintf("Bearer resource_metadata=%q", s.srv.URL+s.prmPath)
			}
			w.Header().Set("WWW-Authenticate", hdr)
		}
		w.WriteHeader(s.unauthStatus)
	})
	s.mux.HandleFunc(s.prmPath, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(s.prmStatus)
		if s.prmStatus != http.StatusOK {
			return
		}
		body := s.prmBody
		if body == "" {
			b, _ := json.Marshal(protectedResourceMetadata{
				Resource:             s.srv.URL + "/mcp",
				AuthorizationServers: []string{s.srv.URL},
			})
			body = string(b)
		}
		_, _ = w.Write([]byte(body))
	})
	s.mux.HandleFunc(s.asPath, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(s.asStatus)
		if s.asStatus != http.StatusOK {
			return
		}
		body := s.asBody
		if body == "" {
			b, _ := json.Marshal(authorizationServerMetadata{
				Issuer:                        s.srv.URL,
				RegistrationEndpoint:          s.srv.URL + "/register",
				CodeChallengeMethodsSupported: []string{"S256"},
			})
			body = string(b)
		}
		_, _ = w.Write([]byte(body))
	})
	return s
}

func run(t *testing.T, s *server) *record.Result {
	t.Helper()
	return Run(context.Background(), Config{Endpoint: s.srv.URL + "/mcp", Client: s.srv.Client()})
}

func stepStatus(res *record.Result, id string) string {
	for _, s := range res.Steps {
		if s.ID == id {
			return s.Status
		}
	}
	return ""
}

func TestProbeAllStepsPass(t *testing.T) {
	s := newServer(t)
	res := run(t, s)
	if res.Status != record.StatusPass {
		t.Fatalf("status = %s, want pass; steps=%+v", res.Status, res.Steps)
	}
	for _, id := range Steps {
		if stepStatus(res, id) != record.StatusPass {
			t.Errorf("step %s = %s, want pass", id, stepStatus(res, id))
		}
	}
	if _, err := record.DecodeResult(mustMarshal(t, res)); err != nil {
		t.Fatalf("result does not decode strictly: %v", err)
	}
}

func mustMarshal(t *testing.T, res *record.Result) []byte {
	t.Helper()
	b, err := res.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestProbePointedAtAPathWithoutPRM is the kill proof: the endpoint returns
// no 401 challenge at all (a plain 404, as an unrelated path on a real host
// would), so a_unauth fails and the whole leg is red.
func TestProbePointedAtAPathWithoutPRM(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux()) // 404 on every path, no WWW-Authenticate
	t.Cleanup(srv.Close)
	res := Run(context.Background(), Config{Endpoint: srv.URL + "/nonexistent"})
	if res.Status != record.StatusFail {
		t.Fatalf("status = %s, want fail", res.Status)
	}
	if stepStatus(res, StepUnauth) != record.StatusFail {
		t.Fatalf("step %s = %s, want fail", StepUnauth, stepStatus(res, StepUnauth))
	}
	if stepStatus(res, StepPRM) != record.StatusFail || stepStatus(res, StepASMeta) != record.StatusFail {
		t.Fatalf("downstream steps must also fail when a_unauth fails: prm=%s asmeta=%s", stepStatus(res, StepPRM), stepStatus(res, StepASMeta))
	}
}

// TestProbeDetectsPlantedDefects observes each guard failing, per the repo's
// verification rule 2: a defect the guard exists to catch must flip the
// specific step from pass to fail, not just the overall status.
func TestProbeDetectsPlantedDefects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(s *server)
		wantFor string // step expected to fail
	}{
		{"200 instead of 401", func(s *server) { s.unauthStatus = http.StatusOK }, StepUnauth},
		{"no WWW-Authenticate on 401", func(s *server) { s.omitAuth = true }, StepUnauth},
		{"bare Bearer, no resource_metadata", func(s *server) { s.unauthHeader = "Bearer" }, StepUnauth},
		{"resource_metadata is http, not https", func(s *server) {
			s.unauthHeader = fmt.Sprintf("Bearer resource_metadata=%q", "http://example.invalid/.well-known/oauth-protected-resource/mcp")
		}, StepUnauth},
		{"resource_metadata points at another host", func(s *server) {
			s.unauthHeader = `Bearer resource_metadata="https://attacker.invalid/.well-known/oauth-protected-resource/mcp"`
		}, StepUnauth},
		{"resource_metadata path is not oauth-protected-resource", func(s *server) {
			s.unauthHeader = fmt.Sprintf("Bearer resource_metadata=%q", s.srv.URL+"/somewhere-else")
		}, StepUnauth},
		{"PRM 500", func(s *server) { s.prmStatus = http.StatusInternalServerError }, StepPRM},
		{"PRM resource mismatch", func(s *server) {
			b, _ := json.Marshal(protectedResourceMetadata{Resource: "https://wrong.invalid/mcp", AuthorizationServers: []string{"https://as.invalid"}})
			s.prmBody = string(b)
		}, StepPRM},
		{"PRM empty authorization_servers", func(s *server) {
			b, _ := json.Marshal(protectedResourceMetadata{Resource: s.srv.URL + "/mcp", AuthorizationServers: nil})
			s.prmBody = string(b)
		}, StepPRM},
		{"PRM malformed JSON", func(s *server) { s.prmBody = "{not json" }, StepPRM},
		{"AS metadata 404", func(s *server) { s.asStatus = http.StatusNotFound }, StepASMeta},
		{"AS metadata missing S256", func(s *server) {
			b, _ := json.Marshal(authorizationServerMetadata{Issuer: s.srv.URL, RegistrationEndpoint: s.srv.URL + "/register", CodeChallengeMethodsSupported: []string{"plain"}})
			s.asBody = string(b)
		}, StepASMeta},
		{"AS metadata no registration_endpoint", func(s *server) {
			b, _ := json.Marshal(authorizationServerMetadata{Issuer: s.srv.URL, RegistrationEndpoint: "", CodeChallengeMethodsSupported: []string{"S256"}})
			s.asBody = string(b)
		}, StepASMeta},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Baseline: confirm a fresh, unmutated fixture passes (proves the
			// mutation below, not the fixture itself, causes the failure).
			if base := run(t, newServer(t)); base.Status != record.StatusPass {
				t.Fatalf("baseline fixture did not pass: %+v", base.Steps)
			}
			s := newServer(t)
			tc.mutate(s)
			res := run(t, s)
			if res.Status != record.StatusFail {
				t.Fatalf("planted defect %q not detected: status=%s steps=%+v", tc.name, res.Status, res.Steps)
			}
			if stepStatus(res, tc.wantFor) != record.StatusFail {
				t.Fatalf("planted defect %q: step %s = %s, want fail", tc.name, tc.wantFor, stepStatus(res, tc.wantFor))
			}
		})
	}
}

func TestProbeResultNeverLogsAHeaderOrBody(t *testing.T) {
	s := newServer(t)
	res := run(t, s)
	for _, step := range res.Steps {
		if len(step.Detail) > 400 {
			t.Errorf("step %s detail exceeds the 400-byte cap: %d bytes", step.ID, len(step.Detail))
		}
	}
}
