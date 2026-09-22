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
	result, err := Write(context.Background(), TargetStdout, dir, "test_token_secret", render.RemoteURL)
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
	result, err := Write(context.Background(), TargetEnv, dir, "test_token_secret_value", render.RemoteURL)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.EnvFile == "" {
		t.Fatal("want EnvFile set")
	}
	content := readFile(t, result.EnvFile)
	want := "export ACR_MCP_TOKEN='test_token_secret_value'\n"
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
	if _, err := Write(context.Background(), TargetEnv, dir, "test_token_secret", render.RemoteURL); err == nil {
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

	result, err := Write(context.Background(), TargetCodex, dir, "test_token_codex_token", render.RemoteURL)
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
	if _, err := Write(context.Background(), TargetCodex, dir, "test_token_codex_token_2", render.RemoteURL); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	second := readFile(t, configPath)
	if got := strings.Count(second, "[mcp_servers."+render.ServerName+"]"); got != 1 {
		t.Errorf("mcp_servers table appears %d times after a second run, want 1:\n%s", got, second)
	}

	envContent := readFile(t, EnvFilePath(dir, TargetCodex))
	if envContent != "export ACR_MCP_TOKEN='test_token_codex_token_2'\n" {
		t.Errorf("env file not updated on the second run: %q", envContent)
	}
}

// TestWrite_Codex_UnrelatedTableSharingTheEnvVarNameStillAppends is
// cf-6235-r2 finding 3: an unrelated table that happens to reuse the
// literal `bearer_token_env_var = "ACR_MCP_TOKEN"` line must not be
// mistaken for the dev-health table -- the real table still gets appended.
func TestWrite_Codex_UnrelatedTableSharingTheEnvVarNameStillAppends(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	configPath := filepath.Join(codexHome, "config.toml")
	unrelated := "[mcp_servers.other]\nurl = \"https://other.invalid/mcp\"\n" +
		"bearer_token_env_var = \"ACR_MCP_TOKEN\"\nenabled = true\n"
	if err := os.WriteFile(configPath, []byte(unrelated), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Write(context.Background(), TargetCodex, dir, "test_token_x", render.RemoteURL)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := readFile(t, configPath)
	if !strings.HasPrefix(got, unrelated) {
		t.Errorf("the unrelated table was not preserved:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers."+render.ServerName+"]") {
		t.Errorf("dev-health table was NOT appended (falsely treated the unrelated table's env-var line as already wired):\n%s", got)
	}
	if result.ConfigWritten == "" {
		t.Error("want ConfigWritten set -- the dev-health table really was appended")
	}
	if result.Warning != "" {
		t.Errorf("Warning = %q, want empty -- this is not the ambiguous case", result.Warning)
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
	if _, err := Write(context.Background(), TargetCodex, dir, "test_token_x", render.RemoteURL); err != nil {
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

// TestWrite_Codex_UsesTheGivenMCPURL is cf-6235-r1 finding 3: a non-default
// --mcp-url (a self-hosted or trial deployment, matching docs/self-hosted.md)
// must land in the written config, not the compiled-in RemoteURL default.
func TestWrite_Codex_UsesTheGivenMCPURL(t *testing.T) {
	const custom = "https://mcp.trial.example.internal/mcp"
	dir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if _, err := Write(context.Background(), TargetCodex, dir, "test_token_x", custom); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := readFile(t, filepath.Join(codexHome, "config.toml"))
	if !strings.Contains(got, custom) {
		t.Errorf("config.toml does not contain the requested --mcp-url %q:\n%s", custom, got)
	}
	if strings.Contains(got, render.RemoteURL) {
		t.Errorf("config.toml contains the compiled-in default RemoteURL instead of the requested --mcp-url:\n%s", got)
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
	result, err := Write(context.Background(), TargetCodex, dir, "test_token_x", render.RemoteURL)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := readFile(t, configPath)
	if got != handEdited {
		t.Errorf("a pre-existing mcp_servers table must be left untouched, got:\n%s", got)
	}
	if result.EnvFile == "" {
		t.Error("want the token saved to the env file regardless")
	}
	if result.ConfigWritten != "" {
		t.Errorf("ConfigWritten = %q, want empty -- nothing was actually wired for bearer use", result.ConfigWritten)
	}
	if result.Warning == "" {
		t.Fatal("want a Warning: an existing non-bearer table must not be silently reported as wired")
	}
	if !strings.Contains(result.Warning, "bearer") {
		t.Errorf("Warning = %q, want it to explain the table is not bearer-shaped", result.Warning)
	}
}

// TestWrite_Codex_ExistingOAuthTableIsNotReportedAsWired is the reviewer's
// own scenario from cf-6235-r1 finding 1: the STANDARD rendered OAuth table
// (byte-identical to what codex/configs/config.oauth.toml ships, not a
// synthetic one) must not be reported as "wired" for the bearer token that
// was just saved.
func TestWrite_Codex_ExistingOAuthTableIsNotReportedAsWired(t *testing.T) {
	dir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	oauthBlock, err := render.Render(render.Codex, render.OAuth)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(oauthBlock), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Write(context.Background(), TargetCodex, dir, "test_token_x", render.RemoteURL)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.ConfigWritten != "" {
		t.Errorf("ConfigWritten = %q, want empty -- the OAuth table was left OAuth-only", result.ConfigWritten)
	}
	if result.Warning == "" {
		t.Fatal("want a Warning when an existing OAuth table means the saved token is not actually wired to anything")
	}
	if got := readFile(t, configPath); got != oauthBlock {
		t.Errorf("the OAuth table must be left byte-identical, got:\n%s", got)
	}
}

func TestClaudeMCPAddCommand_NeverContainsALiteralToken(t *testing.T) {
	cmd := claudeMCPAddCommand(render.RemoteURL)
	want := "claude 'mcp' 'add' '--transport' 'http' 'dev-health' 'https://mcp.fullchaos.dev/mcp' '--header' 'Authorization: Bearer ${ACR_MCP_TOKEN}'"
	if cmd != want {
		t.Errorf("claudeMCPAddCommand(render.RemoteURL) = %q, want %q", cmd, want)
	}
}

// TestShellQuote_RoundTripsThroughARealShell is cf-6235-r2 finding 1: a
// --mcp-url carrying shell metacharacters must never execute anything when
// the printed manual command is pasted into a real shell.
func TestShellQuote_RoundTripsThroughARealShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell quoting only")
	}
	dangerous := []string{
		`https://mcp.example.test/mcp?x='abc';touch /tmp/should-never-run-` + t.Name(),
		"https://mcp.example.test/mcp?x=`touch /tmp/should-never-run-backtick`",
		"a value with spaces and $ENV and ${braces}",
		`it's got an apostrophe`,
	}
	for _, d := range dangerous {
		quoted := ShellQuote(d)
		cmd := exec.Command("sh", "-c", "printf '%s' "+quoted)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("sh -c printf %s: %v", quoted, err)
		}
		if got := string(out); got != d {
			t.Errorf("ShellQuote round-trip: sh printed %q, want the original %q (quoted form: %s)", got, d, quoted)
		}
	}
}

// TestAdversarialValueThroughEverySink is the structural fix for the
// cf-6235-r1/r2/r3 shell-injection class: the SAME set of adversarial
// values is fed through EVERY sink this package writes an
// externally-supplied string (a token, or an mcp-url) into, and every one
// is proven safe -- not just the site a particular round happened to find.
// Sinks: (1) the env file's `export` line (shell-interpreted on `source`),
// (2) the printed/executed `claude mcp add` command (shell-interpreted if
// pasted), (3) the printed `source <path>` hint (shell-interpreted), (4)
// the codex config.toml `url = "..."` line (TOML-string-interpreted, not
// shell -- proven by decoding it back out, not by running a shell).
func TestAdversarialValueThroughEverySink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell quoting only")
	}
	adversarial := []string{
		`abc';touch /tmp/should-never-run-` + t.Name() + `;'`,
		"abc`touch /tmp/should-never-run-backtick`",
		"has spaces and $ENV and ${braces} and \"double quotes\"",
		`it's got an apostrophe`,
		"trailing backslash\\",
	}
	runShell := func(t *testing.T, quoted string) string {
		t.Helper()
		out, err := exec.Command("sh", "-c", "printf '%s' "+quoted).Output()
		if err != nil {
			t.Fatalf("sh -c printf %s: %v", quoted, err)
		}
		return string(out)
	}

	for _, v := range adversarial {
		t.Run("env_file/"+v, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeEnvFile(EnvFilePath(dir, TargetEnv), v); err != nil {
				t.Fatalf("writeEnvFile: %v", err)
			}
			content := readFile(t, EnvFilePath(dir, TargetEnv))
			// The file is exactly one `export NAME=<quoted>\n` line; source
			// it for real and check the shell reconstructs v.
			out, err := exec.Command("sh", "-c", content+"printf '%s' \"$"+render.TokenEnvVar+"\"").Output()
			if err != nil {
				t.Fatalf("sourcing the env file: %v (content: %q)", err, content)
			}
			if got := string(out); got != v {
				t.Errorf("env file round-trip: sh saw %q, want %q (file content: %q)", got, v, content)
			}
		})
		t.Run("claude_command/"+v, func(t *testing.T) {
			cmd := claudeMCPAddCommand(v)
			// Run the ACTUAL printed command through a real shell, with
			// `claude` replaced by a function that prints its 6th
			// argument (`mcp add --transport http dev-health <url> ...`)
			// -- this parses cmd's quoting the way a user pasting it
			// would, rather than re-parsing the quoted text ourselves.
			script := `claude() { printf '%s' "$6"; }; ` + cmd
			out, err := exec.Command("sh", "-c", script).Output()
			if err != nil {
				t.Fatalf("sh -c %q: %v", script, err)
			}
			if got := string(out); got != v {
				t.Errorf("claude command round-trip: got %q, want %q (command: %s)", got, v, cmd)
			}
		})
		t.Run("source_hint/"+v, func(t *testing.T) {
			got := runShell(t, ShellQuote(v))
			if got != v {
				t.Errorf("source-hint quoting round-trip: sh saw %q, want %q", got, v)
			}
		})
		t.Run("codex_config_url/"+v, func(t *testing.T) {
			block, err := render.RenderCodexWithURL(render.Bearer, v)
			if err != nil {
				t.Fatalf("RenderCodexWithURL: %v", err)
			}
			if got := codexTableURL([]byte(block)); got != v {
				t.Errorf("codex config url round-trip: decoded %q, want %q (block: %s)", got, v, block)
			}
		})
	}
}

func TestWrite_ClaudeCode_FallsBackToManualCommandWhenCLIMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir) // a directory with no `claude` binary
	result, err := Write(context.Background(), TargetClaudeCode, dir, "test_token_cc_token", render.RemoteURL)
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
	if got := readFile(t, result.EnvFile); got != "export ACR_MCP_TOKEN='test_token_cc_token'\n" {
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

	result, err := Write(context.Background(), TargetClaudeCode, dir, "test_token_cc_token", render.RemoteURL)
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

// TestWrite_ClaudeCode_CLIPresentButFailsFallsBackGracefully is chris's
// clarification on cf-6235-r2/r3: "you can't use a fake claude path because
// it wants a login to start" -- a real `claude` CLI that IS on PATH but
// fails (most commonly: not logged in) must be treated exactly like the
// CLI-missing case, not as a hard error. Write still succeeds (the token is
// saved either way); ManualCommand and a Warning explaining why are set.
func TestWrite_ClaudeCode_CLIPresentButFailsFallsBackGracefully(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake binary is unix-only")
	}
	dir := t.TempDir()
	fakeBinDir := t.TempDir()
	script := "#!/bin/sh\necho 'Not logged in. Run `claude login` first.' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(fakeBinDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBinDir)

	result, err := Write(context.Background(), TargetClaudeCode, dir, "test_token_x", render.RemoteURL)
	if err != nil {
		t.Fatalf("Write: %v, want success (the CLI failing must not fail Write)", err)
	}
	if result.EnvFile == "" {
		t.Error("want the token saved to the env file regardless")
	}
	if result.ConfigWritten != "" {
		t.Errorf("ConfigWritten = %q, want empty -- the CLI invocation failed", result.ConfigWritten)
	}
	if result.ManualCommand == "" {
		t.Fatal("want ManualCommand set as the fallback")
	}
	if strings.Contains(result.ManualCommand, "test_token_x") {
		t.Error("ManualCommand must never contain the literal token")
	}
	if result.Warning == "" {
		t.Error("want a Warning distinguishing this from the CLI-missing case")
	}
}

// TestWrite_Codex_ExistingBearerTableForADifferentURLIsNotReportedAsWired
// is cf-6235-r3 finding 3: an existing bearer-shaped table wired for one
// --mcp-url (e.g. prod) must not be reported as "wired" when this run
// targets a DIFFERENT one (e.g. trial) -- the marker alone is not enough;
// the url must match too.
func TestWrite_Codex_ExistingBearerTableForADifferentURLIsNotReportedAsWired(t *testing.T) {
	const oldURL = "https://mcp.fullchaos.dev/mcp"
	const newURL = "https://mcp.trial.example/mcp"
	dir := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	oldBlock, err := render.RenderCodexWithURL(render.Bearer, oldURL)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(oldBlock), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Write(context.Background(), TargetCodex, dir, "test_token_x", newURL)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := readFile(t, configPath); got != oldBlock {
		t.Errorf("the existing (old-URL) table must be left byte-identical, got:\n%s", got)
	}
	if result.ConfigWritten != "" {
		t.Errorf("ConfigWritten = %q, want empty -- the config still points at %q, not %q", result.ConfigWritten, oldURL, newURL)
	}
	if result.Warning == "" {
		t.Fatal("want a Warning: an existing table for a DIFFERENT url must not be silently reported as wired for this one")
	}
	if !strings.Contains(result.Warning, oldURL) || !strings.Contains(result.Warning, newURL) {
		t.Errorf("Warning = %q, want it to name both the existing url %q and the requested one %q", result.Warning, oldURL, newURL)
	}
}

func TestCodexTableURL(t *testing.T) {
	block, err := render.RenderCodexWithURL(render.Bearer, "https://mcp.example.test/mcp?a=b")
	if err != nil {
		t.Fatal(err)
	}
	if got := codexTableURL([]byte(block)); got != "https://mcp.example.test/mcp?a=b" {
		t.Errorf("codexTableURL = %q, want the rendered url", got)
	}
	if got := codexTableURL([]byte("[mcp_servers.dev-health]\nenabled = true\n")); got != "" {
		t.Errorf("codexTableURL with no url line = %q, want empty", got)
	}
}

// TestWrite_ClaudeCode_UsesTheGivenMCPURL is cf-6235-r1 finding 3 for the
// Claude Code writer: a non-default --mcp-url must reach `claude mcp add`,
// not the compiled-in RemoteURL default.
func TestWrite_ClaudeCode_UsesTheGivenMCPURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake binary is unix-only")
	}
	const custom = "https://mcp.trial.example.internal/mcp"
	dir := t.TempDir()
	fakeBinDir := t.TempDir()
	recorded := filepath.Join(fakeBinDir, "claude.args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + recorded + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(fakeBinDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBinDir)

	if _, err := Write(context.Background(), TargetClaudeCode, dir, "test_token_x", custom); err != nil {
		t.Fatalf("Write: %v", err)
	}
	args := readFile(t, recorded)
	if !strings.Contains(args, custom) {
		t.Errorf("claude mcp add args = %q, want the requested --mcp-url %q", args, custom)
	}
	if strings.Contains(args, render.RemoteURL) {
		t.Errorf("claude mcp add args = %q, contains the compiled-in default instead of --mcp-url", args)
	}
}
