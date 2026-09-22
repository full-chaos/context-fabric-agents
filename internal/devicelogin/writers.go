package devicelogin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/full-chaos/context-fabric-agents/internal/render"
)

// TargetClient names which headless client's config Write wires the token
// into. It intentionally reuses render.Client's names where one exists
// (Codex, ClaudeCode) plus two client-agnostic outputs.
type TargetClient string

const (
	TargetCodex      TargetClient = "codex"
	TargetClaudeCode TargetClient = "claude-code"
	// TargetEnv writes the token to a 0600 env file only; no client config
	// is touched.
	TargetEnv TargetClient = "env"
	// TargetStdout prints the bare token to stdout and nothing else on
	// stdout. It is the one mode that puts the token on a terminal/pipe by
	// design, for scripted capture (e.g. TOKEN=$(login --client stdout)).
	// Every other mode never prints the token.
	TargetStdout TargetClient = "stdout"
)

// Targets lists every accepted --client value, in the order -h should show
// them.
var Targets = []TargetClient{TargetCodex, TargetClaudeCode, TargetEnv, TargetStdout}

// ParseTargetClient validates a --client flag value.
func ParseTargetClient(s string) (TargetClient, error) {
	for _, t := range Targets {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("--client %q is not one of %v", s, Targets)
}

// WriteResult reports what Write did, for the caller's human-readable
// summary. It never carries the token itself.
type WriteResult struct {
	// EnvFile is the 0600 file the token was written to (empty for
	// TargetStdout, which writes no file).
	EnvFile string
	// ConfigWritten is the config file changed for TargetCodex, or the
	// `claude mcp add` command run for TargetClaudeCode; empty otherwise.
	ConfigWritten string
	// ManualCommand is set instead of ConfigWritten when the client's own
	// CLI was not found and the caller must run a command by hand. It
	// never contains the literal token (see render.RenderClaudeCodeAddCommand
	// and renderCodexBearerBlock: both name the token only by the
	// ACR_MCP_TOKEN environment-variable expansion, never a value).
	ManualCommand string
	// Warning is set when the token was saved (EnvFile is always populated
	// on the same call) but the client's own config was NOT wired to use
	// it -- e.g. an existing mcp_servers.dev-health table in Codex's
	// config.toml that is not the bearer shape (an OAuth table, or one
	// hand-edited without bearer_token_env_var). Write never overwrites an
	// existing table of any shape, so this is the caller's signal that
	// "token saved" does not mean "client wired" this time.
	Warning string
}

// EnvFilePath returns the 0600 env file Write uses for target, under dir
// (the caller's config directory; StateDir() gives the default).
func EnvFilePath(dir string, target TargetClient) string {
	return filepath.Join(dir, string(target)+".env")
}

// StateDir is the default directory Write's env files and lock state live
// under: $XDG_CONFIG_HOME/context-fabric-agents/login, or
// ~/.config/context-fabric-agents/login.
func StateDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config directory: %w", err)
	}
	return filepath.Join(base, "context-fabric-agents", "login"), nil
}

// writeEnvFile writes token to path as `export ACR_MCP_TOKEN=<token>\n`,
// creating parent directories at 0700 and the file at 0600. It never
// widens an existing file's permissions and refuses to follow a symlink at
// path (O_NOFOLLOW-equivalent via Lstat check) so a pre-planted link can't
// redirect the write.
func writeEnvFile(path, token string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink, refusing to write a credential through it", path)
	}
	content := fmt.Sprintf("export %s=%s\n", render.TokenEnvVar, token)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil { // belt-and-braces vs. a restrictive umask leaving it looser than requested is fine; tighten if looser.
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return f.Close()
}

// Write wires token into the target client's config and returns what it
// did. dir is the directory env files are written under (StateDir() for
// the real CLI; a temp dir in tests). mcpURL is the hosted MCP endpoint the
// caller signed in against (--mcp-url); it is written into the client
// config exactly as given, so a non-default (self-hosted/trial) endpoint is
// what the client actually connects to, not the compiled-in default.
func Write(ctx context.Context, target TargetClient, dir, token, mcpURL string) (*WriteResult, error) {
	switch target {
	case TargetStdout:
		return &WriteResult{}, nil
	case TargetEnv:
		path := EnvFilePath(dir, target)
		if err := writeEnvFile(path, token); err != nil {
			return nil, err
		}
		return &WriteResult{EnvFile: path}, nil
	case TargetCodex:
		path := EnvFilePath(dir, target)
		if err := writeEnvFile(path, token); err != nil {
			return nil, err
		}
		configPath, warning, err := writeCodexConfig(mcpURL)
		if err != nil {
			return nil, err
		}
		// ConfigWritten means "the client is wired to use this token" --
		// when writeCodexConfig left an existing non-bearer table alone, it
		// still names the path (for the warning's own wording) but nothing
		// was actually wired, so ConfigWritten must stay empty here or the
		// caller prints a contradictory "wired ... / WARNING: not wired".
		result := &WriteResult{EnvFile: path, Warning: warning}
		if warning == "" {
			result.ConfigWritten = configPath
		}
		return result, nil
	case TargetClaudeCode:
		path := EnvFilePath(dir, target)
		if err := writeEnvFile(path, token); err != nil {
			return nil, err
		}
		result := &WriteResult{EnvFile: path}
		// Any failure to run `claude mcp add` -- the binary missing, OR
		// present but failing (e.g. not logged in: chris, "you can't use a
		// fake claude path because it wants a login to start" -- the same
		// applies to a genuinely un-authenticated real CLI) -- falls back
		// to the manual command exactly the same way. The token is still
		// saved either way; only the config-wiring step is skipped.
		if err := runClaudeMCPAdd(ctx, mcpURL); err != nil {
			result.ManualCommand = claudeMCPAddCommand(mcpURL)
			if !errors.Is(err, exec.ErrNotFound) {
				result.Warning = fmt.Sprintf(
					"`claude mcp add` failed (%v) -- the token was saved, but Claude Code's config was not updated; "+
						"run the command above yourself (after `claude login` if that is why it failed)", err)
			}
			return result, nil
		}
		result.ConfigWritten = claudeMCPAddCommand(mcpURL)
		return result, nil
	default:
		return nil, fmt.Errorf("unknown target client %q", target)
	}
}

// codexHome resolves ~/.codex (or $CODEX_HOME, which the codex CLI itself
// honors).
func codexHome() (string, error) {
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

const codexBlockMarker = "[mcp_servers." // shared prefix; the exact table name is render.ServerName

// codexBearerMarker is a line that appears in config.toml if and only if a
// dev-health mcp_servers table of the BEARER shape is already there
// (rendered by render.RenderCodexWithURL(Codex, Bearer, ...), which always
// includes this exact key). A bare codexBlockMarker match is not enough: an
// existing table of that name could be the OAuth variant (no
// bearer_token_env_var key at all), which this func must never mistake for
// "already wired for headless use."
var codexBearerMarker = fmt.Sprintf("bearer_token_env_var = %q", render.TokenEnvVar)

// codexTableBlock returns the text of one top-level TOML table -- from its
// "[name]" header (matched by tableMarker, e.g. "[mcp_servers.dev-health]")
// up to the next top-level "[" header on its own line, or EOF -- or nil if
// tableMarker is not present. This is a minimal slice, not a general TOML
// parser, but it is enough to answer "is a marker string inside THIS
// table", which a whole-file bytes.Contains cannot: a whole-file check
// falsely matched an unrelated [mcp_servers.other] table that happened to
// reuse the literal bearer_token_env_var = "ACR_MCP_TOKEN" line
// (cf-6235-r2 finding 3).
func codexTableBlock(doc []byte, tableMarker string) []byte {
	start := bytes.Index(doc, []byte(tableMarker))
	if start < 0 {
		return nil
	}
	rest := doc[start+len(tableMarker):]
	end := len(doc)
	if next := bytes.Index(rest, []byte("\n[")); next >= 0 {
		end = start + len(tableMarker) + next + 1 // +1: keep the "\n", not the "["
	}
	return doc[start:end]
}

// writeCodexConfig appends the rendered Codex bearer block (for mcpURL) to
// ~/.codex/config.toml if a dev-health BEARER mcp_servers table is not
// already there. It never rewrites an existing table (the user may have
// edited it) and never touches the file when a bearer table is already
// present. If a dev-health table exists but is NOT the bearer shape (e.g.
// the OAuth default, or a hand-edited table), appending would create an
// invalid duplicate TOML section -- so it leaves the file untouched and
// returns a warning instead of silently claiming the client is wired for
// the token it just saved.
func writeCodexConfig(mcpURL string) (path string, warning string, err error) {
	home, err := codexHome()
	if err != nil {
		return "", "", err
	}
	path = filepath.Join(home, "config.toml")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", "", fmt.Errorf("read %s: %w", path, err)
	}
	tableMarker := codexBlockMarker + render.ServerName + "]"
	if block := codexTableBlock(existing, tableMarker); block != nil {
		if bytes.Contains(block, []byte(codexBearerMarker)) {
			return path, "", nil // already wired for bearer; nothing to do
		}
		return path, fmt.Sprintf(
			"an existing [mcp_servers.%s] table in %s does not use a bearer token (likely the OAuth default) -- "+
				"the ACR_MCP_TOKEN env file was still written, but %s was left untouched to avoid a duplicate table; "+
				"add %q under that table by hand, or remove it and rerun",
			render.ServerName, path, path, codexBearerMarker), nil
	}
	block, err := render.RenderCodexWithURL(render.Bearer, mcpURL)
	if err != nil {
		return "", "", fmt.Errorf("render codex bearer config: %w", err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", "", fmt.Errorf("create %s: %w", home, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return "", "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		if _, err := f.WriteString("\n"); err != nil {
			return "", "", fmt.Errorf("write %s: %w", path, err)
		}
	}
	if _, err := f.WriteString("\n" + block); err != nil {
		return "", "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, "", f.Close()
}

// claudeMCPAddArgs is the exact `claude mcp add` invocation, argument by
// argument, matching render.RenderClaudeCodeAddCommandWithURL's bearer
// variant for mcpURL. The header value names ACR_MCP_TOKEN by reference
// only -- Claude Code expands it at connect time, so the literal token
// never appears in this command, in Claude Code's own config, or in this
// program's output.
func claudeMCPAddArgs(mcpURL string) []string {
	return []string{
		"mcp", "add", "--transport", "http", render.ServerName, mcpURL,
		"--header", fmt.Sprintf("Authorization: Bearer ${%s}", render.TokenEnvVar),
	}
}

// ShellQuote returns s as a single POSIX-shell single-quoted token, safe to
// paste into a shell command line regardless of its content -- spaces,
// `$`, backticks, semicolons, embedded single quotes. Every argument this
// package prints for a human to copy-paste is quoted this way unconditionally.
// A prior version quoted only when a character-class heuristic ("does it
// contain a space or $ or { or }") said an arg looked risky; a `--mcp-url`
// value containing `;` or a backtick has none of those and printed
// unquoted, making the printed instruction shell-injectable the moment
// someone ran it (cf-6235-r2 finding 1).
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func claudeMCPAddCommand(mcpURL string) string {
	args := claudeMCPAddArgs(mcpURL)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = ShellQuote(a)
	}
	return "claude " + strings.Join(quoted, " ")
}

func runClaudeMCPAdd(ctx context.Context, mcpURL string) error {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return exec.ErrNotFound
	}
	cmd := exec.CommandContext(ctx, bin, claudeMCPAddArgs(mcpURL)...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
