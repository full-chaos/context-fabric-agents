package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Schema source (read 2026-09-21):
//   https://code.claude.com/docs/en/plugin-marketplaces  (marketplace.json)
//   https://code.claude.com/docs/en/plugins-reference    (plugin.json)

const (
	marketplacePath = ".claude-plugin/marketplace.json"
	pluginDir       = "plugins/dev-health"
	pluginJSONPath  = pluginDir + "/.claude-plugin/plugin.json"
	pluginWorkflow  = ".github/workflows/claude-plugin.yml"
	claudePinPath   = "ci/claude-code/package.json"
)

var semverRe = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

type marketplaceDoc struct {
	Name  string `json:"name"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
	Plugins []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	} `json:"plugins"`
}

// marketplaceProblems checks one marketplace.json body against the shape the
// docs require and this repo's contract (one plugin, relative source).
func marketplaceProblems(body string, pluginExists func(rel string) bool) []string {
	var d marketplaceDoc
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		return []string{"marketplace.json: " + err.Error()}
	}
	var out []string
	if d.Name != ServerName {
		out = append(out, "marketplace name = "+d.Name+", want "+ServerName)
	}
	if d.Owner.Name == "" {
		out = append(out, "marketplace owner.name is empty")
	}
	if len(d.Plugins) != 1 {
		return append(out, "marketplace must list exactly one plugin")
	}
	p := d.Plugins[0]
	if p.Name != ServerName {
		out = append(out, "plugin name = "+p.Name+", want "+ServerName)
	}
	if p.Source != "./"+pluginDir {
		out = append(out, "plugin source = "+p.Source+", want ./"+pluginDir)
	}
	if !strings.HasPrefix(p.Source, "./") || strings.Contains(p.Source, "..") {
		out = append(out, "plugin source must be a ./ path without ..")
	} else if !pluginExists(strings.TrimPrefix(p.Source, "./")) {
		out = append(out, "plugin source directory has no .claude-plugin/plugin.json")
	}
	return out
}

// pluginManifestKeys are the only keys plugin.json may carry: the documented
// metadata fields, minus every component field (D14: no hooks, servers,
// monitors or executables; the MCP entry lives in .mcp.json).
var pluginManifestKeys = map[string]bool{
	"name": true, "version": true, "description": true, "author": true,
	"homepage": true, "repository": true, "license": true, "keywords": true,
}

func pluginManifestProblems(body string) []string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return []string{"plugin.json: " + err.Error()}
	}
	var out []string
	for k := range raw {
		if !pluginManifestKeys[k] {
			out = append(out, "plugin.json: key "+k+" is not allowed")
		}
	}
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(raw[k], &s)
		return s
	}
	if str("name") != ServerName {
		out = append(out, "plugin.json: name = "+str("name")+", want "+ServerName)
	}
	if !semverRe.MatchString(str("version")) {
		out = append(out, "plugin.json: version "+str("version")+" is not semver X.Y.Z")
	}
	if str("license") != "Apache-2.0" {
		out = append(out, "plugin.json: license must be Apache-2.0")
	}
	if str("description") == "" {
		out = append(out, "plugin.json: description is empty")
	}
	return out
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMarketplaceManifest(t *testing.T) {
	root := repoRoot(t)
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel), ".claude-plugin", "plugin.json"))
		return err == nil
	}
	if p := marketplaceProblems(readRepoFile(t, marketplacePath), exists); len(p) > 0 {
		t.Fatal(strings.Join(p, "\n"))
	}
}

func TestMarketplaceCheckDetectsPlantedDefects(t *testing.T) {
	good := readRepoFile(t, marketplacePath)
	yes := func(string) bool { return true }
	if p := marketplaceProblems(good, yes); len(p) != 0 {
		t.Fatalf("committed marketplace flagged: %v", p)
	}
	cases := map[string]string{
		"reserved-like name": strings.Replace(good, `"name": "dev-health",`, `"name": "other",`, 1),
		"no owner name":      strings.Replace(good, `"name": "Full Chaos"`, `"name": ""`, 1),
		"parent source":      strings.Replace(good, `"./plugins/dev-health"`, `"../plugins/dev-health"`, 1),
		"wrong source":       strings.Replace(good, `"./plugins/dev-health"`, `"./plugins/other"`, 1),
		"no plugins":         `{"name":"dev-health","owner":{"name":"x"},"plugins":[]}`,
		"broken json":        `{`,
	}
	for name, body := range cases {
		if body == good {
			t.Fatalf("%s: mutation did not change the document", name)
		}
		if p := marketplaceProblems(body, yes); len(p) == 0 {
			t.Errorf("%s: planted defect not detected", name)
		}
	}
	if p := marketplaceProblems(good, func(string) bool { return false }); len(p) == 0 {
		t.Error("missing plugin directory not detected")
	}
}

func TestPluginManifest(t *testing.T) {
	if p := pluginManifestProblems(readRepoFile(t, pluginJSONPath)); len(p) > 0 {
		t.Fatal(strings.Join(p, "\n"))
	}
}

func TestPluginManifestCheckDetectsPlantedDefects(t *testing.T) {
	good := readRepoFile(t, pluginJSONPath)
	if p := pluginManifestProblems(good); len(p) != 0 {
		t.Fatalf("committed manifest flagged: %v", p)
	}
	cases := map[string]string{
		"misspelled license": strings.Replace(good, `"license"`, `"licence"`, 1),
		"bad version":        strings.Replace(good, `"0.3.0"`, `"v0.1"`, 1),
		"hooks key":          strings.Replace(good, `"name": "dev-health",`, `"name": "dev-health", "hooks": {},`, 1),
		"mcpServers inline":  strings.Replace(good, `"name": "dev-health",`, `"name": "dev-health", "mcpServers": {},`, 1),
		"wrong name":         strings.Replace(good, `"name": "dev-health",`, `"name": "x",`, 1),
	}
	for name, body := range cases {
		if body == good {
			t.Fatalf("%s: mutation did not change the document", name)
		}
		if p := pluginManifestProblems(body); len(p) == 0 {
			t.Errorf("%s: planted defect not detected", name)
		}
	}
}

// The plugin's .mcp.json is the rendered Claude Code OAuth config, byte for
// byte (CHAOS-6208: default auth flipped to OAuth discovery), and is the
// only MCP entry of the plugin. It carries no Authorization header, no
// headers key and no credential name.
func TestPluginMCPJSONIsTheRenderedOAuthConfig(t *testing.T) {
	want, err := Render(ClaudeCode, OAuth)
	if err != nil {
		t.Fatal(err)
	}
	if got := readRepoFile(t, PluginMCPPath); got != want {
		t.Fatalf("%s differs from the renderer output (run cmd/render -write)", PluginMCPPath)
	}
	for _, banned := range []string{"headers", "Authorization", "Bearer", TokenEnvVar} {
		if strings.Contains(want, banned) {
			t.Fatalf("%s: OAuth variant must not contain %q", PluginMCPPath, banned)
		}
	}
	arts, err := LoadArtifacts(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range arts {
		if a.Path == PluginMCPPath {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s is not a renderer artifact", PluginMCPPath)
	}
}

// The plugin directory holds only the manifest, .mcp.json and the skill.
func TestPluginDirectoryHasOnlyAllowedEntries(t *testing.T) {
	root := filepath.Join(repoRoot(t), filepath.FromSlash(pluginDir))
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{".claude-plugin": true, ".mcp.json": true, "skills": true}
	for _, e := range entries {
		if !allowed[e.Name()] {
			t.Errorf("%s/%s is not an allowed plugin entry", pluginDir, e.Name())
		}
	}
	if len(entries) != len(allowed) {
		t.Errorf("plugin directory has %d entries, want %d", len(entries), len(allowed))
	}
}

// CI must run both validations with --strict, with the Claude Code version
// pinned in one place (ci/claude-code) and installed from that lockfile.
func TestPluginWorkflowValidatesStrictWithPinnedClient(t *testing.T) {
	wf := readRepoFile(t, pluginWorkflow)
	for _, want := range []string{
		"claude plugin validate plugins/dev-health --strict",
		"claude plugin validate . --strict",
		"npm ci",
		"ci/claude-code",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("%s does not contain %q", pluginWorkflow, want)
		}
	}
	var pkg struct {
		Dev map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, claudePinPath)), &pkg); err != nil {
		t.Fatal(err)
	}
	pin := pkg.Dev["@anthropic-ai/claude-code"]
	if !semverRe.MatchString(pin) {
		t.Fatalf("claude-code pin %q is not an exact version", pin)
	}
	if !strings.Contains(readRepoFile(t, "ci/claude-code/package-lock.json"), `"version": "`+pin+`"`) {
		t.Errorf("lockfile does not resolve claude-code %s", pin)
	}
}
