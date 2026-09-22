package devicelogin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAS is an in-process authorization server implementing exactly the
// discovery chain and device-grant endpoints devicelogin.Client calls.
type fakeAS struct {
	mcp *httptest.Server
	as  *httptest.Server

	// tokenSequence is popped (FIFO) once per /token call; the last entry
	// repeats once exhausted. Each entry is a function so a test can also
	// assert what the request carried.
	tokenSequence []func(w http.ResponseWriter, r *http.Request)
	tokenCalls    atomic.Int64

	registerClientID       string
	deviceCode             string
	userCode               string
	deviceInterval         int
	deviceExpiresIn        int
	noVerificationComplete bool
}

func newFakeAS(t *testing.T) *fakeAS {
	t.Helper()
	f := &fakeAS{
		registerClientID: "test-client-id",
		deviceCode:       "devicecode123",
		userCode:         "ABCD-EFGH",
		deviceInterval:   0, // tests set this per-case; Client defaults to 5s if 0 comes back
		deviceExpiresIn:  600,
	}

	mux := http.NewServeMux()
	asMux := http.NewServeMux()

	// Discover enforces https (production talks to real hosts only), so the
	// fakes run TLS with self-signed certs; the client below is built to
	// trust both of them, mirroring how a real client trusts a real CA.
	f.mcp = httptest.NewTLSServer(mux)
	f.as = httptest.NewTLSServer(asMux)
	t.Cleanup(f.mcp.Close)
	t.Cleanup(f.as.Close)

	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, f.mcp.URL))
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"resource":              f.mcp.URL + "/mcp",
			"authorization_servers": []string{f.as.URL},
		})
	})
	asMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                        f.as.URL,
			"device_authorization_endpoint": f.as.URL + "/device_authorization",
			"token_endpoint":                f.as.URL + "/token",
			"registration_endpoint":         f.as.URL + "/register",
		})
	})
	asMux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"client_id": f.registerClientID})
	})
	asMux.HandleFunc("/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.PostForm.Get("client_id") != f.registerClientID {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "invalid_client"})
			return
		}
		body := map[string]any{
			"device_code":      f.deviceCode,
			"user_code":        f.userCode,
			"verification_uri": f.as.URL + "/activate",
			"expires_in":       f.deviceExpiresIn,
			"interval":         f.deviceInterval,
		}
		if !f.noVerificationComplete {
			body["verification_uri_complete"] = f.as.URL + "/activate?user_code=" + f.userCode
		}
		writeJSON(w, body)
	})
	asMux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		n := f.tokenCalls.Add(1) - 1
		idx := int(n)
		if idx >= len(f.tokenSequence) {
			idx = len(f.tokenSequence) - 1
		}
		if idx < 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.tokenSequence[idx](w, r)
	})
	return f
}

// client returns a Client whose HTTP transport trusts both fake TLS
// servers' self-signed certificates -- nothing else about verification is
// relaxed, so a scheme/host mismatch in Discover's own checks still fails
// exactly as it would against real hosts.
func (f *fakeAS) client() *Client {
	pool := x509.NewCertPool()
	pool.AddCert(f.mcp.Certificate())
	pool.AddCert(f.as.Certificate())
	return &Client{HTTPClient: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func tokenPending(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, map[string]any{"error": "authorization_pending"})
}

func tokenSlowDown(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, map[string]any{"error": "slow_down"})
}

func tokenDenied(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, map[string]any{"error": "access_denied", "error_description": "the user denied the request"})
}

func tokenExpired(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, map[string]any{"error": "expired_token"})
}

func tokenSuccess(accessToken string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"access_token": accessToken, "token_type": "Bearer",
			"expires_in": 3600, "scope": "context:read evidence:read",
		})
	}
}

func TestDiscover_HappyPath(t *testing.T) {
	f := newFakeAS(t)
	d, err := f.client().Discover(context.Background(), f.mcp.URL+"/mcp")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if d.Issuer != f.as.URL {
		t.Errorf("Issuer = %q, want %q", d.Issuer, f.as.URL)
	}
	if d.DeviceAuthorizationEndpoint != f.as.URL+"/device_authorization" {
		t.Errorf("DeviceAuthorizationEndpoint = %q", d.DeviceAuthorizationEndpoint)
	}
	if d.TokenEndpoint != f.as.URL+"/token" {
		t.Errorf("TokenEndpoint = %q", d.TokenEndpoint)
	}
	if d.RegistrationEndpoint != f.as.URL+"/register" {
		t.Errorf("RegistrationEndpoint = %q", d.RegistrationEndpoint)
	}
}

func TestDiscover_Failures(t *testing.T) {
	cases := map[string]func(mux *http.ServeMux, base string){
		"200 instead of 401": func(mux *http.ServeMux, base string) {
			mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		},
		"401 with no WWW-Authenticate": func(mux *http.ServeMux, base string) {
			mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
		},
		"challenge missing resource_metadata": func(mux *http.ServeMux, base string) {
			mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="x"`)
				w.WriteHeader(http.StatusUnauthorized)
			})
		},
		"resource_metadata on a different host": func(mux *http.ServeMux, base string) {
			mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://evil.example/.well-known/oauth-protected-resource/mcp"`)
				w.WriteHeader(http.StatusUnauthorized)
			})
		},
		"PRM 404": func(mux *http.ServeMux, base string) {
			mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, base))
				w.WriteHeader(http.StatusUnauthorized)
			})
			mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			})
		},
		"PRM has no authorization_servers": func(mux *http.ServeMux, base string) {
			mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, base))
				w.WriteHeader(http.StatusUnauthorized)
			})
			mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, map[string]any{"resource": base + "/mcp", "authorization_servers": []string{}})
			})
		},
		"AS metadata has no device_authorization_endpoint": func(mux *http.ServeMux, base string) {
			mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, base))
				w.WriteHeader(http.StatusUnauthorized)
			})
			mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}})
			})
			mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, map[string]any{"issuer": base, "token_endpoint": base + "/token"})
			})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			srv := httptest.NewTLSServer(mux)
			t.Cleanup(srv.Close)
			setup(mux, srv.URL)
			pool := x509.NewCertPool()
			pool.AddCert(srv.Certificate())
			client := &Client{HTTPClient: &http.Client{
				Timeout:   5 * time.Second,
				Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
			}}
			_, err := client.Discover(context.Background(), srv.URL+"/mcp")
			if err == nil {
				t.Fatal("Discover: want error, got nil")
			}
		})
	}
}

// TestDiscover_RefusesHTTPMCPURL is cf-6235-r2 finding 2: Discover must
// refuse a plain-http --mcp-url outright (before any network call), never
// complete a login that would then wire a client to send the bearer token
// over cleartext.
func TestDiscover_RefusesHTTPMCPURL(t *testing.T) {
	_, err := (&Client{}).Discover(context.Background(), "http://mcp.example.test/mcp")
	if err == nil {
		t.Fatal("want an error for a plain http --mcp-url")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("err = %v, want it to name https", err)
	}
}

// TestDiscover_RefusesHTTPOnLoopbackWithoutFlag and
// TestDiscover_AllowsHTTPOnLoopbackWithFlag together pin --insecure-loopback:
// http is refused on a loopback host by default, and allowed only when the
// caller opts in.
func TestDiscover_RefusesHTTPOnLoopbackWithoutFlag(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux) // plain http, on 127.0.0.1
	t.Cleanup(srv.Close)
	_, err := (&Client{}).Discover(context.Background(), srv.URL+"/mcp")
	var apiErr *APIError
	if err == nil || errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want a plain (non-APIError) refusal naming --insecure-loopback", err)
	}
	if !strings.Contains(err.Error(), "insecure-loopback") {
		t.Errorf("err = %v, want it to name --insecure-loopback", err)
	}
}

func TestDiscover_AllowsHTTPOnLoopbackWithFlag(t *testing.T) {
	mux := http.NewServeMux()
	asMux := http.NewServeMux()
	mcpSrv := httptest.NewServer(mux)
	t.Cleanup(mcpSrv.Close)
	asSrv := httptest.NewServer(asMux)
	t.Cleanup(asSrv.Close)

	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, mcpSrv.URL))
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"resource": mcpSrv.URL + "/mcp", "authorization_servers": []string{asSrv.URL}})
	})
	asMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer": asSrv.URL, "device_authorization_endpoint": asSrv.URL + "/device_authorization",
			"token_endpoint": asSrv.URL + "/token", "registration_endpoint": asSrv.URL + "/register",
		})
	})

	d, err := (&Client{InsecureLoopback: true}).Discover(context.Background(), mcpSrv.URL+"/mcp")
	if err != nil {
		t.Fatalf("Discover with InsecureLoopback: %v", err)
	}
	if d.Issuer != asSrv.URL {
		t.Errorf("Issuer = %q, want %q", d.Issuer, asSrv.URL)
	}
}

// TestDiscover_RefusesHTTPDeviceAuthorizationEndpoint pins that a
// downgrade-to-http can't hide in the AS metadata JSON body either: the
// outer chain (401/PRM/AS metadata) is https throughout, but the
// device_authorization_endpoint it advertises is http.
func TestDiscover_RefusesHTTPDeviceAuthorizationEndpoint(t *testing.T) {
	asMux := http.NewServeMux()
	downgradedAS := httptest.NewTLSServer(asMux)
	t.Cleanup(downgradedAS.Close)
	// device_authorization_endpoint downgraded to http; token_endpoint/
	// registration_endpoint are never fetched by Discover (only parsed and
	// scheme-checked), so placeholder https URLs on the same server are
	// fine -- no handler for them is needed.
	asMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer": downgradedAS.URL, "device_authorization_endpoint": "http://downgrade.example.test/device_authorization",
			"token_endpoint": downgradedAS.URL + "/token", "registration_endpoint": downgradedAS.URL + "/register",
		})
	})

	mux := http.NewServeMux()
	mcpSrv := httptest.NewTLSServer(mux)
	t.Cleanup(mcpSrv.Close)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, mcpSrv.URL))
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"resource": mcpSrv.URL + "/mcp", "authorization_servers": []string{downgradedAS.URL}})
	})

	pool := x509.NewCertPool()
	pool.AddCert(mcpSrv.Certificate())
	pool.AddCert(downgradedAS.Certificate())
	client := &Client{HTTPClient: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}}

	_, err := client.Discover(context.Background(), mcpSrv.URL+"/mcp")
	if err == nil {
		t.Fatal("want an error when device_authorization_endpoint is http")
	}
	if !strings.Contains(err.Error(), "device_authorization_endpoint") {
		t.Errorf("err = %v, want it to name device_authorization_endpoint", err)
	}
}

// schemeChainCfg is the mutable set of URLs a schemeChain's handlers serve.
// A test overrides exactly one field to downgrade exactly one role in the
// discovery chain, keeping everything else https.
type schemeChainCfg struct {
	resourceMetadataURL     string
	resource                string
	authServers             []string
	deviceAuthEndpoint      string
	tokenEndpoint           string
	registrationEndpoint    string
	verificationURI         string
	verificationURIComplete string
}

// schemeChain is a full fake MCP+AS pair (both https) whose every
// discovered URL is individually overridable via cfg, for
// TestAllDiscoveredURLsRequireHTTPS: the single point-of-acceptance table
// test cf-6235-r3 asked for, covering every URL role at once instead of
// one bespoke test per role.
type schemeChain struct {
	mcp *httptest.Server
	as  *httptest.Server
	cfg *schemeChainCfg
}

func newSchemeChain(t *testing.T) *schemeChain {
	t.Helper()
	mux := http.NewServeMux()
	asMux := http.NewServeMux()
	mcpSrv := httptest.NewTLSServer(mux)
	t.Cleanup(mcpSrv.Close)
	asSrv := httptest.NewTLSServer(asMux)
	t.Cleanup(asSrv.Close)

	cfg := &schemeChainCfg{
		resourceMetadataURL:     mcpSrv.URL + "/.well-known/oauth-protected-resource/mcp",
		resource:                mcpSrv.URL + "/mcp",
		authServers:             []string{asSrv.URL},
		deviceAuthEndpoint:      asSrv.URL + "/device_authorization",
		tokenEndpoint:           asSrv.URL + "/token",
		registrationEndpoint:    asSrv.URL + "/register",
		verificationURI:         asSrv.URL + "/activate",
		verificationURIComplete: asSrv.URL + "/activate?user_code=ABCD",
	}
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer resource_metadata=%q", cfg.resourceMetadataURL))
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"resource": cfg.resource, "authorization_servers": cfg.authServers})
	})
	asMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer": asSrv.URL, "device_authorization_endpoint": cfg.deviceAuthEndpoint,
			"token_endpoint": cfg.tokenEndpoint, "registration_endpoint": cfg.registrationEndpoint,
		})
	})
	asMux.HandleFunc("/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"device_code": "dc123", "user_code": "ABCD-EFGH",
			"verification_uri": cfg.verificationURI, "verification_uri_complete": cfg.verificationURIComplete,
			"expires_in": 600, "interval": 1,
		})
	})
	return &schemeChain{mcp: mcpSrv, as: asSrv, cfg: cfg}
}

func (s *schemeChain) client() *Client {
	pool := x509.NewCertPool()
	pool.AddCert(s.mcp.Certificate())
	pool.AddCert(s.as.Certificate())
	return &Client{HTTPClient: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}}
}

// TestAllDiscoveredURLsRequireHTTPS is the single table test cf-6235-r3
// asked for: every URL this package accepts from the network -- not just
// the ones an earlier round happened to find missing a check -- must be
// validated at the point it is accepted. Each case downgrades exactly one
// role to a syntactically-valid, unreachable http URL and expects Discover
// (or StartDeviceAuthorization, for the two roles it validates) to refuse,
// naming that role.
func TestAllDiscoveredURLsRequireHTTPS(t *testing.T) {
	const downgrade = "http://insecure.invalid/x"

	discoverCases := []struct {
		name   string
		mutate func(*schemeChainCfg)
		want   string
	}{
		{"resource_metadata", func(c *schemeChainCfg) { c.resourceMetadataURL = downgrade }, "resource_metadata"},
		{"issuer", func(c *schemeChainCfg) { c.authServers = []string{downgrade} }, "authorization_servers"},
		{"device_authorization_endpoint", func(c *schemeChainCfg) { c.deviceAuthEndpoint = downgrade }, "device_authorization_endpoint"},
		{"token_endpoint", func(c *schemeChainCfg) { c.tokenEndpoint = downgrade }, "token_endpoint"},
		{"registration_endpoint", func(c *schemeChainCfg) { c.registrationEndpoint = downgrade }, "registration_endpoint"},
	}
	for _, tc := range discoverCases {
		t.Run(tc.name, func(t *testing.T) {
			chain := newSchemeChain(t)
			tc.mutate(chain.cfg)
			_, err := chain.client().Discover(context.Background(), chain.mcp.URL+"/mcp")
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}

	// verification_uri and verification_uri_complete arrive in the
	// device_authorization RESPONSE, so they're validated by
	// StartDeviceAuthorization, only reachable once Discover has already
	// succeeded (everything else stays https for these two cases).
	pollCases := []struct {
		name   string
		mutate func(*schemeChainCfg)
		want   string
	}{
		{"verification_uri", func(c *schemeChainCfg) { c.verificationURI = downgrade }, "verification_uri"},
		{"verification_uri_complete", func(c *schemeChainCfg) { c.verificationURIComplete = downgrade }, "verification_uri_complete"},
	}
	for _, tc := range pollCases {
		t.Run(tc.name, func(t *testing.T) {
			chain := newSchemeChain(t)
			tc.mutate(chain.cfg)
			client := chain.client()
			d, err := client.Discover(context.Background(), chain.mcp.URL+"/mcp")
			if err != nil {
				t.Fatalf("Discover (chain should still be https except %s): %v", tc.name, err)
			}
			_, err = client.StartDeviceAuthorization(context.Background(), d.DeviceAuthorizationEndpoint, "test-client", "", chain.mcp.URL+"/mcp")
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestRegister(t *testing.T) {
	f := newFakeAS(t)
	id, err := f.client().Register(context.Background(), f.as.URL+"/register", "test-client-name")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if id != f.registerClientID {
		t.Errorf("client_id = %q, want %q", id, f.registerClientID)
	}
}

func TestRegister_NoEndpoint(t *testing.T) {
	if _, err := (&Client{}).Register(context.Background(), "", "x"); err == nil {
		t.Fatal("want error for empty registration_endpoint")
	}
}

// TestRegister_RefusalParsesTheErrorCode is cf-6235-r2 finding 6: a
// registration refusal must surface the server's own OAuth error code
// (invalid_client_metadata, ...), not a generic "registration returned
// 400" -- the ticket requires "exits non-zero with the acr-api error code
// on any refusal".
func TestRegister_RefusalParsesTheErrorCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "invalid_client_metadata", "error_description": "client_name is required"})
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	client := &Client{HTTPClient: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}}

	_, err := client.Register(context.Background(), srv.URL+"/register", "test-client-name")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_client_metadata" {
		t.Fatalf("err = %v, want APIError{Code: invalid_client_metadata}", err)
	}
	if apiErr.Description != "client_name is required" {
		t.Errorf("Description = %q, want the server's error_description", apiErr.Description)
	}
}

func TestStartDeviceAuthorization(t *testing.T) {
	f := newFakeAS(t)
	f.deviceInterval = 1
	d, err := f.client().StartDeviceAuthorization(context.Background(), f.as.URL+"/device_authorization", f.registerClientID, "context:read", f.mcp.URL+"/mcp")
	if err != nil {
		t.Fatalf("StartDeviceAuthorization: %v", err)
	}
	if d.DeviceCode != f.deviceCode || d.UserCode != f.userCode {
		t.Errorf("got device_code=%q user_code=%q", d.DeviceCode, d.UserCode)
	}
	if d.VerificationURIComplete == "" {
		t.Error("want VerificationURIComplete when the server sends one")
	}
	if d.Interval != time.Second {
		t.Errorf("Interval = %v, want 1s", d.Interval)
	}
}

func TestStartDeviceAuthorization_NoVerificationURIComplete(t *testing.T) {
	f := newFakeAS(t)
	f.noVerificationComplete = true
	d, err := f.client().StartDeviceAuthorization(context.Background(), f.as.URL+"/device_authorization", f.registerClientID, "", f.mcp.URL+"/mcp")
	if err != nil {
		t.Fatalf("StartDeviceAuthorization: %v", err)
	}
	if d.VerificationURIComplete != "" {
		t.Errorf("VerificationURIComplete = %q, want empty (falls back to VerificationURI + UserCode)", d.VerificationURIComplete)
	}
	if d.VerificationURI == "" || d.UserCode == "" {
		t.Error("want VerificationURI and UserCode as the fallback")
	}
}

func TestStartDeviceAuthorization_Refused(t *testing.T) {
	f := newFakeAS(t)
	_, err := f.client().StartDeviceAuthorization(context.Background(), f.as.URL+"/device_authorization", "wrong-client", "", f.mcp.URL+"/mcp")
	var apiErr *APIError
	if err == nil {
		t.Fatal("want error for an unregistered client_id")
	}
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_client" {
		t.Errorf("err = %v, want APIError{Code: invalid_client}", err)
	}
}

func TestPoll_PendingThenSlowDownThenSuccess(t *testing.T) {
	restore := shrinkSlowDownIncrement(t)
	defer restore()
	f := newFakeAS(t)
	f.tokenSequence = []func(http.ResponseWriter, *http.Request){
		tokenPending, tokenPending, tokenSlowDown, tokenPending, tokenSuccess("test_token_test_token"),
	}
	var progressed []int
	token, err := f.client().Poll(context.Background(), f.as.URL+"/token", f.deviceCode, f.registerClientID,
		5*time.Millisecond, time.Now().Add(5*time.Second),
		func(p PollProgress) { progressed = append(progressed, p.Attempt) })
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if token.AccessToken != "test_token_test_token" {
		t.Errorf("AccessToken = %q", token.AccessToken)
	}
	if len(progressed) != 5 {
		t.Errorf("onProgress called %d times, want 5", len(progressed))
	}
	if int(f.tokenCalls.Load()) != 5 {
		t.Errorf("/token called %d times, want 5", f.tokenCalls.Load())
	}
}

func TestPoll_Denied(t *testing.T) {
	f := newFakeAS(t)
	f.tokenSequence = []func(http.ResponseWriter, *http.Request){tokenDenied}
	_, err := f.client().Poll(context.Background(), f.as.URL+"/token", f.deviceCode, f.registerClientID, time.Millisecond, time.Now().Add(time.Second), nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != ErrAccessDenied {
		t.Fatalf("err = %v, want APIError{Code: access_denied}", err)
	}
}

func TestPoll_ExpiredFromServer(t *testing.T) {
	f := newFakeAS(t)
	f.tokenSequence = []func(http.ResponseWriter, *http.Request){tokenExpired}
	_, err := f.client().Poll(context.Background(), f.as.URL+"/token", f.deviceCode, f.registerClientID, time.Millisecond, time.Now().Add(time.Second), nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != ErrExpiredToken {
		t.Fatalf("err = %v, want APIError{Code: expired_token}", err)
	}
}

func TestPoll_NeverPollsPastDeadline(t *testing.T) {
	f := newFakeAS(t)
	f.tokenSequence = []func(http.ResponseWriter, *http.Request){tokenPending}
	deadline := time.Now().Add(20 * time.Millisecond)
	start := time.Now()
	_, err := f.client().Poll(context.Background(), f.as.URL+"/token", f.deviceCode, f.registerClientID, 5*time.Millisecond, deadline, nil)
	elapsed := time.Since(start)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != ErrExpiredToken {
		t.Fatalf("err = %v, want APIError{Code: expired_token} once the deadline passes", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("Poll ran %v past a 20ms deadline; it must stop polling once the device code expires", elapsed)
	}
}

// TestPoll_NeverSendsARequestAfterDeadlineEvenWhenIntervalOverruns is
// cf-6235-r1 finding 2: when interval alone is longer than the remaining
// time to deadline, the ORIGINAL code checked the deadline only before
// waiting, then sent a real request after waking up -- by then already past
// expiry. Assert zero requests ever reach the server once the interval
// overruns the deadline, not just that Poll eventually returns expired.
func TestPoll_NeverSendsARequestAfterDeadlineEvenWhenIntervalOverruns(t *testing.T) {
	f := newFakeAS(t)
	f.tokenSequence = []func(http.ResponseWriter, *http.Request){tokenSuccess("should-never-be-requested")}
	deadline := time.Now().Add(20 * time.Millisecond)
	_, err := f.client().Poll(context.Background(), f.as.URL+"/token", f.deviceCode, f.registerClientID, 100*time.Millisecond, deadline, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != ErrExpiredToken {
		t.Fatalf("err = %v, want APIError{Code: expired_token}", err)
	}
	if calls := f.tokenCalls.Load(); calls != 0 {
		t.Errorf("/token was called %d time(s); an interval longer than the remaining deadline must never send a request at all", calls)
	}
}

func TestPoll_RespectsContextCancellation(t *testing.T) {
	f := newFakeAS(t)
	f.tokenSequence = []func(http.ResponseWriter, *http.Request){tokenPending}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err := f.client().Poll(ctx, f.as.URL+"/token", f.deviceCode, f.registerClientID, 5*time.Millisecond, time.Now().Add(time.Hour), nil)
	if err == nil {
		t.Fatal("want error when ctx is cancelled")
	}
}

func TestPoll_SlowDownBackoffGrows(t *testing.T) {
	restore := shrinkSlowDownIncrement(t)
	defer restore()
	f := newFakeAS(t)
	f.tokenSequence = []func(http.ResponseWriter, *http.Request){tokenSlowDown, tokenSlowDown, tokenSuccess("t")}
	start := time.Now()
	_, err := f.client().Poll(context.Background(), f.as.URL+"/token", f.deviceCode, f.registerClientID, 10*time.Millisecond, time.Now().Add(5*time.Second), nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	// interval starts at 10ms; each slow_down adds slowDownIncrement (shrunk
	// to 5ms by this test), so 3 sequential polls with 2 backoff steps take
	// at least 10+5+15 = 30ms.
	if elapsed < 30*time.Millisecond {
		t.Errorf("elapsed %v, want >= 30ms (interval grows by slowDownIncrement on each slow_down)", elapsed)
	}
}

// shrinkSlowDownIncrement overrides the package's RFC 8628 §3.5 backoff
// step for the duration of one test, so a test exercising slow_down does
// not have to wait out the real 5s production constant. Restores the
// original value on the returned func.
func shrinkSlowDownIncrement(t *testing.T) func() {
	t.Helper()
	orig := slowDownIncrement
	slowDownIncrement = 5 * time.Millisecond
	return func() { slowDownIncrement = orig }
}

func TestRetryAfterHeader_UsedWhenLarger(t *testing.T) {
	// slowDownIncrement shrunk so the computed interval (1ms + increment)
	// stays well under 1s: this isolates the Retry-After path -- without
	// the shrink, the production 5s increment would dominate and this test
	// would pass even if Retry-After were ignored entirely.
	restore := shrinkSlowDownIncrement(t)
	defer restore()
	f := newFakeAS(t)
	calls := 0
	f.tokenSequence = []func(http.ResponseWriter, *http.Request){
		func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Retry-After", "1")
			tokenSlowDown(w, r)
		},
		tokenSuccess("t"),
	}
	start := time.Now()
	_, err := f.client().Poll(context.Background(), f.as.URL+"/token", f.deviceCode, f.registerClientID, time.Millisecond, time.Now().Add(3*time.Second), nil)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if time.Since(start) < time.Second {
		t.Error("Retry-After: 1 should make Poll wait at least 1s before the next attempt, overriding the ~6ms computed interval")
	}
	if calls != 1 {
		t.Errorf("slow_down handler called %d times, want 1", calls)
	}
}
