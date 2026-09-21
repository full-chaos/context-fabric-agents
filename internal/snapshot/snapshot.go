// Package snapshot captures the live hosted MCP contract and compares two
// captures. The committed snapshot (contracts/acr-mcp/snapshot.json) is the
// pin: the live server is the public contract, so drift is detected by
// re-capturing it, never by reading the server's source.
package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// SchemaVersion is the acr tool contract this snapshot pins.
const SchemaVersion = "mcp_tools.v1"

// Snapshot is the pinned contract. Slices are sorted so output is stable.
type Snapshot struct {
	SchemaVersion string        `json:"schema_version"`
	CapturedAt    string        `json:"captured_at"`
	Host          string        `json:"host"`
	ServerInfo    ServerInfo    `json:"server_info"`
	Negotiations  []Negotiation `json:"negotiations"`
	Tools         []Tool        `json:"tools"`
	Resources     []Resource    `json:"resources"`
	Prompts       []Prompt      `json:"prompts"`
}

// ServerInfo is the server's self-reported identity.
type ServerInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// Negotiation records the revision the server settled on for one client path.
type Negotiation struct {
	// Requested is the revision the probe asked for.
	Requested string `json:"requested"`
	// Path is the handshake actually used: "server/discover" or "initialize".
	Path string `json:"path"`
	// Negotiated is the revision the server answered with.
	Negotiated string `json:"negotiated"`
}

// Tool pins one tool: name, description digest and the full input schema.
type Tool struct {
	Name              string `json:"name"`
	DescriptionDigest string `json:"description_digest"`
	InputSchemaDigest string `json:"input_schema_digest"`
	InputSchema       any    `json:"input_schema"`
}

// Resource pins one listed resource.
type Resource struct {
	Name string `json:"name"`
	URI  string `json:"uri"`
}

// Prompt pins one prompt and its arguments.
type Prompt struct {
	Name      string           `json:"name"`
	Arguments []PromptArgument `json:"arguments"`
}

// PromptArgument pins one prompt argument.
type PromptArgument struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// Digest returns "sha256:<hex>" over the canonical JSON of v.
func Digest(v any) (string, error) {
	b, err := canonical(v, "")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// canonical renders v as JSON with sorted object keys. Go sorts map keys, so
// a round trip through a generic value gives a stable form. indent "" means
// compact. HTML escaping is off so schemas stay readable.
func canonical(v any, indent string) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(generic); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Normalize sorts every slice so equal contracts serialize identically.
func (s *Snapshot) Normalize() {
	sort.Slice(s.Negotiations, func(i, j int) bool { return s.Negotiations[i].Requested > s.Negotiations[j].Requested })
	sort.Slice(s.Tools, func(i, j int) bool { return s.Tools[i].Name < s.Tools[j].Name })
	sort.Slice(s.Resources, func(i, j int) bool { return s.Resources[i].Name < s.Resources[j].Name })
	sort.Slice(s.Prompts, func(i, j int) bool { return s.Prompts[i].Name < s.Prompts[j].Name })
	for i := range s.Prompts {
		sort.Slice(s.Prompts[i].Arguments, func(a, b int) bool { return s.Prompts[i].Arguments[a].Name < s.Prompts[i].Arguments[b].Name })
	}
	if s.Negotiations == nil {
		s.Negotiations = []Negotiation{}
	}
	if s.Tools == nil {
		s.Tools = []Tool{}
	}
	if s.Resources == nil {
		s.Resources = []Resource{}
	}
	if s.Prompts == nil {
		s.Prompts = []Prompt{}
	}
	for i := range s.Prompts {
		if s.Prompts[i].Arguments == nil {
			s.Prompts[i].Arguments = []PromptArgument{}
		}
	}
}

// Marshal returns the canonical, indented, newline-terminated JSON form.
func (s *Snapshot) Marshal() ([]byte, error) {
	c := *s
	c.Normalize()
	b, err := canonical(&c, "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Load reads a snapshot file. A missing or unparsable file is an error.
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s.Normalize()
	return &s, nil
}
