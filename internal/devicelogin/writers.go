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
// the real CLI; a temp dir in tests).
func Write(ctx context.Context, target TargetClient, dir, token string) (*WriteResult, error) {
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
		configPath, err := writeCodexConfig()
		if err != nil {
			return nil, err
		}
		return &WriteResult{EnvFile: path, ConfigWritten: configPath}, nil
	case TargetClaudeCode:
		path := EnvFilePath(dir, target)
		if err := writeEnvFile(path, token); err != nil {
			return nil, err
		}
		result := &WriteResult{EnvFile: path}
		if err := runClaudeMCPAdd(ctx); err != nil {
			if !errors.Is(err, exec.ErrNotFound) {
				return nil, fmt.Errorf("claude mcp add: %w", err)
			}
			result.ManualCommand = claudeMCPAddCommand()
			return result, nil
		}
		result.ConfigWritten = claudeMCPAddCommand()
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

// writeCodexConfig appends the rendered Codex bearer block to
// ~/.codex/config.toml if a dev-health mcp_servers table is not already
// there. It never rewrites an existing table (the user may have edited it)
// and never touches the file at all when the table is already present.
func writeCodexConfig() (string, error) {
	home, err := codexHome()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, "config.toml")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	marker := codexBlockMarker + render.ServerName + "]"
	if bytes.Contains(existing, []byte(marker)) {
		return path, nil // already wired; nothing to do
	}
	block, err := render.Render(render.Codex, render.Bearer)
	if err != nil {
		return "", fmt.Errorf("render codex bearer config: %w", err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", home, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		if _, err := f.WriteString("\n"); err != nil {
			return "", fmt.Errorf("write %s: %w", path, err)
		}
	}
	if _, err := f.WriteString("\n" + block); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, f.Close()
}

// claudeMCPAddCommand is the exact `claude mcp add` invocation, argument by
// argument, matching render.RenderClaudeCodeAddCommand's bearer variant.
// The header value names ACR_MCP_TOKEN by reference only -- Claude Code
// expands it at connect time, so the literal token never appears in this
// command, in Claude Code's own config, or in this program's output.
func claudeMCPAddArgs() []string {
	return []string{
		"mcp", "add", "--transport", "http", render.ServerName, render.RemoteURL,
		"--header", fmt.Sprintf("Authorization: Bearer ${%s}", render.TokenEnvVar),
	}
}

func claudeMCPAddCommand() string {
	args := claudeMCPAddArgs()
	quoted := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " ${}") {
			quoted[i] = "'" + a + "'"
		} else {
			quoted[i] = a
		}
	}
	return "claude " + strings.Join(quoted, " ")
}

func runClaudeMCPAdd(ctx context.Context) error {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return exec.ErrNotFound
	}
	cmd := exec.CommandContext(ctx, bin, claudeMCPAddArgs()...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
