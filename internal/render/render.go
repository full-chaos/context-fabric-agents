// Package render is the canonical model for the remote (hosted, Streamable
// HTTP) client configs this repository ships, plus the skill copies. It is
// ported from the acr `internal/mcpclientfixtures/remote.go` renderer (stdlib
// only) and owns the goldens from here on: `cmd/render -write` regenerates
// every artifact and `cmd/render -check` fails when a committed file differs.
//
// Every config points a client at the hosted server and names the caller's
// credential only through the client's own environment expansion. A literal
// token never appears in any rendered artifact. Per-client shapes and the
// vendor doc URL each one was read from are listed in README.md next to this
// file.
package render

import "fmt"

const (
	// RemoteURL is the hosted MCP endpoint (production).
	RemoteURL = "https://mcp.fullchaos.dev/mcp"
	// ServerName is the server key every remote config registers.
	ServerName = "dev-health"
	// TokenEnvVar names the environment variable that holds the caller's
	// token in the bearer variants. Each client expands it at connect time.
	TokenEnvVar = "ACR_MCP_TOKEN"
	// SkillName is the Agent Skills name and directory of the shared skill.
	SkillName = "dev-health"
	// SkillSource is the repo-relative path of the one skill source.
	SkillSource = "skills/dev-health/SKILL.md"

	// vsCodeInputID is the VS Code `inputs` id that carries the bearer token.
	vsCodeInputID = "acr-mcp-token"
)

// Client names one client a remote config is rendered for.
type Client string

const (
	ClaudeCode Client = "claude-code"
	Codex      Client = "codex"
	OpenCode   Client = "opencode"
	// OpenCodeV2 is the OpenCode v2 config shape. It is not accepted by v1
	// and does not accept v1's.
	OpenCodeV2 Client = "opencode-v2"
	Cursor     Client = "cursor"
	VSCode     Client = "vscode"
)

// Clients lists every client with a remote config, in a fixed order.
var Clients = []Client{ClaudeCode, Codex, OpenCode, OpenCodeV2, Cursor, VSCode}

// Variant is the authentication variant of a config.
type Variant string

const (
	// Bearer sends the caller's token from an environment variable (or, for
	// VS Code, a password prompt) as `Authorization: Bearer <token>`.
	Bearer Variant = "bearer"
	// OAuth carries the URL only. The client discovers the authorization
	// server from the 401 challenge and runs its own login. No Authorization
	// header, no credential name.
	OAuth Variant = "oauth"
)

// Variants lists both variants, in a fixed order.
var Variants = []Variant{Bearer, OAuth}

// Kind says what an artifact is.
type Kind string

const (
	KindConfig  Kind = "config"
	KindCommand Kind = "command"
	KindSkill   Kind = "skill"
)

// Artifact is one generated file.
type Artifact struct {
	// Path is the slash-separated repo-relative path.
	Path    string
	Kind    Kind
	Client  Client
	Variant Variant // empty for skills
	Content string
}

// bundleDir is the repo directory of each client's bundle.
var bundleDir = map[Client]string{
	ClaudeCode: "plugins",
	Codex:      "codex",
	OpenCode:   "opencode",
	OpenCodeV2: "opencode",
	Cursor:     "cursor",
	VSCode:     "vscode",
}

// BundleDirs lists the distinct bundle directories, in a fixed order.
var BundleDirs = []string{"plugins", "codex", "opencode", "cursor", "vscode"}

// skillDir is where each bundle keeps its copy of the shared skill.
var skillDir = map[string]string{
	"plugins":  "plugins/dev-health/skills/dev-health",
	"codex":    "codex/skills/dev-health",
	"opencode": "opencode/skills/dev-health",
	"cursor":   "cursor/skills/dev-health",
	"vscode":   "vscode/skills/dev-health",
}

// configFile is the file name of each client's config under <bundle>/configs.
var configFile = map[Client]struct{ bearer, oauth string }{
	ClaudeCode: {"claude-code.bearer.mcp.json", "claude-code.oauth.mcp.json"},
	Codex:      {"config.bearer.toml", "config.oauth.toml"},
	OpenCode:   {"opencode.bearer.json", "opencode.oauth.json"},
	OpenCodeV2: {"opencode-v2.bearer.json", "opencode-v2.oauth.json"},
	Cursor:     {"mcp.bearer.json", "mcp.oauth.json"},
	VSCode:     {"mcp.bearer.json", "mcp.oauth.json"},
}

// ConfigPath returns the repo-relative path of a client's config.
func ConfigPath(c Client, v Variant) string {
	f := configFile[c]
	name := f.bearer
	if v == OAuth {
		name = f.oauth
	}
	return bundleDir[c] + "/configs/" + name
}

// PluginMCPPath is the repo-relative path of the Claude Code plugin's
// `.mcp.json`. It is the OAuth variant of the Claude Code config (CHAOS-6201
// shipped the bearer variant for v0.1.0; CHAOS-6208 flips the default to
// OAuth discovery now that it is live on prod. The bearer variant stays
// documented and rendered at plugins/configs/claude-code.bearer.mcp.json for
// headless/CI use).
const PluginMCPPath = "plugins/dev-health/.mcp.json"

// AddCommandPath returns the repo-relative path of the Claude Code
// `claude mcp add` command file.
func AddCommandPath(v Variant) string {
	return "plugins/configs/claude-code." + string(v) + ".add.txt"
}

// Expansion returns the client-specific env expansion a bearer config uses.
func Expansion(c Client) string {
	switch c {
	case ClaudeCode:
		return "${" + TokenEnvVar + "}"
	case Cursor:
		return "${env:" + TokenEnvVar + "}"
	case OpenCode, OpenCodeV2:
		return "{env:" + TokenEnvVar + "}"
	case VSCode:
		return "${input:" + vsCodeInputID + "}"
	}
	return ""
}

// Render returns the config for one client and variant.
func Render(c Client, v Variant) (string, error) {
	if v != Bearer && v != OAuth {
		return "", fmt.Errorf("unknown variant %q", v)
	}
	switch c {
	case ClaudeCode:
		return renderClaudeCode(v), nil
	case Codex:
		return renderCodex(v), nil
	case OpenCode:
		return renderOpenCode(v), nil
	case OpenCodeV2:
		return renderOpenCodeV2(v), nil
	case Cursor:
		return renderCursor(v), nil
	case VSCode:
		return renderVSCode(v), nil
	}
	return "", fmt.Errorf("unknown client %q", c)
}

// RenderClaudeCodeAddCommand renders the `claude mcp add` form. In the bearer
// variant the header value is single-quoted so the shell passes ${ACR_MCP_TOKEN}
// through unexpanded and the token is never written to the client's config.
func RenderClaudeCodeAddCommand(v Variant) string {
	if v == OAuth {
		return fmt.Sprintf("claude mcp add --transport http %s %s\n", ServerName, RemoteURL)
	}
	return fmt.Sprintf("claude mcp add --transport http %s %s --header 'Authorization: Bearer ${%s}'\n",
		ServerName, RemoteURL, TokenEnvVar)
}

// Artifacts returns every generated file, in a fixed order. skill is the
// content of SkillSource; it is copied verbatim into each bundle.
func Artifacts(skill string) ([]Artifact, error) {
	var out []Artifact
	for _, c := range Clients {
		for _, v := range Variants {
			content, err := Render(c, v)
			if err != nil {
				return nil, err
			}
			out = append(out, Artifact{Path: ConfigPath(c, v), Kind: KindConfig, Client: c, Variant: v, Content: content})
		}
	}
	pluginMCP, err := Render(ClaudeCode, OAuth)
	if err != nil {
		return nil, err
	}
	out = append(out, Artifact{Path: PluginMCPPath, Kind: KindConfig, Client: ClaudeCode, Variant: OAuth, Content: pluginMCP})
	for _, v := range Variants {
		out = append(out, Artifact{Path: AddCommandPath(v), Kind: KindCommand, Client: ClaudeCode, Variant: v, Content: RenderClaudeCodeAddCommand(v)})
	}
	for _, b := range BundleDirs {
		out = append(out, Artifact{Path: skillDir[b] + "/SKILL.md", Kind: KindSkill, Content: skill})
	}
	return out, nil
}

// ManagedDirs lists the directories whose whole content the renderer owns:
// a file there that no artifact accounts for is drift.
func ManagedDirs() []string {
	var dirs []string
	for _, b := range BundleDirs {
		dirs = append(dirs, b+"/configs", skillDir[b])
	}
	return dirs
}

func renderClaudeCode(v Variant) string {
	if v == OAuth {
		return fmt.Sprintf(`{
  "mcpServers": {
    %q: {
      "type": "http",
      "url": %q
    }
  }
}
`, ServerName, RemoteURL)
	}
	return fmt.Sprintf(`{
  "mcpServers": {
    %q: {
      "type": "http",
      "url": %q,
      "headers": {
        "Authorization": "Bearer ${%s}"
      }
    }
  }
}
`, ServerName, RemoteURL, TokenEnvVar)
}

func renderCursor(v Variant) string {
	if v == OAuth {
		return fmt.Sprintf(`{
  "mcpServers": {
    %q: {
      "url": %q
    }
  }
}
`, ServerName, RemoteURL)
	}
	return fmt.Sprintf(`{
  "mcpServers": {
    %q: {
      "url": %q,
      "headers": {
        "Authorization": "Bearer ${env:%s}"
      }
    }
  }
}
`, ServerName, RemoteURL, TokenEnvVar)
}

func renderOpenCode(v Variant) string {
	if v == OAuth {
		return fmt.Sprintf(`{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    %q: {
      "type": "remote",
      "url": %q,
      "enabled": true,
      "oauth": {}
    }
  }
}
`, ServerName, RemoteURL)
	}
	return fmt.Sprintf(`{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    %q: {
      "type": "remote",
      "url": %q,
      "enabled": true,
      "oauth": false,
      "headers": {
        "Authorization": "Bearer {env:%s}"
      }
    }
  }
}
`, ServerName, RemoteURL, TokenEnvVar)
}

func renderOpenCodeV2(v Variant) string {
	if v == OAuth {
		return fmt.Sprintf(`{
  "mcp": {
    "servers": {
      %q: {
        "type": "remote",
        "url": %q,
        "protocol": "auto",
        "oauth": {}
      }
    }
  }
}
`, ServerName, RemoteURL)
	}
	return fmt.Sprintf(`{
  "mcp": {
    "servers": {
      %q: {
        "type": "remote",
        "url": %q,
        "oauth": false,
        "protocol": "auto",
        "headers": {
          "Authorization": "Bearer {env:%s}"
        }
      }
    }
  }
}
`, ServerName, RemoteURL, TokenEnvVar)
}

func renderVSCode(v Variant) string {
	if v == OAuth {
		return fmt.Sprintf(`{
  "servers": {
    %q: {
      "type": "http",
      "url": %q
    }
  }
}
`, ServerName, RemoteURL)
	}
	return fmt.Sprintf(`{
  "inputs": [
    {
      "type": "promptString",
      "id": %q,
      "description": "Dev Health MCP token (the value of %s)",
      "password": true
    }
  ],
  "servers": {
    %q: {
      "type": "http",
      "url": %q,
      "headers": {
        "Authorization": "Bearer ${input:%s}"
      }
    }
  }
}
`, vsCodeInputID, TokenEnvVar, ServerName, RemoteURL, vsCodeInputID)
}

const codexBearerComment = `# Dev Health hosted MCP server entry for Codex CLI (bearer variant).
# Shape source: https://learn.chatgpt.com/docs/extend/mcp
# Codex sends the value of the named environment variable as
# "Authorization: Bearer <value>" on every request. Export the variable in the
# shell that starts Codex; never write the token into this file. Place this
# table in ~/.codex/config.toml (user scope) or .codex/config.toml (project
# scope, requires trusting the project on first use).
`

const codexOAuthComment = `# Dev Health hosted MCP server entry for Codex CLI (OAuth variant).
# Shape source: https://learn.chatgpt.com/docs/extend/mcp
# No credential is named here. The "auth" key is left at its default, which
# is OAuth: run "codex mcp login dev-health" to sign in. Place this table in
# ~/.codex/config.toml (user scope) or .codex/config.toml (project scope,
# requires trusting the project on first use).
`

func renderCodex(v Variant) string {
	if v == OAuth {
		return fmt.Sprintf(`%s
[mcp_servers.%s]
url = %q
enabled = true
`, codexOAuthComment, ServerName, RemoteURL)
	}
	return fmt.Sprintf(`%s
[mcp_servers.%s]
url = %q
bearer_token_env_var = %q
enabled = true
`, codexBearerComment, ServerName, RemoteURL, TokenEnvVar)
}
