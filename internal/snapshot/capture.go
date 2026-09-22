package snapshot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// LatestRevision is requested first; the SDK uses server/discover for it.
	LatestRevision = "2026-07-28"
	// LegacyRevision is requested second; the SDK uses initialize for it.
	LegacyRevision = "2025-06-18"
)

// ErrTokenMissing is returned when no credential is supplied. The caller
// must exit non-zero: a missing secret is a red run, never a skip.
var ErrTokenMissing = errors.New("credential missing")

// bearerTransport adds the credential to requests for one host only and
// never logs. It refuses to send it anywhere else.
type bearerTransport struct {
	host  string
	token string
	next  http.RoundTripper
}

func (b *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != b.host {
		return nil, fmt.Errorf("refusing to send credential to host %q", req.URL.Host)
	}
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// redact removes the credential from an error before it can be printed.
func redact(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), token, "[REDACTED]")
	return errors.New(msg)
}

// Capture records the contract served at endpoint. now is the capture time.
func Capture(ctx context.Context, endpoint, token string, now time.Time) (*Snapshot, error) {
	if token == "" {
		return nil, ErrTokenMissing
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("bad endpoint %q", endpoint)
	}
	httpClient := &http.Client{
		Transport: &bearerTransport{host: u.Host, token: token, next: http.DefaultTransport},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are refused")
		},
	}
	snap, err := capture(ctx, endpoint, u.Host, httpClient, now)
	return snap, redact(err, token)
}

func connect(ctx context.Context, endpoint string, hc *http.Client, revision string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "cfa-contract-snapshot", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           hc,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	return client.Connect(ctx, transport, &mcp.ClientSessionOptions{ProtocolVersion: revision})
}

func capture(ctx context.Context, endpoint, host string, hc *http.Client, now time.Time) (*Snapshot, error) {
	s := &Snapshot{SchemaVersion: SchemaVersion, Host: host}

	// Path 1: latest revision (server/discover). Holds the lists.
	latest, err := connect(ctx, endpoint, hc, LatestRevision)
	if err != nil {
		return nil, fmt.Errorf("connect at %s: %w", LatestRevision, err)
	}
	defer latest.Close()
	init1 := latest.InitializeResult()
	if init1 == nil {
		return nil, errors.New("no server result at latest revision")
	}
	if init1.ServerInfo != nil {
		s.ServerInfo = ServerInfo{Name: init1.ServerInfo.Name, Title: init1.ServerInfo.Title, Version: init1.ServerInfo.Version}
	}
	s.Negotiations = append(s.Negotiations, negotiation(LatestRevision, init1.ProtocolVersion))

	for tool, err := range latest.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		digest, err := Digest(tool.InputSchema)
		if err != nil {
			return nil, err
		}
		desc, err := Digest(tool.Description)
		if err != nil {
			return nil, err
		}
		s.Tools = append(s.Tools, Tool{Name: tool.Name, DescriptionDigest: desc, InputSchemaDigest: digest, InputSchema: tool.InputSchema})
	}
	caps := init1.Capabilities
	if caps != nil && caps.Resources != nil {
		for res, err := range latest.Resources(ctx, nil) {
			if err != nil {
				return nil, fmt.Errorf("resources/list: %w", err)
			}
			s.Resources = append(s.Resources, Resource{Name: res.Name, URI: res.URI})
		}
	}
	if caps != nil && caps.Prompts != nil {
		for p, err := range latest.Prompts(ctx, nil) {
			if err != nil {
				return nil, fmt.Errorf("prompts/list: %w", err)
			}
			sp := Prompt{Name: p.Name}
			for _, a := range p.Arguments {
				sp.Arguments = append(sp.Arguments, PromptArgument{Name: a.Name, Required: a.Required})
			}
			s.Prompts = append(s.Prompts, sp)
		}
	}
	if len(s.Tools) == 0 {
		return nil, errors.New("tools/list returned no tools; refusing to record an empty contract")
	}

	// Path 2: legacy revision (initialize).
	legacy, err := connect(ctx, endpoint, hc, LegacyRevision)
	if err != nil {
		return nil, fmt.Errorf("connect at %s: %w", LegacyRevision, err)
	}
	defer legacy.Close()
	init2 := legacy.InitializeResult()
	if init2 == nil {
		return nil, errors.New("no server result at legacy revision")
	}
	s.Negotiations = append(s.Negotiations, negotiation(LegacyRevision, init2.ProtocolVersion))

	s.CapturedAt = now.UTC().Format(time.RFC3339)
	s.Normalize()
	return s, nil
}

// negotiation derives the handshake from the revision the server answered:
// the stateless revision means server/discover worked; anything older means
// initialize ran (either as asked, or as the SDK's fallback).
func negotiation(requested, negotiated string) Negotiation {
	path := "initialize"
	if negotiated >= LatestRevision {
		path = "server/discover"
	}
	return Negotiation{Requested: requested, Path: path, Negotiated: negotiated}
}
