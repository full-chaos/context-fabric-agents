// Package devicelogin implements the client side of an RFC 8628 device
// authorization grant against a hosted MCP server: discover the
// authorization server from the MCP endpoint's own 401 challenge (the same
// chain liveness/internal/l3 verifies, RFC 9728 protected-resource metadata
// followed by RFC 8414 authorization-server metadata), register a public
// client if none is already known, start a device authorization, and poll
// for the token.
//
// Nothing here prints, logs, or persists a token; callers decide where the
// result goes (see cmd/login).
package devicelogin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Discovery is the authorization server this MCP endpoint names, resolved
// through the unauthenticated 401 -> protected-resource-metadata ->
// authorization-server-metadata chain.
type Discovery struct {
	Issuer                      string
	DeviceAuthorizationEndpoint string
	TokenEndpoint               string
	RegistrationEndpoint        string
}

// Client performs the discovery + device-grant HTTP calls. HTTPClient
// defaults to a 20s-timeout client with redirects disabled (matching
// liveness/internal/l3's probe client) when left nil.
type Client struct {
	HTTPClient *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type protectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

type authorizationServerMetadata struct {
	Issuer                      string `json:"issuer"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	RegistrationEndpoint        string `json:"registration_endpoint"`
}

var paramRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_-]*)\s*=\s*("([^"\\]|\\.)*"|[^\s,]+)`)

// parseChallenge accepts exactly one Bearer challenge and returns its
// parameters, lower-cased keys, unquoted values.
func parseChallenge(values []string) (map[string]string, error) {
	if len(values) != 1 {
		return nil, fmt.Errorf("want one WWW-Authenticate header, got %d", len(values))
	}
	v := strings.TrimSpace(values[0])
	scheme, rest, _ := strings.Cut(v, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return nil, fmt.Errorf("scheme %q, want Bearer", scheme)
	}
	params := map[string]string{}
	for _, m := range paramRe.FindAllStringSubmatch(rest, -1) {
		params[strings.ToLower(m[1])] = strings.Trim(m[2], `"`)
	}
	if strings.TrimSpace(rest) != "" && len(params) == 0 {
		return nil, fmt.Errorf("unparsable challenge parameters")
	}
	return params, nil
}

func drain(resp *http.Response) {
	if resp == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
}

// Discover runs the unauthenticated discovery chain against mcpURL: a 401
// with a Bearer challenge naming the protected-resource metadata document,
// that document naming an authorization server, and that server's own
// RFC 8414 metadata. It never sends a credential.
func (c *Client) Discover(ctx context.Context, mcpURL string) (*Discovery, error) {
	u, err := url.Parse(mcpURL)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return nil, fmt.Errorf("invalid --mcp-url %q", mcpURL)
	}
	client := c.httpClient()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, strings.NewReader(`{}`))
	if err != nil {
		return nil, fmt.Errorf("build discovery request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unauthenticated POST %s: %w", mcpURL, err)
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		return nil, fmt.Errorf("unauthenticated POST %s returned %d, want 401 (server may not require OAuth, or --mcp-url is wrong)", mcpURL, resp.StatusCode)
	}
	params, err := parseChallenge(resp.Header.Values("WWW-Authenticate"))
	if err != nil {
		return nil, fmt.Errorf("401 challenge: %w", err)
	}
	rmURL, ok := params["resource_metadata"]
	if !ok {
		return nil, errors.New("401 challenge has no resource_metadata parameter")
	}
	if p, err := url.Parse(rmURL); err != nil || p.Scheme != "https" || p.Host != u.Host {
		return nil, fmt.Errorf("resource_metadata %q is not an https URL on %s", rmURL, u.Host)
	}

	var prm protectedResourceMetadata
	if err := getJSON(ctx, client, rmURL, &prm); err != nil {
		return nil, fmt.Errorf("protected-resource metadata: %w", err)
	}
	if len(prm.AuthorizationServers) == 0 {
		return nil, errors.New("protected-resource metadata has no authorization_servers")
	}
	issuer := prm.AuthorizationServers[0]

	asURL := strings.TrimRight(issuer, "/") + "/.well-known/oauth-authorization-server"
	var asMeta authorizationServerMetadata
	if err := getJSON(ctx, client, asURL, &asMeta); err != nil {
		return nil, fmt.Errorf("authorization-server metadata: %w", err)
	}
	if strings.TrimSpace(asMeta.DeviceAuthorizationEndpoint) == "" {
		return nil, fmt.Errorf("authorization server %s does not advertise device_authorization_endpoint", issuer)
	}
	if strings.TrimSpace(asMeta.TokenEndpoint) == "" {
		return nil, fmt.Errorf("authorization server %s does not advertise token_endpoint", issuer)
	}
	return &Discovery{
		Issuer:                      asMeta.Issuer,
		DeviceAuthorizationEndpoint: asMeta.DeviceAuthorizationEndpoint,
		TokenEndpoint:               asMeta.TokenEndpoint,
		RegistrationEndpoint:        asMeta.RegistrationEndpoint,
	}, nil
}

func getJSON(ctx context.Context, client *http.Client, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %d, want 200", rawURL, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", rawURL, err)
	}
	return nil
}

// Register performs dynamic client registration (RFC 7591) against the
// authorization server's registration_endpoint and returns the issued
// client_id. clientName identifies the caller in the server's client list;
// it is never a secret.
func (c *Client) Register(ctx context.Context, registrationEndpoint, clientName string) (string, error) {
	if strings.TrimSpace(registrationEndpoint) == "" {
		return "", errors.New("authorization server has no registration_endpoint (no CIMD client_id was given either)")
	}
	body, err := json.Marshal(map[string]any{
		"client_name":                clientName,
		"redirect_uris":              []string{},
		"grant_types":                []string{"urn:ietf:params:oauth:grant-type:device_code"},
		"response_types":             []string{},
		"token_endpoint_auth_method": "none",
	})
	if err != nil {
		return "", fmt.Errorf("encode registration request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("build registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", registrationEndpoint, err)
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registration returned %d, want 200 or 201", resp.StatusCode)
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode registration response: %w", err)
	}
	if strings.TrimSpace(out.ClientID) == "" {
		return "", errors.New("registration response has no client_id")
	}
	return out.ClientID, nil
}

// DeviceAuthorization is a started RFC 8628 §3.2 device authorization.
type DeviceAuthorization struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               time.Duration
	Interval                time.Duration
}

// oauthErrorCode is the wire shape acr's OAuth endpoints use for every
// refusal: {"error": "<code>"[, "error_description": "..."]}.
type oauthErrorCode struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// APIError is a refusal reported by the authorization server as an OAuth
// error code (RFC 6749 §5.2 / RFC 8628 §3.5).
type APIError struct {
	Code        string
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	return e.Code
}

func retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	seconds, err := strconv.Atoi(v)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// StartDeviceAuthorization calls POST device_authorization_endpoint.
func (c *Client) StartDeviceAuthorization(ctx context.Context, endpoint, clientID, scope, resource string) (*DeviceAuthorization, error) {
	form := url.Values{"client_id": {clientID}, "resource": {resource}}
	if scope != "" {
		form.Set("scope", scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build device_authorization request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		var e oauthErrorCode
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("device_authorization returned %d", resp.StatusCode)
		}
		return nil, &APIError{Code: e.Error, Description: e.ErrorDescription, RetryAfter: retryAfter(resp)}
	}
	var out struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int64  `json:"expires_in"`
		Interval                int64  `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode device_authorization response: %w", err)
	}
	if out.DeviceCode == "" || out.UserCode == "" || out.VerificationURI == "" {
		return nil, errors.New("device_authorization response is missing device_code, user_code, or verification_uri")
	}
	interval := time.Duration(out.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &DeviceAuthorization{
		DeviceCode:              out.DeviceCode,
		UserCode:                out.UserCode,
		VerificationURI:         out.VerificationURI,
		VerificationURIComplete: out.VerificationURIComplete,
		ExpiresIn:               time.Duration(out.ExpiresIn) * time.Second,
		Interval:                interval,
	}, nil
}

// Token is an issued OAuth bearer credential.
type Token struct {
	AccessToken string
	TokenType   string
	ExpiresIn   time.Duration
	Scope       string
}

// Error codes RFC 8628 §3.5 defines for a /token device-code poll.
const (
	ErrAuthorizationPending = "authorization_pending"
	ErrSlowDown             = "slow_down"
	ErrAccessDenied         = "access_denied"
	ErrExpiredToken         = "expired_token"
)

func (c *Client) pollOnce(ctx context.Context, tokenEndpoint, deviceCode, clientID string) (*Token, error) {
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
		"client_id":   {clientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", tokenEndpoint, err)
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		var e oauthErrorCode
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("token endpoint returned %d", resp.StatusCode)
		}
		return nil, &APIError{Code: e.Error, Description: e.ErrorDescription, RetryAfter: retryAfter(resp)}
	}
	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if out.AccessToken == "" {
		return nil, errors.New("token response has no access_token")
	}
	return &Token{
		AccessToken: out.AccessToken,
		TokenType:   out.TokenType,
		ExpiresIn:   time.Duration(out.ExpiresIn) * time.Second,
		Scope:       out.Scope,
	}, nil
}

// slowDownIncrement is RFC 8628 §3.5's mandated backoff step: "the interval
// MUST be increased by 5 seconds for this and all subsequent requests". A
// package-level var (not a const) so a whitebox test can shrink it instead
// of waiting out 5 real seconds per slow_down.
var slowDownIncrement = 5 * time.Second

// PollProgress is reported once per poll attempt, before the outcome is
// known, so a caller can print "waiting..." without owning the loop.
type PollProgress struct {
	Attempt int
	Waited  time.Duration
}

// Poll implements RFC 8628 §3.4/§3.5: it polls tokenEndpoint at interval,
// doubling the wait on slow_down (per §3.5, "the interval MUST be increased
// by 5 seconds for this and all subsequent requests"), and returns the
// terminal outcome. It never polls past deadline (device_authorization's
// expires_in, applied by the caller) or past ctx's own deadline/timeout. A
// nil onProgress is fine.
func (c *Client) Poll(ctx context.Context, tokenEndpoint, deviceCode, clientID string, interval time.Duration, deadline time.Time, onProgress func(PollProgress)) (*Token, error) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	expired := func() bool { return !deadline.IsZero() && time.Now().After(deadline) }
	attempt := 0
	for {
		if expired() {
			return nil, &APIError{Code: ErrExpiredToken, Description: "device code expired before an approval was observed"}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		// Re-check right before sending: the wait above can itself cross
		// the deadline, and a request sent after expiry would be a real
		// network call the caller was told would never happen (RFC 8628's
		// "never polls past expires_in" promise, not just an internal loop
		// invariant).
		if expired() {
			return nil, &APIError{Code: ErrExpiredToken, Description: "device code expired before an approval was observed"}
		}
		attempt++
		if onProgress != nil {
			onProgress(PollProgress{Attempt: attempt, Waited: interval})
		}
		token, err := c.pollOnce(ctx, tokenEndpoint, deviceCode, clientID)
		if err == nil {
			return token, nil
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			return nil, err
		}
		switch apiErr.Code {
		case ErrAuthorizationPending:
			continue
		case ErrSlowDown:
			interval += slowDownIncrement
			if apiErr.RetryAfter > interval {
				interval = apiErr.RetryAfter
			}
			continue
		default:
			// access_denied, expired_token, invalid_grant, or anything else
			// the server names: all terminal, per RFC 8628 §3.5.
			return nil, apiErr
		}
	}
}
