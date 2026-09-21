package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bundleRoot returns a temp tree with every bundle directory present.
func bundleRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, b := range BundleDirs {
		if err := os.MkdirAll(filepath.Join(root, b), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRepoBundlesHaveNoBannedEntries(t *testing.T) {
	problems, err := CheckBundleBans(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("bundle bans violated:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestRepoArtifactsPassTheBans(t *testing.T) {
	_, arts := loadRepo(t)
	if problems := CheckArtifactBans(arts); len(problems) > 0 {
		t.Fatalf("artifact bans violated:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestNoAuthorizationHeaderInOAuthVariants is the explicit ban from the
// ticket, asserted on the committed files independently of the renderer.
func TestNoAuthorizationHeaderInOAuthVariants(t *testing.T) {
	root := repoRoot(t)
	n := 0
	for _, c := range Clients {
		p := filepath.Join(root, filepath.FromSlash(ConfigPath(c, OAuth)))
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		n++
		for _, banned := range []string{"Authorization", "Bearer", "headers", TokenEnvVar} {
			if strings.Contains(string(data), banned) {
				t.Errorf("%s contains %q", ConfigPath(c, OAuth), banned)
			}
		}
	}
	if n != len(Clients) {
		t.Fatalf("checked %d oauth configs, want %d", n, len(Clients))
	}
}

func TestArtifactBansDetectPlantedDefects(t *testing.T) {
	token := "fcacr" + "_" + strings.Repeat("a", 43)
	long := strings.Repeat("A1b2", 12)
	cases := []struct {
		name string
		art  Artifact
		want string
	}{
		{"prefixed token", Artifact{Path: "p", Kind: KindConfig, Content: `{"k": "` + token + `"}`}, "token-shaped literal"},
		{"long opaque token", Artifact{Path: "p", Kind: KindConfig, Content: `{"k": "` + long + `"}`}, "token-shaped literal"},
		{"literal bearer", Artifact{Path: "p", Kind: KindConfig, Client: Cursor, Variant: Bearer, Content: `"Authorization": "Bearer abc123"`}, "Bearer is followed"},
		{"bearer then expansion then literal", Artifact{Path: "p", Kind: KindConfig, Content: `Bearer ${ACR_MCP_TOKEN} Bearer literal`}, "Bearer is followed"},
		{"oauth authorization header", Artifact{Path: "p", Kind: KindConfig, Client: Cursor, Variant: OAuth, Content: `{"headers": {"Authorization": "Bearer ${env:ACR_MCP_TOKEN}"}}`}, "oauth variant contains"},
		{"oauth env var name", Artifact{Path: "p", Kind: KindConfig, Client: Codex, Variant: OAuth, Content: "# set ACR_MCP_TOKEN\n"}, "oauth variant contains"},
		{"stdio command", Artifact{Path: "p", Kind: KindConfig, Content: `{"command": "acr-mcp"}`}, "STDIO-only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(CheckArtifactBans([]Artifact{tc.art}), "\n")
			if !strings.Contains(got, tc.want) {
				t.Fatalf("planted defect not reported (want %q), got %q", tc.want, got)
			}
		})
	}
	// Control: the allowed expansions pass.
	for _, c := range Clients {
		if c == Codex {
			continue
		}
		a := artifact(t, c, Bearer)
		if problems := CheckArtifactBans([]Artifact{a}); len(problems) > 0 {
			t.Errorf("control %s: %v", c, problems)
		}
	}
}

func TestBundleBansDetectPlantedDefects(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, root string)
		want  string
	}{
		{"hooks dir in a plugin", func(t *testing.T, r string) {
			mustWrite(t, r, "plugins/dev-health/hooks/hooks.json", "{}")
		}, `forbidden plugin entry "hooks"`},
		{"bin dir", func(t *testing.T, r string) { mustWrite(t, r, "codex/plugin/bin/run.sh", "#!/bin/sh") }, `forbidden plugin entry "bin"`},
		{"lsp file", func(t *testing.T, r string) { mustWrite(t, r, "plugins/dev-health/.lsp.json", "{}") }, `".lsp.json"`},
		{"monitors dir", func(t *testing.T, r string) { mustWrite(t, r, "cursor/x/monitors/monitors.json", "{}") }, `"monitors"`},
		{"manifest with hooks", func(t *testing.T, r string) {
			mustWrite(t, r, "plugins/dev-health/.claude-plugin/plugin.json", `{"name":"dev-health","hooks":{}}`)
		}, `manifest declares "hooks"`},
		{"mcp server with command", func(t *testing.T, r string) {
			mustWrite(t, r, "plugins/dev-health/.mcp.json", `{"mcpServers":{"x":{"command":"acr-mcp"}}}`)
		}, "runs a local command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := bundleRoot(t)
			problems, err := CheckBundleBans(root)
			if err != nil || len(problems) > 0 {
				t.Fatalf("control on the clean tree: %v %v", problems, err)
			}
			tc.plant(t, root)
			problems, err = CheckBundleBans(root)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Fatalf("planted defect not reported (want %q): %v", tc.want, problems)
			}
		})
	}
}

// TestBundleBansFailWhenABundleIsMissing: an unmeasured tree must not read as clean.
func TestBundleBansFailWhenABundleIsMissing(t *testing.T) {
	root := bundleRoot(t)
	if err := os.Remove(filepath.Join(root, "vscode")); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckBundleBans(root); err == nil {
		t.Fatal("missing bundle directory must be an error")
	}
}

// TestCheckDetectsDrift: -check semantics on a scratch tree.
func TestCheckDetectsDrift(t *testing.T) {
	_, arts := loadRepo(t)
	setup := func(t *testing.T) string {
		root := bundleRoot(t)
		if err := Write(root, arts); err != nil {
			t.Fatal(err)
		}
		if problems, err := Check(root, arts); err != nil || len(problems) > 0 {
			t.Fatalf("control: %v %v", problems, err)
		}
		return root
	}
	t.Run("hand-edited golden", func(t *testing.T) {
		root := setup(t)
		p := ConfigPath(Cursor, Bearer)
		data, _ := os.ReadFile(filepath.Join(root, p))
		mustWrite(t, root, p, strings.Replace(string(data), "Bearer", "Basic", 1))
		got, err := Check(root, arts)
		if err != nil || !strings.Contains(strings.Join(got, "\n"), p+": differs from the renderer") {
			t.Fatalf("hand edit not reported: %v %v", got, err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		root := setup(t)
		p := ConfigPath(Codex, OAuth)
		if err := os.Remove(filepath.Join(root, p)); err != nil {
			t.Fatal(err)
		}
		got, _ := Check(root, arts)
		if !strings.Contains(strings.Join(got, "\n"), p+": missing") {
			t.Fatalf("missing file not reported: %v", got)
		}
	})
	t.Run("extra file in a managed dir", func(t *testing.T) {
		root := setup(t)
		mustWrite(t, root, "vscode/configs/stale.json", "{}")
		got, _ := Check(root, arts)
		if !strings.Contains(strings.Join(got, "\n"), "vscode/configs/stale.json: not produced by the renderer") {
			t.Fatalf("extra file not reported: %v", got)
		}
	})
	t.Run("skill copy drifted", func(t *testing.T) {
		root := setup(t)
		p := "codex/skills/dev-health/SKILL.md"
		mustWrite(t, root, p, "changed\n")
		got, _ := Check(root, arts)
		if !strings.Contains(strings.Join(got, "\n"), p+": differs") {
			t.Fatalf("skill drift not reported: %v", got)
		}
	})
	t.Run("hooks dir added to a plugin", func(t *testing.T) {
		root := setup(t)
		mustWrite(t, root, "plugins/dev-health/hooks/hooks.json", "{}")
		got, _ := Check(root, arts)
		if !strings.Contains(strings.Join(got, "\n"), `forbidden plugin entry "hooks"`) {
			t.Fatalf("hooks dir not reported: %v", got)
		}
	})
}

func TestWriteRefusesInvalidArtifacts(t *testing.T) {
	_, arts := loadRepo(t)
	bad := append([]Artifact(nil), arts...)
	bad[0].Content = strings.Replace(bad[0].Content, RemoteURL, "https://x.invalid/mcp", 1)
	root := bundleRoot(t)
	if err := Write(root, bad); err == nil {
		t.Fatal("invalid artifact was written")
	}
	if _, err := os.Stat(filepath.Join(root, bad[0].Path)); err == nil {
		t.Fatal("a file was written despite the validation failure")
	}
}
