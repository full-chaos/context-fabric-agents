// Package l1 is the liveness L1 protocol probe (CHAOS-6204). It speaks to
// the hosted MCP with the go-sdk client and checks, in one serial run:
//
//	a_discover   server/discover negotiates 2026-07-28
//	b_initialize legacy initialize at 2025-06-18 works
//	c_contract   tools/resources/prompts equal the committed snapshot
//	             (names + tool input-schema digest)
//	d_context    context_for_task on the granted repository returns a packet
//	e_unauth     an unauthenticated POST is 401 with a Bearer challenge
//
// Negotiated revisions must also equal the ones pinned in the snapshot. The
// probe never logs a header, a body or the credential; step details are
// short fixed text plus names and categories.
package l1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/full-chaos/context-fabric-agents/internal/snapshot"
	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	Leg = "l1"

	StepDiscover   = "a_discover"
	StepInitialize = "b_initialize"
	StepContract   = "c_contract"
	StepContext    = "d_context"
	StepUnauth     = "e_unauth"

	// unauthBody is the raw call shape proven by the CHAOS-6196 smoke: a
	// 2026-07-28 stateless call carries the _meta triple and an Mcp-Method
	// header equal to the body method.
	unauthBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"cfa-liveness","version":"0"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
)

// Steps is the fixed step order of a result.
var Steps = []string{StepDiscover, StepInitialize, StepContract, StepContext, StepUnauth}

// Config is one probe run. Endpoint and Slug are compiled constants in the
// command; tests point them at an in-process server.
type Config struct {
	Endpoint    string
	Token       string
	TokenName   string
	Slug        string
	Snapshot    *snapshot.Snapshot
	MaxRequests int
	MinInterval time.Duration
	RetryWait   time.Duration
	Now         func() time.Time
}

type probe struct {
	cfg    Config
	host   string
	pacer  *pacer
	authed *http.Client
	anon   *http.Client
	steps  map[string]record.Step
	res    *record.Result
}

// Run executes the probe and always returns a finalized result.
func Run(ctx context.Context, cfg Config) *record.Result {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.TokenName == "" {
		cfg.TokenName = "credential"
	}
	res := &record.Result{
		SchemaVersion: record.ResultSchema,
		Leg:           Leg,
		StartedAt:     cfg.Now().UTC().Format(time.RFC3339),
		Negotiated:    map[string]string{},
	}
	p := &probe{cfg: cfg, res: res, steps: map[string]record.Step{}}
	p.run(ctx)
	for _, id := range Steps {
		s, ok := p.steps[id]
		if !ok {
			s = record.Step{ID: id, Status: record.StatusNotRun, Detail: "step did not run"}
		}
		res.Steps = append(res.Steps, s)
	}
	if p.pacer != nil {
		res.RequestCount = p.pacer.Count()
	}
	res.FinishedAt = cfg.Now().UTC().Format(time.RFC3339)
	res.Finalize()
	return res
}

func (p *probe) set(id, status, format string, a ...any) {
	detail := fmt.Sprintf(format, a...)
	if len(detail) > 400 {
		detail = detail[:400] + "..."
	}
	if p.cfg.Token != "" {
		detail = strings.ReplaceAll(detail, p.cfg.Token, "[REDACTED]")
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
	p.pacer = newPacer(p.cfg.MaxRequests, p.cfg.MinInterval, p.cfg.RetryWait)
	p.authed = &http.Client{Transport: &transport{host: u.Host, token: p.cfg.Token, p: p.pacer, next: http.DefaultTransport}, CheckRedirect: noRedirects}
	p.anon = &http.Client{Transport: &transport{host: u.Host, p: p.pacer, next: http.DefaultTransport}, CheckRedirect: noRedirects}

	switch {
	case p.cfg.Token == "":
		for _, id := range []string{StepDiscover, StepInitialize, StepContract, StepContext} {
			p.fail(id, "%s missing: a missing secret is a red run, never a skip", p.cfg.TokenName)
		}
	case p.cfg.Snapshot == nil:
		for _, id := range []string{StepDiscover, StepInitialize, StepContract, StepContext} {
			p.fail(id, "committed snapshot not loaded")
		}
	default:
		p.authenticated(ctx)
	}
	p.unauthenticated(ctx)
}

func (p *probe) connect(ctx context.Context, revision string) (*mcp.ClientSession, string, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "cfa-liveness-l1", Version: "1"}, nil)
	t := &mcp.StreamableClientTransport{
		Endpoint:             p.cfg.Endpoint,
		HTTPClient:           p.authed,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	mark := p.pacer.mark()
	cs, err := client.Connect(ctx, t, &mcp.ClientSessionOptions{ProtocolVersion: revision})
	return cs, p.pacer.firstMethodSince(mark), err
}

// expected returns the snapshot's pinned negotiation for a requested revision.
func (p *probe) expected(requested string) (snapshot.Negotiation, bool) {
	for _, n := range p.cfg.Snapshot.Negotiations {
		if n.Requested == requested {
			return n, true
		}
	}
	return snapshot.Negotiation{}, false
}

func (p *probe) checkHandshake(id string, cs *mcp.ClientSession, first, requested, wantPath string, err error) bool {
	if err != nil {
		p.fail(id, "connect at %s failed: %v", requested, err)
		return false
	}
	ir := cs.InitializeResult()
	if ir == nil {
		p.fail(id, "no server result at %s", requested)
		return false
	}
	got := ir.ProtocolVersion
	p.res.Negotiated[requested] = got
	if ir.ServerInfo != nil && p.res.ServerVersion == "" {
		p.res.ServerVersion = ir.ServerInfo.Version
	}
	want, ok := p.expected(requested)
	var problems []string
	if first != wantPath {
		problems = append(problems, fmt.Sprintf("first method %q, want %q", first, wantPath))
	}
	if got != requested {
		problems = append(problems, fmt.Sprintf("negotiated %q, want %q", got, requested))
	}
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("snapshot pins no negotiation for %s", requested))
	case want.Negotiated != got || want.Path != wantPath:
		problems = append(problems, fmt.Sprintf("snapshot expects %s negotiated %q via %q; live gave %q via %q", requested, want.Negotiated, want.Path, got, first))
	}
	if len(problems) > 0 {
		p.fail(id, "%s", strings.Join(problems, "; "))
		return false
	}
	p.pass(id, "%s negotiated %s via %s", requested, got, first)
	return true
}

func (p *probe) authenticated(ctx context.Context) {
	latest, first, err := p.connect(ctx, snapshot.LatestRevision)
	if latest != nil {
		defer latest.Close()
	}
	p.checkHandshake(StepDiscover, latest, first, snapshot.LatestRevision, "server/discover", err)
	if err != nil || latest.InitializeResult() == nil {
		p.fail(StepContract, "no session at %s", snapshot.LatestRevision)
		p.fail(StepContext, "no session at %s", snapshot.LatestRevision)
	} else {
		p.contract(ctx, latest)
		p.context(ctx, latest)
	}

	legacy, first, err := p.connect(ctx, snapshot.LegacyRevision)
	if legacy != nil {
		defer legacy.Close()
	}
	p.checkHandshake(StepInitialize, legacy, first, snapshot.LegacyRevision, "initialize", err)
}

func (p *probe) contract(ctx context.Context, cs *mcp.ClientSession) {
	live := map[string]string{}
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			p.fail(StepContract, "tools/list: %v", err)
			return
		}
		d, err := snapshot.Digest(tool.InputSchema)
		if err != nil {
			p.fail(StepContract, "digest %s: %v", tool.Name, err)
			return
		}
		live["tool "+tool.Name] = d
	}
	caps := cs.InitializeResult().Capabilities
	if caps != nil && caps.Resources != nil {
		for r, err := range cs.Resources(ctx, nil) {
			if err != nil {
				p.fail(StepContract, "resources/list: %v", err)
				return
			}
			live["resource "+r.Name] = r.URI
		}
	}
	if caps != nil && caps.Prompts != nil {
		for pr, err := range cs.Prompts(ctx, nil) {
			if err != nil {
				p.fail(StepContract, "prompts/list: %v", err)
				return
			}
			live["prompt "+pr.Name] = ""
		}
	}
	pinned := map[string]string{}
	for _, t := range p.cfg.Snapshot.Tools {
		pinned["tool "+t.Name] = t.InputSchemaDigest
	}
	for _, r := range p.cfg.Snapshot.Resources {
		pinned["resource "+r.Name] = r.URI
	}
	for _, pr := range p.cfg.Snapshot.Prompts {
		pinned["prompt "+pr.Name] = ""
	}
	var diffs []string
	for k, v := range pinned {
		lv, ok := live[k]
		switch {
		case !ok:
			diffs = append(diffs, "missing live: "+k)
		case lv != v:
			diffs = append(diffs, "differs: "+k)
		}
	}
	for k := range live {
		if _, ok := pinned[k]; !ok {
			diffs = append(diffs, "not in snapshot: "+k)
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		p.fail(StepContract, "live contract != snapshot: %s", strings.Join(diffs, ", "))
		return
	}
	p.pass(StepContract, "%d tools, %d resources, %d prompts equal the snapshot", len(p.cfg.Snapshot.Tools), len(p.cfg.Snapshot.Resources), len(p.cfg.Snapshot.Prompts))
}

var categoryRe = regexp.MustCompile(`^([a-z_]+):`)

// toolText joins a result's text content.
func toolText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func errorCategory(res *mcp.CallToolResult) string {
	if m := categoryRe.FindStringSubmatch(strings.TrimSpace(toolText(res))); m != nil {
		return m[1]
	}
	return "unclassified"
}

func (p *probe) callContext(ctx context.Context, cs *mcp.ClientSession) (*mcp.CallToolResult, error) {
	return cs.CallTool(ctx, &mcp.CallToolParams{Name: "context_for_task", Arguments: map[string]any{
		"goal":       "Liveness check: summarize the current state of recent work in this repository.",
		"repository": map[string]any{"slug": p.cfg.Slug},
		"budget":     map[string]any{"max_items": 3},
	}})
}

func (p *probe) context(ctx context.Context, cs *mcp.ClientSession) {
	res, err := p.callContext(ctx, cs)
	if err == nil && res.IsError && errorCategory(res) == "rate_limit" && p.cfg.RetryWait > 0 {
		time.Sleep(p.cfg.RetryWait)
		res, err = p.callContext(ctx, cs)
	}
	if err != nil {
		p.fail(StepContext, "context_for_task %s: protocol error: %v", p.cfg.Slug, err)
		return
	}
	if res.IsError {
		p.fail(StepContext, "context_for_task %s: tool error category=%s", p.cfg.Slug, errorCategory(res))
		return
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		p.fail(StepContext, "context_for_task %s: structured content not encodable", p.cfg.Slug)
		return
	}
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		p.fail(StepContext, "context_for_task %s: structured content is not an object", p.cfg.Slug)
		return
	}
	packet := top
	if inner, ok := top["structured"].(map[string]any); ok {
		packet = inner
	}
	id, _ := packet["context_packet_id"].(string)
	if id == "" {
		p.fail(StepContext, "context_for_task %s: no context_packet_id in the result", p.cfg.Slug)
		return
	}
	items, _ := packet["items"].([]any)
	status, _ := packet["status"].(string)
	p.pass(StepContext, "context_for_task %s: packet returned (status=%s, items=%d)", p.cfg.Slug, status, len(items))
}

func (p *probe) unauthenticated(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.Endpoint, strings.NewReader(unauthBody))
	if err != nil {
		p.fail(StepUnauth, "build request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", snapshot.LatestRevision)
	req.Header.Set("Mcp-Method", "tools/list")
	resp, err := p.anon.Do(req)
	if err != nil {
		p.fail(StepUnauth, "unauthenticated POST: %v", err)
		return
	}
	drain(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		p.fail(StepUnauth, "unauthenticated POST returned %d, want 401", resp.StatusCode)
		return
	}
	params, err := checkChallenge(resp.Header.Values("WWW-Authenticate"), p.host)
	if err != nil {
		p.fail(StepUnauth, "401 challenge: %v", err)
		return
	}
	p.pass(StepUnauth, "401 with Bearer challenge (params=%v)", params)
}

var paramRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_-]*)\s*=\s*("([^"\\]|\\.)*"|[^\s,]+)`)

// checkChallenge accepts exactly one Bearer challenge. Bare "Bearer" is the
// pre-OAuth form; when resource_metadata is present it must point at this
// host's protected-resource metadata over https. It returns the parameter
// names only.
func checkChallenge(values []string, host string) ([]string, error) {
	if len(values) != 1 {
		return nil, fmt.Errorf("want one WWW-Authenticate header, got %d", len(values))
	}
	v := strings.TrimSpace(values[0])
	scheme, rest, _ := strings.Cut(v, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return nil, fmt.Errorf("scheme %q, want Bearer", scheme)
	}
	names := []string{}
	for _, m := range paramRe.FindAllStringSubmatch(rest, -1) {
		name := strings.ToLower(m[1])
		names = append(names, name)
		if name == "resource_metadata" {
			val := strings.Trim(m[2], `"`)
			u, err := url.Parse(val)
			if err != nil || u.Scheme != "https" || u.Host != host || !strings.HasPrefix(u.Path, "/.well-known/oauth-protected-resource") {
				return nil, fmt.Errorf("resource_metadata is not https://%s/.well-known/oauth-protected-resource...", host)
			}
		}
	}
	if strings.TrimSpace(rest) != "" && len(names) == 0 {
		return nil, fmt.Errorf("unparsable challenge parameters")
	}
	sort.Strings(names)
	return names, nil
}
