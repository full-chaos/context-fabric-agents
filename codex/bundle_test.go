// Package codex holds the Codex plugin bundle. This test file checks the
// bundle files (CHAOS-6202): plugin manifest, plugin MCP entry, repo
// marketplace and the skill copy. Codex itself parses the same files in CI
// (proof/static_check.sh); these checks run under plain `go test`.
package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/full-chaos/context-fabric-agents/internal/render"
)

var (
	kebab  = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	semver = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
)

func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Skills      string `json:"skills"`
	MCPServers  string `json:"mcpServers"`
}

type mcpFile struct {
	MCPServers map[string]map[string]any `json:"mcpServers"`
}

type marketplace struct {
	Name    string `json:"name"`
	Plugins []struct {
		Name   string `json:"name"`
		Source struct {
			Source string `json:"source"`
			Path   string `json:"path"`
		} `json:"source"`
	} `json:"plugins"`
}

// problems returns every defect in one bundle, given the file bodies.
func problems(manifestJSON, mcpJSON, marketJSON, skill, rootSkill []byte) []string {
	var out []string
	var m manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return []string{"plugin.json: " + err.Error()}
	}
	if !kebab.MatchString(m.Name) {
		out = append(out, "plugin name is not kebab-case: "+m.Name)
	}
	if !semver.MatchString(m.Version) {
		out = append(out, "plugin version is not semver: "+m.Version)
	}
	if m.Description == "" {
		out = append(out, "plugin description is empty")
	}
	if m.Skills != "./skills/" {
		out = append(out, "plugin skills path = "+m.Skills+", want ./skills/")
	}
	if m.MCPServers != "./.mcp.json" {
		out = append(out, "plugin mcpServers path = "+m.MCPServers+", want ./.mcp.json")
	}

	var mc mcpFile
	if err := json.Unmarshal(mcpJSON, &mc); err != nil {
		return append(out, ".mcp.json: "+err.Error())
	}
	s, ok := mc.MCPServers[render.ServerName]
	if !ok || len(mc.MCPServers) != 1 {
		out = append(out, ".mcp.json must hold exactly one server named "+render.ServerName)
	} else {
		if s["url"] != render.RemoteURL {
			out = append(out, ".mcp.json url is not "+render.RemoteURL)
		}
		for k := range s {
			if k != "url" && k != "enabled" {
				out = append(out, ".mcp.json carries unexpected key "+k+" (OAuth default: no credential in the bundle)")
			}
		}
	}
	if strings.Contains(string(mcpJSON), "Authorization") || strings.Contains(strings.ToLower(string(mcpJSON)), "bearer") {
		out = append(out, ".mcp.json names a credential")
	}

	var mk marketplace
	if err := json.Unmarshal(marketJSON, &mk); err != nil {
		return append(out, "marketplace.json: "+err.Error())
	}
	if len(mk.Plugins) != 1 || mk.Plugins[0].Name != m.Name {
		out = append(out, "marketplace must list exactly the plugin "+m.Name)
	} else if p := mk.Plugins[0].Source; p.Source != "local" || p.Path != "./codex" {
		out = append(out, "marketplace source = "+p.Source+" "+p.Path+", want local ./codex")
	}

	if !bytes.Equal(skill, rootSkill) {
		out = append(out, "codex/skills/dev-health/SKILL.md differs from skills/dev-health/SKILL.md")
	}
	return out
}

func loadBundle(t *testing.T) (manifestJSON, mcpJSON, marketJSON, skill, rootSkill []byte) {
	t.Helper()
	return repoFile(t, "codex/.codex-plugin/plugin.json"),
		repoFile(t, "codex/.mcp.json"),
		repoFile(t, ".agents/plugins/marketplace.json"),
		repoFile(t, "codex/skills/dev-health/SKILL.md"),
		repoFile(t, "skills/dev-health/SKILL.md")
}

func TestBundleIsValid(t *testing.T) {
	a, b, c, d, e := loadBundle(t)
	for _, p := range problems(a, b, c, d, e) {
		t.Error(p)
	}
	// The marketplace path must resolve to the directory holding the manifest.
	if _, err := os.Stat(filepath.Join("..", "codex", ".codex-plugin", "plugin.json")); err != nil {
		t.Error(err)
	}
}

// TestBundleRejectsPlantedDefects plants one defect per row and requires
// problems() to report it. A row that stops failing means a guard is dead.
func TestBundleRejectsPlantedDefects(t *testing.T) {
	a, b, c, d, e := loadBundle(t)
	rep := func(src []byte, old, new string) []byte {
		if !strings.Contains(string(src), old) {
			t.Fatalf("mutation anchor not found: %q", old)
		}
		return []byte(strings.Replace(string(src), old, new, 1))
	}
	cases := []struct {
		name string
		got  []string
	}{
		{"skills path", problems(rep(a, `"./skills/"`, `"./skill/"`), b, c, d, e)},
		{"bad version", problems(rep(a, `"0.1.0"`, `"one"`), b, c, d, e)},
		{"wrong url", problems(a, rep(b, "mcp.fullchaos.dev", "example.com"), c, d, e)},
		{"planted bearer key", problems(a, rep(b, `"enabled": true`, `"enabled": true, "bearer_token_env_var": "ACR_MCP_TOKEN"`), c, d, e)},
		{"planted Authorization", problems(a, rep(b, `"enabled": true`, `"headers": {"Authorization": "x"}`), c, d, e)},
		{"marketplace path", problems(a, b, rep(c, `"./codex"`, `"./nowhere"`), d, e)},
		{"skill drift", problems(a, b, c, append(append([]byte{}, d...), '!'), e)},
		{"invalid json", problems([]byte("{"), b, c, d, e)},
	}
	for _, tc := range cases {
		if len(tc.got) == 0 {
			t.Errorf("%s: planted defect not detected", tc.name)
		}
	}
}
