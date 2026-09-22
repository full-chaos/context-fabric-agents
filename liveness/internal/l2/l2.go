// Package l2 is the liveness L2 real-client connect leg (CHAOS-6205).
//
// One run drives one pinned client through the loopback recording proxy
// (liveness/internal/proxy) with the committed bearer config variant, using
// the client's own connect-only command (no LLM call), then judges:
//
//	a_install   the installed client version equals the lockfile pin and
//	            compat.json's pinned_version
//	b_render    the committed bearer config renders with only the URL
//	            changed, to the loopback proxy
//	c_connect   the client's connect-only command reports Connected
//	d_recorded  the proxy recorded the client's traffic (at least one
//	            request, the first one 2xx, no budget/rate 429, no 5xx)
//	e_compat    the recorded first method + negotiated revision equal
//	            contracts/acr-mcp/compat.json (any change, up or down, is
//	            red: "update compat.json")
//
// The credential comes only from the caller (the command reads
// ACR_MCP_CI_BEARER). Client output is redacted before it is printed and
// never copied into the result record beyond one matched status line.
package l2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/full-chaos/context-fabric-agents/liveness/internal/proxy"
	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

const (
	StepInstall  = "a_install"
	StepRender   = "b_render"
	StepConnect  = "c_connect"
	StepRecorded = "d_recorded"
	StepCompat   = "e_compat"

	// ProdURL is the URL every committed config names. Rendering replaces
	// exactly this string with the proxy URL.
	ProdURL = proxy.Upstream + proxy.Path
	// TokenEnv is the variable the committed bearer configs read.
	TokenEnv = "ACR_MCP_TOKEN"
)

// Steps is the fixed step order of an L2 result.
var Steps = []string{StepInstall, StepRender, StepConnect, StepRecorded, StepCompat}

// LegID returns the leg id (and result file stem) for a client.
func LegID(client string) string { return "l2-" + client }

// Runner runs a command. Tests replace it; production uses ExecRunner.
type Runner func(ctx context.Context, dir string, env []string, name string, args ...string) (string, error)

// ExecRunner runs name with exactly env (no inherited environment) and
// returns combined output.
func ExecRunner(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = 5 * time.Second
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// Config is one L2 run.
type Config struct {
	Client      string
	ProxyURL    string
	RecordsPath string
	RepoRoot    string
	Compat      *Compat
	// Tools are the tool names the committed snapshot lists (Codex checks them).
	Tools   []string
	Token   string
	WorkDir string
	Run     Runner
	Log     io.Writer
	Now     func() time.Time
	// Sleep is used between OpenCode list attempts.
	Sleep func(time.Duration)
	// Path is the PATH handed to the client.
	Path string
}

// CompatEntry is one client in contracts/acr-mcp/compat.json.
type CompatEntry struct {
	ExpectedMethod   string `json:"expected_method"`
	ExpectedRevision string `json:"expected_revision"`
	PinnedVersion    string `json:"pinned_version"`
	Status           string `json:"status"`
}

// Compat is contracts/acr-mcp/compat.json.
type Compat struct {
	Contract string                 `json:"contract"`
	Clients  map[string]CompatEntry `json:"clients"`
}

// LoadCompat reads compat.json strictly.
func LoadCompat(path string) (*Compat, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Compat
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// env is what a client adapter works with.
type env struct {
	cfg  Config
	home string
	proj string
	vars []string
}

func (e *env) run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := e.cfg.Run(ctx, e.proj, e.vars, name, args...)
	out = redact(out, e.cfg.Token)
	fmt.Fprintf(e.cfg.Log, "$ %s %s\n%s", name, strings.Join(args, " "), out)
	if out != "" && !strings.HasSuffix(out, "\n") {
		fmt.Fprintln(e.cfg.Log)
	}
	return out, err
}

func redact(s, token string) string {
	if token != "" {
		s = strings.ReplaceAll(s, token, "[REDACTED]")
	}
	return s
}

// client is one adapter.
type client struct {
	// pinFile is the package.json whose devDependencies pin the client.
	pinFile, pinPackage string
	binary              string
	versionArgs         []string
	versionRe           *regexp.Regexp
	// render writes the committed bearer config, pointed at proxyURL.
	render func(e *env, proxyURL string) error
	// connect runs the connect-only check; it returns the matched status line.
	connect func(ctx context.Context, e *env, proxyURL string) (string, error)
}

var clients = map[string]*client{
	"claude-code": {
		pinFile: "ci/claude-code/package.json", pinPackage: "@anthropic-ai/claude-code",
		binary: "claude", versionArgs: []string{"--version"},
		versionRe: regexp.MustCompile(`^(\S+) \(Claude Code\)\s*$`),
		render:    renderClaude, connect: connectClaude,
	},
	"codex": {
		pinFile: "ci/codex/package.json", pinPackage: "@openai/codex",
		binary: "codex", versionArgs: []string{"--version"},
		versionRe: regexp.MustCompile(`^codex-cli (\S+)\s*$`),
		render:    renderCodex, connect: connectCodex,
	},
	"opencode-v2": {
		pinFile: "ci/opencode/package.json", pinPackage: "@opencode/cli",
		binary: "opencode", versionArgs: []string{"--version"},
		versionRe: regexp.MustCompile(`^opencode v(\S+)\s*$`),
		render:    renderOpenCode, connect: connectOpenCode,
	},
}

// Clients returns the supported client ids.
func Clients() []string { return []string{"claude-code", "codex", "opencode-v2"} }

// CheckProxyURL accepts only http://<loopback IP>:<port>/mcp.
func CheckProxyURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if u.Scheme != "http" || u.Path != proxy.Path || u.RawQuery != "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("proxy URL %q must be http://<loopback IP>:<port>%s", s, proxy.Path)
	}
	return proxy.CheckListen(u.Host)
}

type run struct {
	cfg   Config
	res   *record.Result
	steps map[string]record.Step
}

func (r *run) set(id, status, format string, a ...any) {
	d := redact(fmt.Sprintf(format, a...), r.cfg.Token)
	if len(d) > 400 {
		d = d[:400] + "..."
	}
	r.steps[id] = record.Step{ID: id, Status: status, Detail: d}
}
func (r *run) pass(id, f string, a ...any) { r.set(id, record.StatusPass, f, a...) }
func (r *run) fail(id, f string, a ...any) { r.set(id, record.StatusFail, f, a...) }

// Run executes one leg and always returns a finalized result.
func Run(ctx context.Context, cfg Config) *record.Result {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	if cfg.Sleep == nil {
		cfg.Sleep = time.Sleep
	}
	if cfg.Run == nil {
		cfg.Run = ExecRunner
	}
	r := &run{cfg: cfg, steps: map[string]record.Step{}, res: &record.Result{
		SchemaVersion: record.ResultSchema,
		Leg:           LegID(cfg.Client),
		Host:          proxyHost(),
		StartedAt:     cfg.Now().UTC().Format(time.RFC3339),
		Negotiated:    map[string]string{},
	}}
	r.do(ctx)
	for _, id := range Steps {
		s, ok := r.steps[id]
		if !ok {
			s = record.Step{ID: id, Status: record.StatusNotRun, Detail: "step did not run"}
		}
		r.res.Steps = append(r.res.Steps, s)
	}
	r.res.FinishedAt = cfg.Now().UTC().Format(time.RFC3339)
	r.res.Finalize()
	return r.res
}

func proxyHost() string {
	u, _ := url.Parse(proxy.Upstream)
	return u.Host
}

func (r *run) do(ctx context.Context) {
	c, ok := clients[r.cfg.Client]
	if !ok {
		r.fail(StepInstall, "unknown client %q", r.cfg.Client)
		return
	}
	var entry CompatEntry
	haveEntry := false
	if r.cfg.Compat != nil {
		entry, haveEntry = r.cfg.Compat.Clients[r.cfg.Client]
	}
	if !haveEntry {
		r.fail(StepInstall, "compat.json has no entry for %s", r.cfg.Client)
		r.fail(StepCompat, "update compat.json: no entry for %s", r.cfg.Client)
	}

	e, err := r.newEnv()
	if err != nil {
		r.fail(StepInstall, "work dir: %v", err)
		return
	}
	defer os.RemoveAll(e.home)

	// a_install
	installed := haveEntry && r.install(ctx, c, e, entry)

	// b_render
	rendered := false
	switch {
	case r.cfg.Token == "":
		r.fail(StepRender, "credential missing: a missing secret is a red run, never a skip")
	case CheckProxyURL(r.cfg.ProxyURL) != nil:
		r.fail(StepRender, "%v", CheckProxyURL(r.cfg.ProxyURL))
	case !installed:
		r.fail(StepRender, "client not installed at the pin")
	default:
		if err := c.render(e, r.cfg.ProxyURL); err != nil {
			r.fail(StepRender, "%v", err)
		} else {
			rendered = true
			r.pass(StepRender, "committed bearer config rendered with the URL set to %s", r.cfg.ProxyURL)
		}
	}

	// c_connect
	if rendered {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		line, err := c.connect(cctx, e, r.cfg.ProxyURL)
		cancel()
		if err != nil {
			r.fail(StepConnect, "%v", err)
		} else {
			r.pass(StepConnect, "%s", line)
		}
	} else {
		r.fail(StepConnect, "not attempted: render failed")
	}

	// d_recorded and e_compat run whatever happened above: the recording is
	// evidence on its own.
	recs, err := proxy.ReadRecords(r.cfg.RecordsPath)
	if err != nil {
		r.fail(StepRecorded, "proxy records unreadable: %v", err)
		if haveEntry {
			r.fail(StepCompat, "no recording to compare")
		}
		return
	}
	r.res.RequestCount = len(recs)
	r.recorded(recs)
	if haveEntry {
		r.compat(recs, entry)
	}
}

func (r *run) newEnv() (*env, error) {
	base := r.cfg.WorkDir
	if base == "" {
		base = os.TempDir()
	}
	home, err := os.MkdirTemp(base, "l2-"+r.cfg.Client+"-")
	if err != nil {
		return nil, err
	}
	proj := filepath.Join(home, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return nil, err
	}
	vars := []string{
		"HOME=" + home,
		"PATH=" + r.cfg.Path,
		"TMPDIR=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"CODEX_HOME=" + filepath.Join(home, ".codex"),
		"DISABLE_AUTOUPDATER=1",
		"NO_PROXY=127.0.0.1,localhost,::1",
		"LANG=C.UTF-8",
	}
	if r.cfg.Token != "" {
		vars = append(vars, TokenEnv+"="+r.cfg.Token)
	}
	return &env{cfg: r.cfg, home: home, proj: proj, vars: vars}, nil
}

// pinnedVersion reads the version a ci/<client>/package.json pins.
func pinnedVersion(root, file, pkg string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return "", err
	}
	var p struct {
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return "", fmt.Errorf("%s: %w", file, err)
	}
	v := p.DevDependencies[pkg]
	if v == "" {
		return "", fmt.Errorf("%s pins no %s", file, pkg)
	}
	return v, nil
}

// PinnedVersion is the lockfile pin for a client, for tests and docs.
func PinnedVersion(root, client string) (string, error) {
	c, ok := clients[client]
	if !ok {
		return "", fmt.Errorf("unknown client %q", client)
	}
	return pinnedVersion(root, c.pinFile, c.pinPackage)
}

func (r *run) install(ctx context.Context, c *client, e *env, entry CompatEntry) bool {
	pin, err := pinnedVersion(r.cfg.RepoRoot, c.pinFile, c.pinPackage)
	if err != nil {
		r.fail(StepInstall, "pin: %v", err)
		return false
	}
	out, err := e.run(ctx, c.binary, c.versionArgs...)
	if err != nil {
		r.fail(StepInstall, "%s %s failed (client not installed?): %v", c.binary, strings.Join(c.versionArgs, " "), err)
		return false
	}
	m := c.versionRe.FindStringSubmatch(strings.TrimSpace(lastLine(out)))
	if m == nil {
		r.fail(StepInstall, "%s version output not recognised: %q", c.binary, firstN(strings.TrimSpace(out), 120))
		return false
	}
	got := m[1]
	var problems []string
	if got != pin {
		problems = append(problems, fmt.Sprintf("installed %s, lockfile pins %s", got, pin))
	}
	if entry.PinnedVersion != pin {
		problems = append(problems, fmt.Sprintf("update compat.json: pinned_version %q, lockfile pins %s", entry.PinnedVersion, pin))
	}
	if len(problems) > 0 {
		r.fail(StepInstall, "%s", strings.Join(problems, "; "))
		return false
	}
	r.pass(StepInstall, "%s %s installed from %s", c.binary, got, c.pinFile)
	return true
}

func (r *run) recorded(recs []proxy.Record) {
	if len(recs) == 0 {
		r.fail(StepRecorded, "the proxy recorded nothing: the client did not go through the loopback proxy")
		return
	}
	var problems []string
	if recs[0].Status < 200 || recs[0].Status > 299 {
		problems = append(problems, fmt.Sprintf("first request %s answered %d", recs[0].Method, recs[0].Status))
	}
	var statuses []string
	for _, rec := range recs {
		statuses = append(statuses, fmt.Sprintf("%s=%d", rec.Method, rec.Status))
		if rec.Status == 429 || rec.Status >= 500 {
			problems = append(problems, fmt.Sprintf("request %d %s answered %d", rec.Seq, rec.Method, rec.Status))
		}
	}
	if len(problems) > 0 {
		r.fail(StepRecorded, "%s (%s)", strings.Join(problems, "; "), strings.Join(statuses, ", "))
		return
	}
	r.pass(StepRecorded, "%d requests recorded: %s", len(recs), strings.Join(statuses, ", "))
}

func (r *run) compat(recs []proxy.Record, entry CompatEntry) {
	if len(recs) == 0 {
		r.fail(StepCompat, "no recording to compare")
		return
	}
	first := recs[0]
	if first.RequestedRevision != "" {
		r.res.Negotiated[first.RequestedRevision] = first.Revision
	}
	if entry.Status != "confirmed" {
		r.fail(StepCompat, "update compat.json: %s is %q; recorded first method %q at revision %q", r.cfg.Client, entry.Status, first.Method, first.Revision)
		return
	}
	if first.Method != entry.ExpectedMethod || first.Revision != entry.ExpectedRevision {
		r.fail(StepCompat, "update compat.json: recorded first method %q at revision %q; compat.json expects %q at %q",
			first.Method, first.Revision, entry.ExpectedMethod, entry.ExpectedRevision)
		return
	}
	r.pass(StepCompat, "first method %s at %s equals compat.json", first.Method, first.Revision)
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// renderURL replaces the one occurrence of ProdURL in a committed config.
func renderURL(root, rel, proxyURL string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return nil, err
	}
	if n := bytes.Count(data, []byte(ProdURL)); n != 1 {
		return nil, fmt.Errorf("%s names %s %d times, want exactly once", rel, ProdURL, n)
	}
	return bytes.Replace(data, []byte(ProdURL), []byte(proxyURL), 1), nil
}

// ---- Claude Code: `claude mcp list` / `claude mcp get dev-health`.

const claudeConfig = "plugins/configs/claude-code.bearer.mcp.json"

func renderClaude(e *env, proxyURL string) error {
	data, err := renderURL(e.cfg.RepoRoot, claudeConfig, proxyURL)
	if err != nil {
		return err
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("%s: %w", claudeConfig, err)
	}
	server, ok := cfg.MCPServers["dev-health"]
	if !ok || len(cfg.MCPServers) != 1 {
		return fmt.Errorf("%s must hold exactly the dev-health server", claudeConfig)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, server); err != nil {
		return err
	}
	out, err := e.run(context.Background(), "claude", "mcp", "add-json", "dev-health", compact.String(), "--scope", "user")
	if err != nil {
		return fmt.Errorf("claude mcp add-json: %v: %s", err, firstN(out, 200))
	}
	return nil
}

func connectClaude(ctx context.Context, e *env, proxyURL string) (string, error) {
	out, err := e.run(ctx, "claude", "mcp", "list")
	want := "dev-health: " + proxyURL + " (HTTP) - ✔ Connected"
	line := findLine(out, "dev-health: ")
	if err != nil || line != want {
		return "", fmt.Errorf("claude mcp list: want %q, got %q (err=%v)", want, line, err)
	}
	out, err = e.run(ctx, "claude", "mcp", "get", "dev-health")
	status := findLine(out, "Status: ")
	if err != nil || status != "Status: ✔ Connected" {
		return "", fmt.Errorf("claude mcp get dev-health: want %q, got %q (err=%v)", "Status: ✔ Connected", status, err)
	}
	return line + "; get: " + status, nil
}

// findLine returns the first trimmed line with the prefix, or "".
func findLine(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// ---- Codex: `codex app-server` mcpServerStatus/list via codex/proof/mcp_status.py.

const codexConfig = "codex/configs/config.bearer.toml"

func renderCodex(e *env, proxyURL string) error {
	data, err := renderURL(e.cfg.RepoRoot, codexConfig, proxyURL)
	if err != nil {
		return err
	}
	dir := filepath.Join(e.home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.toml"), data, 0o600)
}

func connectCodex(ctx context.Context, e *env, _ string) (string, error) {
	if len(e.cfg.Tools) == 0 {
		return "", errors.New("no tools from the snapshot to expect")
	}
	args := []string{filepath.Join(e.cfg.RepoRoot, "codex", "proof", "mcp_status.py"), "dev-health"}
	for _, t := range e.cfg.Tools {
		args = append(args, "--expect-tool", t)
	}
	out, err := e.run(ctx, "python3", args...)
	if err != nil {
		return "", fmt.Errorf("codex app-server mcpServerStatus/list did not connect with every snapshot tool: %v", err)
	}
	var s struct {
		Found      bool     `json:"found"`
		ToolsError *string  `json:"toolsError"`
		Tools      []string `json:"tools"`
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(lastLine(out))), &s) != nil || !s.Found || s.ToolsError != nil || s.ServerInfo.Name == "" {
		return "", errors.New("codex app-server status summary not recognised")
	}
	return fmt.Sprintf("codex app-server mcpServerStatus/list: dev-health connected, server %s, %d tools", s.ServerInfo.Name, len(s.Tools)), nil
}

// ---- OpenCode v2: `opencode mcp list` (the CLI asks its background service,
// which connects asynchronously; the first call on a cold service can list
// nothing, so the list is retried).

const openCodeConfig = "opencode/configs/opencode-v2.bearer.json"

var (
	openCodeOK   = regexp.MustCompile(`^✓\s+dev-health\s+connected\b`)
	openCodeFail = regexp.MustCompile(`^✗\s+dev-health\b`)
)

func renderOpenCode(e *env, proxyURL string) error {
	data, err := renderURL(e.cfg.RepoRoot, openCodeConfig, proxyURL)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(e.proj, "opencode.json"), data, 0o600)
}

func connectOpenCode(ctx context.Context, e *env, _ string) (string, error) {
	defer func() {
		_, _ = e.run(context.Background(), "opencode", "service", "stop")
	}()
	const attempts = 6
	var last string
	for i := 0; i < attempts; i++ {
		if i > 0 {
			e.cfg.Sleep(5 * time.Second)
		}
		out, err := e.run(ctx, "opencode", "mcp", "list")
		line := findLine(out, "✓ dev-health")
		if line == "" {
			line = findLine(out, "✗ dev-health")
		}
		switch {
		case err != nil:
			last = fmt.Sprintf("opencode mcp list: %v", err)
		case openCodeOK.MatchString(line):
			return line, nil
		case openCodeFail.MatchString(line):
			return "", fmt.Errorf("opencode mcp list: %s", line)
		default:
			last = "opencode mcp list: dev-health not listed yet"
		}
		if ctx.Err() != nil {
			break
		}
	}
	return "", fmt.Errorf("%s (after %d attempts)", last, attempts)
}
