// Package l3 is the liveness L3 OAuth discovery chain probe (CHAOS-6208). It
// never authenticates and never registers a dynamic client: the whole chain
// runs unauthenticated, in one serial run against the hosted MCP endpoint:
//
//	a_unauth an unauthenticated POST is 401 with a single Bearer challenge
//	         carrying resource_metadata, an https URL on this host under
//	         /.well-known/oauth-protected-resource
//	b_prm    that URL returns 200; resource equals the probed endpoint;
//	         authorization_servers is non-empty
//	c_asmeta the first authorization server's metadata (RFC 8414, at
//	         <issuer>/.well-known/oauth-authorization-server) returns 200;
//	         code_challenge_methods_supported contains S256;
//	         registration_endpoint is present (dynamic client registration
//	         advertised; CIMD, CHAOS-6192, is not live yet)
//
// The daily dynamic-client-registration proof (an actual POST /register)
// waits on CHAOS-6191 (idle client purge): it is a separate declared-off
// leg in liveness/legs.json, never folded into this probe's required steps.
//
// The probe never logs a header, a body or a credential (none is used).
package l3

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

const (
	Leg = "l3"

	StepUnauth = "a_unauth"
	StepPRM    = "b_prm"
	StepASMeta = "c_asmeta"
)

// Steps is the fixed step order of a result.
var Steps = []string{StepUnauth, StepPRM, StepASMeta}

// Config is one probe run. Endpoint is a compiled constant in the command;
// tests point it at an in-process server.
type Config struct {
	Endpoint string
	Client   *http.Client
	Now      func() time.Time
}

type protectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

type authorizationServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

type probe struct {
	cfg    Config
	client *http.Client
	host   string
	steps  map[string]record.Step
	res    *record.Result

	resourceMetadataURL string
	prm                 *protectedResourceMetadata
}

func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Run executes the probe and always returns a finalized result.
func Run(ctx context.Context, cfg Config) *record.Result {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: noRedirects}
	}
	res := &record.Result{
		SchemaVersion: record.ResultSchema,
		Leg:           Leg,
		StartedAt:     cfg.Now().UTC().Format(time.RFC3339),
		Negotiated:    map[string]string{},
	}
	p := &probe{cfg: cfg, client: cfg.Client, res: res, steps: map[string]record.Step{}}
	p.run(ctx)
	for _, id := range Steps {
		s, ok := p.steps[id]
		if !ok {
			s = record.Step{ID: id, Status: record.StatusNotRun, Detail: "step did not run"}
		}
		res.Steps = append(res.Steps, s)
	}
	res.RequestCount = len(p.steps)
	res.FinishedAt = cfg.Now().UTC().Format(time.RFC3339)
	res.Finalize()
	return res
}

func (p *probe) set(id, status, format string, a ...any) {
	detail := fmt.Sprintf(format, a...)
	if len(detail) > 400 {
		detail = detail[:400] + "..."
	}
	p.steps[id] = record.Step{ID: id, Status: status, Detail: detail}
}

func (p *probe) pass(id, format string, a ...any) { p.set(id, record.StatusPass, format, a...) }
func (p *probe) fail(id, format string, a ...any) { p.set(id, record.StatusFail, format, a...) }

func (p *probe) run(ctx context.Context) {
	u, err := url.Parse(p.cfg.Endpoint)
	if err != nil || u.Host == "" {
		for _, id := range Steps {
			p.fail(id, "bad endpoint")
		}
		return
	}
	p.host = u.Host
	p.res.Host = u.Host

	p.unauthenticated(ctx)
	p.fetchPRM(ctx)
	p.fetchASMetadata(ctx)
}

func drain(resp *http.Response) {
	if resp == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
}

var paramRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_-]*)\s*=\s*("([^"\\]|\\.)*"|[^\s,]+)`)

// parseChallenge accepts exactly one Bearer challenge and returns its
// parameters, lower-cased, values unquoted.
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

// unauthenticated is step a_unauth: an unauthenticated POST must be 401 with
// a Bearer challenge whose resource_metadata is this host's protected-
// resource metadata document, served over https.
func (p *probe) unauthenticated(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.Endpoint, strings.NewReader(`{}`))
	if err != nil {
		p.fail(StepUnauth, "build request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := p.client.Do(req)
	if err != nil {
		p.fail(StepUnauth, "unauthenticated POST: %v", err)
		return
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		p.fail(StepUnauth, "unauthenticated POST returned %d, want 401", resp.StatusCode)
		return
	}
	params, err := parseChallenge(resp.Header.Values("WWW-Authenticate"))
	if err != nil {
		p.fail(StepUnauth, "401 challenge: %v", err)
		return
	}
	rm, ok := params["resource_metadata"]
	if !ok {
		p.fail(StepUnauth, "401 challenge has no resource_metadata parameter")
		return
	}
	u, err := url.Parse(rm)
	if err != nil || u.Scheme != "https" || u.Host != p.host || !strings.HasPrefix(u.Path, "/.well-known/oauth-protected-resource") {
		p.fail(StepUnauth, "resource_metadata %q is not https://%s/.well-known/oauth-protected-resource...", rm, p.host)
		return
	}
	p.resourceMetadataURL = rm
	p.pass(StepUnauth, "401 with Bearer challenge, resource_metadata=%s", rm)
}

// fetchPRM is step b_prm: the protected-resource metadata document.
func (p *probe) fetchPRM(ctx context.Context) {
	if p.resourceMetadataURL == "" {
		p.fail(StepPRM, "no resource_metadata URL from step %s", StepUnauth)
		return
	}
	var meta protectedResourceMetadata
	if err := p.getJSON(ctx, p.resourceMetadataURL, &meta); err != nil {
		p.fail(StepPRM, "%v", err)
		return
	}
	var problems []string
	if meta.Resource != p.cfg.Endpoint {
		problems = append(problems, fmt.Sprintf("resource %q, want %q", meta.Resource, p.cfg.Endpoint))
	}
	if len(meta.AuthorizationServers) == 0 {
		problems = append(problems, "authorization_servers is empty")
	}
	if len(problems) > 0 {
		p.fail(StepPRM, "%s", strings.Join(problems, "; "))
		return
	}
	p.prm = &meta
	p.pass(StepPRM, "resource=%s, %d authorization_servers", meta.Resource, len(meta.AuthorizationServers))
}

// fetchASMetadata is step c_asmeta: the first authorization server's
// metadata document (RFC 8414 well-known suffix on the issuer).
func (p *probe) fetchASMetadata(ctx context.Context) {
	if p.prm == nil {
		p.fail(StepASMeta, "no protected-resource metadata from step %s", StepPRM)
		return
	}
	issuer := p.prm.AuthorizationServers[0]
	asURL := strings.TrimRight(issuer, "/") + "/.well-known/oauth-authorization-server"
	var meta authorizationServerMetadata
	if err := p.getJSON(ctx, asURL, &meta); err != nil {
		p.fail(StepASMeta, "%v", err)
		return
	}
	var problems []string
	if !contains(meta.CodeChallengeMethodsSupported, "S256") {
		problems = append(problems, fmt.Sprintf("code_challenge_methods_supported %v does not contain S256", meta.CodeChallengeMethodsSupported))
	}
	if strings.TrimSpace(meta.RegistrationEndpoint) == "" {
		problems = append(problems, "registration_endpoint is absent (no DCR, and CIMD is not live yet: CHAOS-6192)")
	}
	if len(problems) > 0 {
		p.fail(StepASMeta, "%s", strings.Join(problems, "; "))
		return
	}
	p.pass(StepASMeta, "issuer=%s, S256 supported, registration_endpoint=%s", meta.Issuer, meta.RegistrationEndpoint)
}

func (p *probe) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("GET: %w", err)
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET returned %d, want 200", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
