package devicelogin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/full-chaos/context-fabric-agents/internal/render"
)

func TestParseTargetClient(t *testing.T) {
	for _, want := range Targets {
		got, err := ParseTargetClient(string(want))
		if err != nil || got != want {
			t.Errorf("ParseTargetClient(%q) = %q, %v", want, got, err)
		}
	}
	if _, err := ParseTargetClient("not-a-client"); err == nil {
		t.Error("want error for an unknown --client value")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(b)
}

func mustMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	return fi.Mode().Perm()
}

func TestWrite_Stdout_NoFile(t *testing.T) {
	dir := t.TempDir()
	result, err := Write(context.Background(), TargetStdout, dir, "test_token_secret")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.EnvFile != "" {
		t.Errorf("EnvFile = %q, want empty (stdout mode writes no file)", result.EnvFile)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("dir has %d entries, want 0 (nothing should be written for --client stdout)", len(entries))
	}
}

func TestWrite_Env(t *testing.T) {
	// A nested, not-yet-existing directory, matching real usage: StateDir()
	// returns a path under the user's config dir that Write creates itself
	// (MkdirAll on an ALREADY-existing dir -- like a bare t.TempDir() --
	// would not exercise the 0700 permission it sets on creation).
	dir := filepath.Join(t.TempDir(), "context-fabric-agents", "login")
	result, err := Write(context.Background(), TargetEnv, dir, "test_token_secret_value")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.EnvFile == "" {
		t.Fatal("want EnvFile set")
	}
	content := readFile(t, result.EnvFile)
	want := "export ACR_MCP_TOKEN=test_token_secret_value\n"
	if content != want {
		t.Errorf("env file content = %q, want %q", content, want)
	}
	if runtime.GOOS != "windows" {
		if mode := mustMode(t, result.EnvFile); mode != 0o600 {
			t.Errorf("env file mode = %v, want 0600", mode)
		}
		if mode := mustMode(t, filepath.Dir(result.EnvFile)); mode != 0o700 {
			t.Errorf("env dir mode = %v, want 0700", mode)
		}
	}
	if result.ConfigWritten != "" {
		t.Errorf("ConfigWritten = %q, want empty for --client env", result.ConfigWritten)
	}
}

func TestWrite_Env_RefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permission semantics differ on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real-target")
	if err := os.WriteFile(target, []byte("not a token"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := EnvFilePath(dir, TargetEnv)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(context.Background(), TargetEnv, dir, "test_token_secret"); err == nil {
		t.Fatal("want an error when the env file path is a symlink")
	}
	if got := readFile(t, target); got != "not a token" {
		t.Errorf("symlink target was written through: %q", got)
	}
}

func TestWrite_Codex_AppendsBlockOnce(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	result, err := Write(context.Background(), TargetCodex, dir, "test_token_codex_token")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if result.ConfigWritten != configPath {
		t.Errorf("ConfigWritten = %q, want %q", result.ConfigWritten, configPath)
	}
	first := readFile(t, configPath)
	if !strings.Contains(first, "[mcp_servers."+render.ServerName+"]") {
		t.Errorf("config.toml missing the mcp_servers table:\n%s", first)
	}
	if !strings.Contains(first, `bearer_token_env_var = "ACR_MCP_TOKEN"`) {
		t.Errorf("config.toml missing bearer_token_env_var:\n%s", first)
	}
	if strings.Contains(first, "test_token_codex_token") {
		t.Error("config.toml must never contain the literal token")
	}

	// Idempotent: running again must not duplicate the table.
	if _, err := Write(context.Background(), TargetCodex, dir, "test_token_codex_token_2"); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	second := readFile(t, configPath)
	if got := strings.Count(second, "[mcp_servers."+render.ServerName+"]"); got != 1 {
		t.Errorf("mcp_servers table appears %d times after a second run, want 1:\n%s", got, second)
	}

	envContent := readFile(t, EnvFilePath(dir, TargetCodex))
	if envContent != "export ACR_MCP_TOKEN=test_token_codex_token_2\n" {
		t.Errorf("env file not updated on the second run: %q", envContent)
	}
}

func TestWrite_Codex_PreservesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	configPath := filepath.Join(codexHome, "config.toml")
	preexisting := "[some_other_table]\nfoo = 1\n"
	if err := os.WriteFile(configPath, []byte(preexisting), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(context.Background(), TargetCodex, dir, "test_token_x"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := readFile(t, configPath)
	if !strings.HasPrefix(got, preexisting) {
		t.Errorf("existing config content was not preserved:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers."+render.ServerName+"]") {
		t.Errorf("mcp_servers table not appended:\n%s", got)
	}
}

func TestWrite_Codex_LeavesAManuallyEditedTableAlone(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	configPath := filepath.Join(codexHome, "config.toml")
	handEdited := "[mcp_servers." + render.ServerName + "]\nurl = \"https://example.invalid/mcp\"\n"
	if err := os.WriteFile(configPath, []byte(handEdited), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(context.Background(), TargetCodex, dir, "test_token_x"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := readFile(t, configPath)
	if got != handEdited {
		t.Errorf("a pre-existing mcp_servers table must be left untouched, got:\n%s", got)
	}
}

func TestClaudeMCPAddCommand_NeverContainsALiteralToken(t *testing.T) {
	cmd := claudeMCPAddCommand()
	want := "claude mcp add --transport http dev-health https://mcp.fullchaos.dev/mcp --header 'Authorization: Bearer ${ACR_MCP_TOKEN}'"
	if cmd != want {
		t.Errorf("claudeMCPAddCommand() = %q, want %q", cmd, want)
	}
}

func TestWrite_ClaudeCode_FallsBackToManualCommandWhenCLIMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir) // a directory with no `claude` binary
	result, err := Write(context.Background(), TargetClaudeCode, dir, "test_token_cc_token")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.ManualCommand == "" {
		t.Fatal("want ManualCommand set when `claude` is not on PATH")
	}
	if strings.Contains(result.ManualCommand, "test_token_cc_token") {
		t.Error("ManualCommand must never contain the literal token")
	}
	if result.ConfigWritten != "" {
		t.Errorf("ConfigWritten = %q, want empty when claude was never invoked", result.ConfigWritten)
	}
	if result.EnvFile == "" {
		t.Fatal("want the env file written even in the fallback path")
	}
	if got := readFile(t, result.EnvFile); got != "export ACR_MCP_TOKEN=test_token_cc_token\n" {
		t.Errorf("env file content = %q", got)
	}
}

func TestWrite_ClaudeCode_InvokesCLIWhenPresent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake binary is unix-only")
	}
	dir := t.TempDir()
	fakeBinDir := t.TempDir()
	recorded := filepath.Join(fakeBinDir, "claude.args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + recorded + "\nexit 0\n"
	fakeBin := filepath.Join(fakeBinDir, "claude")
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBinDir)

	result, err := Write(context.Background(), TargetClaudeCode, dir, "test_token_cc_token")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.ManualCommand != "" {
		t.Errorf("ManualCommand = %q, want empty when the CLI ran", result.ManualCommand)
	}
	if result.ConfigWritten == "" {
		t.Error("want ConfigWritten set when the CLI ran")
	}
	args := readFile(t, recorded)
	if !strings.Contains(args, "${ACR_MCP_TOKEN}") {
		t.Errorf("claude mcp add args = %q, want the literal ${ACR_MCP_TOKEN} reference", args)
	}
	if strings.Contains(args, "test_token_cc_token") {
		t.Error("the literal token must never be passed to `claude mcp add`")
	}
}

// Sanity check that exec.LookPath's ErrNotFound is really what an absent
// binary produces on this platform, since the fallback path depends on it.
func TestExecLookPath_ErrNotFoundSanity(t *testing.T) {
	_, err := exec.LookPath("a-binary-that-should-never-exist-xyz")
	if err == nil {
		t.Skip("unexpectedly found a binary named a-binary-that-should-never-exist-xyz")
	}
}
