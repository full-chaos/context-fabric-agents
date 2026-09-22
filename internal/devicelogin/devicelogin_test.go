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
