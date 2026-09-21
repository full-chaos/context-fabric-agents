package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Validate parses one artifact the way its consumer would at the syntax and
// shape level, and checks it against the expected model. It is NOT a client
// parser: the shapes come from vendor docs (see README.md), and no client
// binary runs here.
func Validate(a Artifact) error {
	switch a.Kind {
	case KindSkill:
		return validateSkill(a.Content)
	case KindCommand:
		return validateAddCommand(a)
	case KindConfig:
		switch a.Client {
		case ClaudeCode:
			return validateMCPServersJSON(a, true)
		case Cursor:
			return validateMCPServersJSON(a, false)
		case OpenCode:
			return validateOpenCode(a)
		case OpenCodeV2:
			return validateOpenCodeV2(a)
		case VSCode:
			return validateVSCode(a)
		case Codex:
			return validateCodex(a)
		}
	}
	return fmt.Errorf("%s: no validator for kind %q client %q", a.Path, a.Kind, a.Client)
}

// ---- strict JSON ----

// strictJSON decodes data into v and rejects: invalid syntax, duplicate keys
// at any depth, unknown fields, and trailing data.
func strictJSON(data string, v any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data after the JSON document")
	}
	return nil
}

func rejectDuplicateKeys(data string) error {
	dec := json.NewDecoder(strings.NewReader(data))
	if err := walkJSON(dec); err != nil {
		return err
	}
	return nil
}

func walkJSON(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := kt.(string)
			if seen[key] {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = true
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token() // closing delimiter
	return err
}

// ---- shape checks ----

func wantAuth(a Artifact) string {
	if a.Variant == Bearer {
		return "Bearer " + Expansion(a.Client)
	}
	return ""
}

func fail(a Artifact, format string, args ...any) error {
	return fmt.Errorf("%s: "+format, append([]any{a.Path}, args...)...)
}

func checkHeaders(a Artifact, headers map[string]string, present bool) error {
	want := wantAuth(a)
	if want == "" {
		if present {
			return fail(a, "oauth variant must carry no headers, got %v", headers)
		}
		return nil
	}
	if !present || len(headers) != 1 || headers["Authorization"] != want {
		return fail(a, "headers = %v, want only Authorization=%q", headers, want)
	}
	return nil
}

type mcpServer struct {
	Type    string             `json:"type"`
	URL     string             `json:"url"`
	Headers *map[string]string `json:"headers"`
}

// validateMCPServersJSON handles the Claude Code and Cursor shape:
// {"mcpServers": {"dev-health": {...}}}. Claude Code requires type "http";
// Cursor has no type key.
func validateMCPServersJSON(a Artifact, typed bool) error {
	var doc struct {
		MCPServers map[string]mcpServer `json:"mcpServers"`
	}
	if err := strictJSON(a.Content, &doc); err != nil {
		return fail(a, "%v", err)
	}
	s, ok := doc.MCPServers[ServerName]
	if !ok || len(doc.MCPServers) != 1 {
		return fail(a, "want exactly one %q entry under mcpServers, got %d", ServerName, len(doc.MCPServers))
	}
	wantType := ""
	if typed {
		wantType = "http"
	}
	if s.Type != wantType || s.URL != RemoteURL {
		return fail(a, "type/url = %q/%q, want %q/%q", s.Type, s.URL, wantType, RemoteURL)
	}
	var h map[string]string
	if s.Headers != nil {
		h = *s.Headers
	}
	return checkHeaders(a, h, s.Headers != nil)
}

func validateOpenCode(a Artifact) error {
	var doc struct {
		Schema string `json:"$schema"`
		MCP    map[string]struct {
			Type    string             `json:"type"`
			URL     string             `json:"url"`
			Enabled *bool              `json:"enabled"`
			OAuth   *json.RawMessage   `json:"oauth"`
			Headers *map[string]string `json:"headers"`
		} `json:"mcp"`
	}
	if err := strictJSON(a.Content, &doc); err != nil {
		return fail(a, "%v", err)
	}
	s, ok := doc.MCP[ServerName]
	if !ok || len(doc.MCP) != 1 {
		return fail(a, "want exactly one %q entry directly under mcp, got %d", ServerName, len(doc.MCP))
	}
	if s.Type != "remote" || s.URL != RemoteURL || s.Enabled == nil || !*s.Enabled {
		return fail(a, "type/url/enabled = %q/%q/%v, want remote/%q/true", s.Type, s.URL, s.Enabled, RemoteURL)
	}
	if err := checkOpenCodeOAuth(a, s.OAuth); err != nil {
		return err
	}
	var h map[string]string
	if s.Headers != nil {
		h = *s.Headers
	}
	return checkHeaders(a, h, s.Headers != nil)
}

func validateOpenCodeV2(a Artifact) error {
	var doc struct {
		MCP struct {
			Servers map[string]struct {
				Type     string             `json:"type"`
				URL      string             `json:"url"`
				OAuth    *json.RawMessage   `json:"oauth"`
				Protocol string             `json:"protocol"`
				Headers  *map[string]string `json:"headers"`
			} `json:"servers"`
		} `json:"mcp"`
	}
	if err := strictJSON(a.Content, &doc); err != nil {
		return fail(a, "%v", err)
	}
	s, ok := doc.MCP.Servers[ServerName]
	if !ok || len(doc.MCP.Servers) != 1 {
		return fail(a, "want exactly one %q entry under mcp.servers, got %d", ServerName, len(doc.MCP.Servers))
	}
	if s.Type != "remote" || s.URL != RemoteURL || s.Protocol != "auto" {
		return fail(a, "type/url/protocol = %q/%q/%q, want remote/%q/auto", s.Type, s.URL, s.Protocol, RemoteURL)
	}
	if err := checkOpenCodeOAuth(a, s.OAuth); err != nil {
		return err
	}
	var h map[string]string
	if s.Headers != nil {
		h = *s.Headers
	}
	return checkHeaders(a, h, s.Headers != nil)
}

// checkOpenCodeOAuth: the bearer variant switches OAuth off (`false`); the
// oauth variant switches it on with an empty settings object.
func checkOpenCodeOAuth(a Artifact, raw *json.RawMessage) error {
	if raw == nil {
		return fail(a, "missing oauth key")
	}
	got := strings.TrimSpace(string(*raw))
	want := "{}"
	if a.Variant == Bearer {
		want = "false"
	}
	if got != want {
		return fail(a, "oauth = %s, want %s", got, want)
	}
	return nil
}

func validateVSCode(a Artifact) error {
	var doc struct {
		Inputs []struct {
			Type        string `json:"type"`
			ID          string `json:"id"`
			Description string `json:"description"`
			Password    bool   `json:"password"`
		} `json:"inputs"`
		Servers map[string]mcpServer `json:"servers"`
	}
	if err := strictJSON(a.Content, &doc); err != nil {
		return fail(a, "%v", err)
	}
	s, ok := doc.Servers[ServerName]
	if !ok || len(doc.Servers) != 1 {
		return fail(a, "want exactly one %q entry under servers, got %d", ServerName, len(doc.Servers))
	}
	if s.Type != "http" || s.URL != RemoteURL {
		return fail(a, "type/url = %q/%q, want http/%q", s.Type, s.URL, RemoteURL)
	}
	if a.Variant == OAuth && len(doc.Inputs) != 0 {
		return fail(a, "oauth variant must declare no inputs")
	}
	if a.Variant == Bearer {
		if len(doc.Inputs) != 1 || doc.Inputs[0].Type != "promptString" || !doc.Inputs[0].Password || doc.Inputs[0].ID != vsCodeInputID {
			return fail(a, "bearer variant needs exactly one promptString password input %q, got %+v", vsCodeInputID, doc.Inputs)
		}
	}
	var h map[string]string
	if s.Headers != nil {
		h = *s.Headers
	}
	return checkHeaders(a, h, s.Headers != nil)
}

// ---- TOML (narrow subset) ----

var (
	tomlTable = regexp.MustCompile(`^\[([A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*)\]$`)
	tomlKey   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// parseTOMLSubset parses the only TOML the Codex configs use: comments,
// `[a.b]` tables, and `key = "string"` / `key = true|false` assignments. It
// is deliberately not a general TOML parser: any line outside the subset, a
// duplicate table, a duplicate key, or an assignment outside a table is an
// error, so a template that outgrows the subset fails loudly.
func parseTOMLSubset(data string) (map[string]map[string]any, error) {
	tables := map[string]map[string]any{}
	var cur map[string]any
	for i, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		n := i + 1
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "["):
			m := tomlTable.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("line %d: unsupported table header %q", n, line)
			}
			if _, dup := tables[m[1]]; dup {
				return nil, fmt.Errorf("line %d: duplicate table [%s]", n, m[1])
			}
			cur = map[string]any{}
			tables[m[1]] = cur
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", n)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if !tomlKey.MatchString(key) {
			return nil, fmt.Errorf("line %d: unsupported key %q", n, key)
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: assignment %q outside of any table", n, key)
		}
		if _, dup := cur[key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", n, key)
		}
		switch {
		case val == "true":
			cur[key] = true
		case val == "false":
			cur[key] = false
		case strings.HasPrefix(val, `"`):
			s, err := strconv.Unquote(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: bad string for %q: %v", n, key, err)
			}
			cur[key] = s
		default:
			return nil, fmt.Errorf("line %d: unsupported value for %q", n, key)
		}
	}
	return tables, nil
}

func validateCodex(a Artifact) error {
	tables, err := parseTOMLSubset(a.Content)
	if err != nil {
		return fail(a, "%v", err)
	}
	table := "mcp_servers." + ServerName
	t, ok := tables[table]
	if !ok || len(tables) != 1 {
		return fail(a, "want exactly one table [%s], got %d tables", table, len(tables))
	}
	want := map[string]any{"url": RemoteURL, "enabled": true}
	if a.Variant == Bearer {
		want["bearer_token_env_var"] = TokenEnvVar
	}
	if len(t) != len(want) {
		return fail(a, "keys = %v, want exactly %v", t, want)
	}
	for k, w := range want {
		if t[k] != w {
			return fail(a, "%s = %v, want %v", k, t[k], w)
		}
	}
	return nil
}

// ---- command and skill ----

func validateAddCommand(a Artifact) error {
	want := RenderClaudeCodeAddCommand(a.Variant)
	if a.Content != want {
		return fail(a, "add command differs from the canonical form %q", want)
	}
	if a.Variant == Bearer && !strings.Contains(a.Content, "'Authorization: Bearer ${"+TokenEnvVar+"}'") {
		return fail(a, "the bearer header must be single-quoted so the shell cannot expand the token")
	}
	return nil
}

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// skillNeedles are the phrases the shared skill must carry: the tool order,
// the receipts handoff, the remote scope requirement and the untrusted-data
// rule. They are ported from the acr skill contract test.
var skillNeedles = []string{
	"context_for_task", "source_evidence", "investigate_question", "investigation_result",
	"parent_result_id", "prior_*_receipts", "single `evidence_ref_id` argument",
	"repository.slug", "acr://guide/", "untrusted",
}

// validateSkill checks the Agent Skills format (https://agentskills.io/specification):
// YAML frontmatter with a `name` that matches the directory and is 1-64
// lowercase alphanumeric/hyphen characters, and a 1-1024 character
// `description`, then the required content.
func validateSkill(content string) error {
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return errors.New("skill: missing opening --- frontmatter fence")
	}
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return errors.New("skill: missing closing --- frontmatter fence")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(front, "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok || strings.TrimSpace(k) != k {
			return fmt.Errorf("skill: frontmatter line %q is not a single-line `key: value`", line)
		}
		if k != "name" && k != "description" {
			return fmt.Errorf("skill: unexpected frontmatter key %q", k)
		}
		if _, dup := fields[k]; dup {
			return fmt.Errorf("skill: duplicate frontmatter key %q", k)
		}
		fields[k] = v
	}
	name := fields["name"]
	if name != SkillName || len(name) > 64 || !skillNamePattern.MatchString(name) {
		return fmt.Errorf("skill: name = %q, want %q (must match its directory)", name, SkillName)
	}
	if d := fields["description"]; len(d) < 1 || len(d) > 1024 {
		return fmt.Errorf("skill: description length %d, want 1-1024", len(d))
	}
	for _, n := range skillNeedles {
		if !strings.Contains(body, n) {
			return fmt.Errorf("skill: body lacks %q", n)
		}
	}
	if !bytes.HasSuffix([]byte(content), []byte("\n")) {
		return errors.New("skill: missing trailing newline")
	}
	return nil
}
