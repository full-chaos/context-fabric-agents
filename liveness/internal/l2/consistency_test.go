package l2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

// TestMatrixIsConsistent ties the L2 matrix together offline: every live L2
// leg in legs.json is a supported client and vice versa; each client has a
// confirmed compat.json entry whose pinned_version equals both its
// package.json pin and its package-lock.json resolution; and the workflow
// job for the leg installs from that lockfile directory, runs that client and
// uploads the leg's artifact.
func TestMatrixIsConsistent(t *testing.T) {
	root := repoRoot(t)
	legs, err := record.LoadLegs(filepath.Join(root, "liveness", "legs.json"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadCompat(filepath.Join(root, "contracts", "acr-mcp", "compat.json"))
	if err != nil {
		t.Fatal(err)
	}
	wfData, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "liveness-l2.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wf := string(wfData)

	var declared []string
	for _, g := range legs.LiveIn("liveness-l2.yml") {
		if !strings.HasPrefix(g.ID, "l2-") || g.Job != g.ID {
			t.Errorf("L2 leg %s: id must be l2-<client> and equal its job (%s)", g.ID, g.Job)
		}
		declared = append(declared, strings.TrimPrefix(g.ID, "l2-"))
		if strings.Join(g.RequiredSteps, ",") != strings.Join(Steps, ",") {
			t.Errorf("L2 leg %s: required_steps %v, want %v", g.ID, g.RequiredSteps, Steps)
		}
	}
	want := Clients()
	sort.Strings(declared)
	sort.Strings(want)
	if strings.Join(declared, ",") != strings.Join(want, ",") {
		t.Fatalf("live L2 legs %v != supported clients %v", declared, want)
	}

	for _, client := range want {
		cl := clients[client]
		e, ok := c.Clients[client]
		if !ok || e.Status != "confirmed" || e.ExpectedMethod == "" || e.ExpectedRevision == "" {
			t.Errorf("%s: compat.json entry %+v must be confirmed with a method and a revision", client, e)
		}
		pin, err := PinnedVersion(root, client)
		if err != nil {
			t.Fatal(err)
		}
		if e.PinnedVersion != pin {
			t.Errorf("%s: compat.json pinned_version %q != %s pin %q", client, e.PinnedVersion, cl.pinFile, pin)
		}
		lock := filepath.Join(root, filepath.Dir(cl.pinFile), "package-lock.json")
		raw, err := os.ReadFile(lock)
		if err != nil {
			t.Fatalf("%s: %v (the leg installs with npm ci)", client, err)
		}
		var l struct {
			Packages map[string]struct {
				Version string `json:"version"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatal(err)
		}
		if got := l.Packages["node_modules/"+cl.pinPackage].Version; got != pin {
			t.Errorf("%s: package-lock.json resolves %s to %q, package.json pins %q", client, cl.pinPackage, got, pin)
		}

		job := jobBlock(t, wf, "l2-"+client)
		for _, w := range []string{
			"CLIENT: " + client + "\n",
			"PIN_DIR: " + filepath.Dir(cl.pinFile) + "\n",
			"name: liveness-result-l2-" + client + "\n",
			"path: results/l2-" + client + ".json\n",
			`npm ci --no-audit --no-fund --prefix "$PIN_DIR"`,
			`-proxy-url "http://127.0.0.1:$PORT/mcp"`,
		} {
			if !strings.Contains(job, w) {
				t.Errorf("workflow job l2-%s lacks %q", client, w)
			}
		}
	}
	// The per-run request budget stays within the rate budget (<= 30).
	total := 0
	for _, m := range regexp.MustCompile(`MAX_REQUESTS: '(\d+)'`).FindAllStringSubmatch(wf, -1) {
		n, _ := strconv.Atoi(m[1])
		total += n
	}
	if total == 0 || total > 30 {
		t.Errorf("sum of MAX_REQUESTS = %d, want 1..30", total)
	}
	// No agent-driven legs: no LLM call anywhere in the matrix (Decision 4).
	for _, bad := range []string{"claude -p", "claude --print", "codex exec", "opencode run"} {
		if strings.Contains(wf, bad) {
			t.Errorf("liveness-l2.yml contains an agent-driven command %q", bad)
		}
	}
}

// jobBlock returns the text of one top-level job.
func jobBlock(t *testing.T, wf, job string) string {
	t.Helper()
	i := strings.Index(wf, "\n  "+job+":\n")
	if i < 0 {
		t.Fatalf("job %s not in the workflow", job)
	}
	rest := wf[i+1:]
	if j := regexp.MustCompile(`\n  [a-z0-9-]+:\n`).FindStringIndex(rest[1:]); j != nil {
		return rest[:j[0]+1]
	}
	return rest
}
