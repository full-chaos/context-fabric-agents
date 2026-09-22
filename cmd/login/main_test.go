package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRun_Help(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"-h"}, &out, &errOut)
	if code != 0 {
		t.Errorf("-h exit code = %d, want 0", code)
	}
}

func TestRun_MissingClient(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{}, &out, &errOut)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "--client is required") {
		t.Errorf("stderr = %q, want a message about --client", errOut.String())
	}
}

func TestRun_UnknownClient(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--client", "not-a-real-client"}, &out, &errOut)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestRun_UnexpectedArgument(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--client", "env", "extra-arg"}, &out, &errOut)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestRun_DiscoveryFailure(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	t.Setenv("HOME", t.TempDir())
	var out, errOut bytes.Buffer
	code := run([]string{"--client", "stdout", "--mcp-url", srv.URL + "/mcp", "--timeout", "1s"}, &out, &errOut)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "discovery failed") {
		t.Errorf("stderr = %q, want a discovery failure message", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty on a discovery failure (never a half-token, never a leaked error there)", out.String())
	}
}

// newFakeMCPAndAS builds a full fake MCP + authorization server pair
// implementing the whole discovery + register + device_authorization +
// (one authorization_pending, then approve) chain, with certs trusted for
// the duration of the test. token is the access_token the /token endpoint
// eventually returns.
func newFakeMCPAndAS(t *testing.T, token string) (mcpURL string) {
	t.Helper()
	mcpMux := http.NewServeMux()
	mcpSrv := httptest.NewTLSServer(mcpMux)
	t.Cleanup(mcpSrv.Close)
	asMux := http.NewServeMux()
	asSrv := httptest.NewTLSServer(asMux)
	t.Cleanup(asSrv.Close)

	mcpMux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, mcpSrv.URL))
		w.WriteHeader(http.StatusUnauthorized)
	})
	mcpMux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, map[string]any{"resource": mcpSrv.URL + "/mcp", "authorization_servers": []string{asSrv.URL}})
	})
	asMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, map[string]any{
			"issuer": asSrv.URL, "device_authorization_endpoint": asSrv.URL + "/device_authorization",
			"token_endpoint": asSrv.URL + "/token", "registration_endpoint": asSrv.URL + "/register",
		})
	})
	asMux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		writeJSONTest(w, map[string]any{"client_id": "test-client"})
	})
	asMux.HandleFunc("/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, map[string]any{
			"device_code": "dc123", "user_code": "AAAA-BBBB",
			"verification_uri": asSrv.URL + "/activate", "verification_uri_complete": asSrv.URL + "/activate?user_code=AAAA-BBBB",
			"expires_in": 600, "interval": 1, // 1s, so this test's --timeout window sees more than one poll attempt
		})
	})
	calls := 0
	asMux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			writeJSONTest(w, map[string]any{"error": "authorization_pending"})
			return
		}
		writeJSONTest(w, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 3600, "scope": "context:read"})
	})

	// The test binary's own default HTTP transport does not trust the fake
	// TLS servers' self-signed certs; installing a pool covering both into
	// http.DefaultTransport for the duration of this test is how main's
	// unexported run() -- which builds its own *devicelogin.Client with no
	// injection point -- ends up trusting them, exactly as a real client
	// trusts a real CA outside of tests.
	t.Cleanup(trustTestCerts(t, mcpSrv, asSrv))
	return mcpSrv.URL + "/mcp"
}

// TestRun_EndToEnd_Stdout exercises the whole path against a fake
// authorization server, using --client stdout so the outcome can be
// asserted without touching a real client config: discover, register,
// start, poll through one authorization_pending, then approve.
func TestRun_EndToEnd_Stdout(t *testing.T) {
	mcpURL := newFakeMCPAndAS(t, "test_token_e2e_token")

	var out, errOut bytes.Buffer
	code := run([]string{"--client", "stdout", "--mcp-url", mcpURL, "--timeout", "8s"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "test_token_e2e_token" {
		t.Errorf("stdout = %q, want the bare token", got)
	}
	if strings.Contains(errOut.String(), "test_token_e2e_token") {
		t.Error("the token must never appear on stderr")
	}
}

// TestRun_EndToEnd_StdoutSucceedsWithoutHomeOrXDG is cf-6235-r2 finding 4:
// --client stdout writes no file at all, so it must succeed even on a
// headless/CI box with neither $HOME nor $XDG_CONFIG_HOME set -- the
// previous code resolved StateDir() unconditionally before Write, failing
// AFTER a real browser approval over a directory stdout mode never uses.
func TestRun_EndToEnd_StdoutSucceedsWithoutHomeOrXDG(t *testing.T) {
	mcpURL := newFakeMCPAndAS(t, "test_token_no_home")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	var out, errOut bytes.Buffer
	code := run([]string{"--client", "stdout", "--mcp-url", mcpURL, "--timeout", "8s"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "test_token_no_home" {
		t.Errorf("stdout = %q, want the bare token", got)
	}
}

func writeJSONTest(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func trustTestCerts(t *testing.T, servers ...*httptest.Server) func() {
	t.Helper()
	pool := x509.NewCertPool()
	for _, s := range servers {
		pool.AddCert(s.Certificate())
	}
	orig := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	return func() { http.DefaultTransport = orig }
}
