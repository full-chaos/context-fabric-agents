package render

import (
	"regexp"
	"strings"
	"testing"
)

func artifact(t *testing.T, c Client, v Variant) Artifact {
	t.Helper()
	content, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}
	return Artifact{Path: ConfigPath(c, v), Kind: KindConfig, Client: c, Variant: v, Content: content}
}

func TestValidateAcceptsEveryRenderedArtifact(t *testing.T) {
	_, arts := loadRepo(t)
	for _, a := range arts {
		if err := Validate(a); err != nil {
			t.Errorf("%v", err)
		}
	}
}

// TestValidatorsRejectPlantedDefects: each row plants one defect into a
// rendered config and requires Validate to fail with the named reason.
func TestValidatorsRejectPlantedDefects(t *testing.T) {
	type planted struct {
		name    string
		client  Client
		variant Variant
		mutate  func(string) string
		want    string
	}
	rep := func(old, new string) func(string) string {
		return func(s string) string {
			if !strings.Contains(s, old) {
				panic("mutation anchor not found: " + old)
			}
			return strings.Replace(s, old, new, 1)
		}
	}
	cases := []planted{
		{"claude wrong url", ClaudeCode, Bearer, rep(RemoteURL, "https://mcp.example.com/mcp"), "type/url"},
		{"claude missing type", ClaudeCode, Bearer, rep(`"type": "http",`, ``), "type/url"},
		{"claude wrong expansion", ClaudeCode, Bearer, rep("${ACR_MCP_TOKEN}", "${env:ACR_MCP_TOKEN}"), "headers"},
		{"claude oauth with header", ClaudeCode, OAuth, rep(`"url": "`+RemoteURL+`"`, `"url": "`+RemoteURL+`", "headers": {"Authorization": "Bearer ${ACR_MCP_TOKEN}"}`), "oauth variant must carry no headers"},
		{"claude duplicate key", ClaudeCode, Bearer, rep(`"type": "http",`, `"type": "http", "type": "http",`), "duplicate JSON key"},
		{"claude unknown field", ClaudeCode, Bearer, rep(`"type": "http",`, `"type": "http", "command": "x",`), "unknown field"},
		{"claude trailing data", ClaudeCode, Bearer, func(s string) string { return s + "{}" }, "trailing data"},
		{"claude second server", ClaudeCode, OAuth, rep(`"dev-health": {`, `"other": {"type": "http", "url": "x"}, "dev-health": {`), "exactly one"},
		{"cursor typed entry", Cursor, Bearer, rep(`"url"`, `"type": "http", "url"`), "type/url"},
		{"cursor extra header", Cursor, Bearer, rep(`"Authorization"`, `"X-Extra": "1", "Authorization"`), "headers"},
		{"opencode bearer oauth on", OpenCode, Bearer, rep(`"oauth": false`, `"oauth": {}`), "oauth"},
		{"opencode oauth off", OpenCode, OAuth, rep(`"oauth": {}`, `"oauth": false`), "oauth"},
		{"opencode disabled", OpenCode, OAuth, rep(`"enabled": true`, `"enabled": false`), "enabled"},
		{"opencode v1 shape in v2", OpenCodeV2, Bearer, rep(`"servers": {`, `"dev-health": {"type": "remote", "url": "x"}, "servers": {`), "unknown field"},
		{"opencode v2 protocol legacy", OpenCodeV2, OAuth, rep(`"auto"`, `"legacy"`), "protocol"},
		{"vscode missing input", VSCode, Bearer, rep(`"password": true`, `"password": false`), "promptString password input"},
		{"vscode oauth with inputs", VSCode, OAuth, rep(`"servers"`, `"inputs": [{"type": "promptString", "id": "x", "description": "d", "password": true}], "servers"`), "no inputs"},
		{"vscode type sse", VSCode, OAuth, rep(`"http"`, `"sse"`), "type/url"},
		{"codex duplicate key", Codex, Bearer, rep("enabled = true", "enabled = true\nenabled = true"), "duplicate key"},
		{"codex duplicate table", Codex, Bearer, func(s string) string { return s + "\n[mcp_servers.dev-health]\n" }, "duplicate table"},
		{"codex trailing comment", Codex, Bearer, rep(`enabled = true`, `enabled = true # on`), "unsupported value"},
		{"codex missing bearer key", Codex, Bearer, rep("bearer_token_env_var", "bearer_token_env_var_x"), "bearer_token_env_var"},
		{"codex oauth carrying bearer", Codex, OAuth, rep("enabled = true", "enabled = true\nbearer_token_env_var = \"ACR_MCP_TOKEN\""), "keys"},
		{"codex bad string", Codex, Bearer, rep(`url = "`+RemoteURL+`"`, `url = "`+RemoteURL), "bad string"},
		{"codex assignment outside table", Codex, Bearer, func(s string) string { return "url = \"x\"\n" + s }, "outside of any table"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := artifact(t, tc.client, tc.variant)
			a.Content = tc.mutate(a.Content)
			err := Validate(a)
			if err == nil {
				t.Fatalf("planted defect accepted:\n%s", a.Content)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("rejected for the wrong reason: %v (want %q)", err, tc.want)
			}
		})
	}
}

func TestSkillValidatorRejectsPlantedDefects(t *testing.T) {
	_, arts := loadRepo(t)
	var good string
	for _, a := range arts {
		if a.Kind == KindSkill {
			good = a.Content
			break
		}
	}
	cases := map[string]func(string) string{
		"no opening fence":  func(s string) string { return strings.TrimPrefix(s, "---\n") },
		"wrong name":        func(s string) string { return strings.Replace(s, "name: dev-health", "name: Dev-Health", 1) },
		"name dir mismatch": func(s string) string { return strings.Replace(s, "name: dev-health", "name: other", 1) },
		"empty description": func(s string) string {
			return regexp.MustCompile(`(?m)^description: .*$`).ReplaceAllString(s, "description: ")
		},
		"long description": func(s string) string {
			return strings.Replace(s, "description: ", "description: "+strings.Repeat("x", 1100), 1)
		},
		"unknown key": func(s string) string {
			return strings.Replace(s, "name: dev-health\n", "name: dev-health\nallowed-tools: Bash\n", 1)
		},
		"missing receipts":    func(s string) string { return strings.ReplaceAll(s, "prior_*_receipts", "prior receipts") },
		"missing untrusted":   func(s string) string { return strings.ReplaceAll(s, "untrusted", "unverified") },
		"missing repo slug":   func(s string) string { return strings.ReplaceAll(s, "repository.slug", "repository") },
		"no closing fence":    func(s string) string { return strings.Replace(s, "\n---\n", "\n", 1) },
		"no trailing newline": func(s string) string { return strings.TrimRight(s, "\n") },
	}
	if err := validateSkill(good); err != nil {
		t.Fatalf("control: %v", err)
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateSkill(mutate(good)); err == nil {
				t.Fatal("planted defect accepted")
			}
		})
	}
}

func TestAddCommandValidatorRejectsUnquotedHeader(t *testing.T) {
	a := Artifact{Path: AddCommandPath(Bearer), Kind: KindCommand, Client: ClaudeCode, Variant: Bearer,
		Content: strings.ReplaceAll(RenderClaudeCodeAddCommand(Bearer), "'", "\"")}
	if err := Validate(a); err == nil {
		t.Fatal("double-quoted header accepted; the shell would expand the token")
	}
}
