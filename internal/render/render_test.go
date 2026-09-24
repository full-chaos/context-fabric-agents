package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot finds the module root by walking up to go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func loadRepo(t *testing.T) (string, []Artifact) {
	t.Helper()
	root := repoRoot(t)
	arts, err := LoadArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, arts
}

// TestCommittedArtifactsMatchRenderer: every committed artifact is
// byte-for-byte what the renderer produces, no unaccounted file sits in a
// managed directory, and validation and bans are clean.
func TestCommittedArtifactsMatchRenderer(t *testing.T) {
	root, arts := loadRepo(t)
	problems, err := Check(root, arts)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("committed tree differs from the renderer:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestArtifactSetIsComplete(t *testing.T) {
	_, arts := loadRepo(t)
	if want := len(Clients)*len(Variants) + 1 + len(Variants) + len(BundleDirs); len(arts) != want {
		t.Fatalf("got %d artifacts, want %d", len(arts), want)
	}
	seen := map[string]bool{}
	for _, a := range arts {
		if seen[a.Path] {
			t.Errorf("duplicate artifact path %s", a.Path)
		}
		seen[a.Path] = true
	}
	for _, c := range Clients {
		for _, v := range Variants {
			if !seen[ConfigPath(c, v)] {
				t.Errorf("no artifact for %s/%s", c, v)
			}
		}
	}
	if _, err := Render("nope", Bearer); err == nil {
		t.Error("unknown client must not render")
	}
	if _, err := Render(Cursor, "nope"); err == nil {
		t.Error("unknown variant must not render")
	}
}

// TestConstants pins the identifiers the ticket fixes.
func TestConstants(t *testing.T) {
	if RemoteURL != "https://mcp.fullchaos.dev" || ServerName != "dev-health" || TokenEnvVar != "ACR_MCP_TOKEN" {
		t.Fatalf("constants drifted: %q %q %q", RemoteURL, ServerName, TokenEnvVar)
	}
}

// TestEveryConfigNamesTheURLOnceAndOnlyTheHostedEntry: the URL constant is the
// only endpoint a config carries.
func TestEveryConfigNamesTheURLOnce(t *testing.T) {
	_, arts := loadRepo(t)
	for _, a := range arts {
		if a.Kind == KindSkill {
			continue
		}
		if n := strings.Count(a.Content, RemoteURL); n != 1 {
			t.Errorf("%s names the URL %d times, want 1", a.Path, n)
		}
		if strings.Contains(a.Content, "example.com") || strings.Contains(a.Content, "commanderkeen") {
			t.Errorf("%s names a non-production host", a.Path)
		}
	}
}

// TestGoldenLiterals pins exact text for the shapes read from vendor docs, so
// a renderer change cannot silently move a golden and its expectation together.
func TestGoldenLiterals(t *testing.T) {
	golden := map[string]string{
		"plugins/configs/claude-code.bearer.mcp.json": `{
  "mcpServers": {
    "dev-health": {
      "type": "http",
      "url": "https://mcp.fullchaos.dev",
      "headers": {
        "Authorization": "Bearer ${ACR_MCP_TOKEN}"
      }
    }
  }
}
`,
		"plugins/configs/claude-code.oauth.mcp.json": `{
  "mcpServers": {
    "dev-health": {
      "type": "http",
      "url": "https://mcp.fullchaos.dev"
    }
  }
}
`,
		"plugins/configs/claude-code.bearer.add.txt": "claude mcp add --transport http dev-health https://mcp.fullchaos.dev --header 'Authorization: Bearer ${ACR_MCP_TOKEN}'\n",
		"plugins/configs/claude-code.oauth.add.txt":  "claude mcp add --transport http dev-health https://mcp.fullchaos.dev\n",
		"cursor/configs/mcp.bearer.json": `{
  "mcpServers": {
    "dev-health": {
      "url": "https://mcp.fullchaos.dev",
      "headers": {
        "Authorization": "Bearer ${env:ACR_MCP_TOKEN}"
      }
    }
  }
}
`,
		"opencode/configs/opencode.bearer.json": `{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "dev-health": {
      "type": "remote",
      "url": "https://mcp.fullchaos.dev",
      "enabled": true,
      "oauth": false,
      "headers": {
        "Authorization": "Bearer {env:ACR_MCP_TOKEN}"
      }
    }
  }
}
`,
		"opencode/configs/opencode-v2.bearer.json": `{
  "mcp": {
    "servers": {
      "dev-health": {
        "type": "remote",
        "url": "https://mcp.fullchaos.dev",
        "oauth": false,
        "protocol": "auto",
        "headers": {
          "Authorization": "Bearer {env:ACR_MCP_TOKEN}"
        }
      }
    }
  }
}
`,
		"vscode/configs/mcp.oauth.json": `{
  "servers": {
    "dev-health": {
      "type": "http",
      "url": "https://mcp.fullchaos.dev"
    }
  }
}
`,
		"codex/configs/config.bearer.toml": `# Dev Health hosted MCP server entry for Codex CLI (bearer variant).
# Shape source: https://learn.chatgpt.com/docs/extend/mcp
# Codex sends the value of the named environment variable as
# "Authorization: Bearer <value>" on every request. Export the variable in the
# shell that starts Codex; never write the token into this file. Place this
# table in ~/.codex/config.toml (user scope) or .codex/config.toml (project
# scope, requires trusting the project on first use).

[mcp_servers.dev-health]
url = "https://mcp.fullchaos.dev"
bearer_token_env_var = "ACR_MCP_TOKEN"
enabled = true
`,
		"codex/configs/config.oauth.toml": `# Dev Health hosted MCP server entry for Codex CLI (OAuth variant).
# Shape source: https://learn.chatgpt.com/docs/extend/mcp
# No credential is named here. The "auth" key is left at its default, which
# is OAuth: run "codex mcp login dev-health" to sign in. Place this table in
# ~/.codex/config.toml (user scope) or .codex/config.toml (project scope,
# requires trusting the project on first use).

[mcp_servers.dev-health]
url = "https://mcp.fullchaos.dev"
enabled = true
`,
	}
	_, arts := loadRepo(t)
	byPath := map[string]string{}
	for _, a := range arts {
		byPath[a.Path] = a.Content
	}
	for path, want := range golden {
		if got, ok := byPath[path]; !ok || got != want {
			t.Errorf("%s does not match its pinned golden:\n%s", path, got)
		}
	}
}

// TestBearerConfigsUseTheClientExpansion: each bearer config names the token
// only through its client's own env expansion (or VS Code's password input).
func TestBearerConfigsUseTheClientExpansion(t *testing.T) {
	_, arts := loadRepo(t)
	for _, a := range arts {
		if a.Kind != KindConfig || a.Variant != Bearer || a.Client == Codex {
			continue
		}
		if want := Expansion(a.Client); !strings.Contains(a.Content, "Bearer "+want) {
			t.Errorf("%s lacks %q", a.Path, "Bearer "+want)
		}
	}
}

// TestSkillSourceIsValidAndCopiedEverywhere: one source, five byte-identical
// copies, one per bundle.
func TestRenderCodexWithURL_DefaultURLMatchesCanonicalRender(t *testing.T) {
	for _, v := range Variants {
		want, err := Render(Codex, v)
		if err != nil {
			t.Fatalf("Render(Codex, %s): %v", v, err)
		}
		got, err := RenderCodexWithURL(v, RemoteURL)
		if err != nil {
			t.Fatalf("RenderCodexWithURL(%s, RemoteURL): %v", v, err)
		}
		if got != want {
			t.Errorf("RenderCodexWithURL(%s, RemoteURL) diverges from Render(Codex, %s):\n%s\nvs\n%s", v, v, got, want)
		}
	}
}

func TestRenderCodexWithURL_CustomURL(t *testing.T) {
	const custom = "https://mcp.example.internal/mcp"
	for _, v := range Variants {
		got, err := RenderCodexWithURL(v, custom)
		if err != nil {
			t.Fatalf("RenderCodexWithURL(%s, custom): %v", v, err)
		}
		if !strings.Contains(got, custom) {
			t.Errorf("RenderCodexWithURL(%s, custom) does not contain %q:\n%s", v, custom, got)
		}
		if strings.Contains(got, RemoteURL) {
			t.Errorf("RenderCodexWithURL(%s, custom) still contains the default RemoteURL:\n%s", v, got)
		}
	}
}

func TestRenderCodexWithURL_UnknownVariant(t *testing.T) {
	if _, err := RenderCodexWithURL("bogus", RemoteURL); err == nil {
		t.Error("want error for an unknown variant")
	}
}

func TestRenderClaudeCodeAddCommandWithURL_DefaultURLMatchesCanonical(t *testing.T) {
	for _, v := range Variants {
		want := RenderClaudeCodeAddCommand(v)
		got := RenderClaudeCodeAddCommandWithURL(v, RemoteURL)
		if got != want {
			t.Errorf("RenderClaudeCodeAddCommandWithURL(%s, RemoteURL) diverges:\n%s\nvs\n%s", v, got, want)
		}
	}
}

func TestRenderClaudeCodeAddCommandWithURL_CustomURL(t *testing.T) {
	const custom = "https://mcp.example.internal/mcp"
	for _, v := range Variants {
		got := RenderClaudeCodeAddCommandWithURL(v, custom)
		if !strings.Contains(got, custom) {
			t.Errorf("RenderClaudeCodeAddCommandWithURL(%s, custom) does not contain %q: %s", v, custom, got)
		}
		if strings.Contains(got, RemoteURL) {
			t.Errorf("RenderClaudeCodeAddCommandWithURL(%s, custom) still contains the default RemoteURL: %s", v, got)
		}
	}
}

func TestSkillSourceIsValidAndCopiedEverywhere(t *testing.T) {
	root, arts := loadRepo(t)
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(SkillSource)))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSkill(string(src)); err != nil {
		t.Fatal(err)
	}
	copies := 0
	for _, a := range arts {
		if a.Kind != KindSkill {
			continue
		}
		copies++
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(a.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(src) {
			t.Errorf("%s is not a byte-identical copy of %s", a.Path, SkillSource)
		}
	}
	if copies != len(BundleDirs) {
		t.Fatalf("%d skill copies, want %d", copies, len(BundleDirs))
	}
}
