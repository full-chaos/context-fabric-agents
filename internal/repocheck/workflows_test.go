package repocheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	usesRe    = regexp.MustCompile(`^\s*-?\s*uses:\s*(\S+)`)
	pinnedRe  = regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`)
	triggerRe = regexp.MustCompile(`^\s*-?\s*pull_request` + `_target\b`)
	topPermRe = regexp.MustCompile(`^permissions:`)
)

// workflowProblems returns policy violations for one workflow file body.
func workflowProblems(name, body string) []string {
	var out []string
	hasTopPerm := false
	for i, line := range strings.Split(body, "\n") {
		if topPermRe.MatchString(line) {
			hasTopPerm = true
		}
		if m := usesRe.FindStringSubmatch(line); m != nil {
			ref := strings.Trim(m[1], `"'`)
			if !strings.HasPrefix(ref, "./") && !pinnedRe.MatchString(ref) {
				out = append(out, name+":"+strconv.Itoa(i+1)+": action not pinned by full SHA")
			}
		}
		if triggerRe.MatchString(line) {
			out = append(out, name+":"+strconv.Itoa(i+1)+": forbidden trigger")
		}
	}
	if !hasTopPerm {
		out = append(out, name+": missing top-level permissions")
	}
	return out
}

func TestWorkflowsFollowPolicy(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 2 {
		t.Fatalf("found %d workflows, want at least 2 (measurement did not happen)", len(files))
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range workflowProblems(filepath.Base(f), string(data)) {
			t.Error(p)
		}
	}
}

func TestWorkflowPolicyDetectsPlantedDefects(t *testing.T) {
	good := "permissions: {}\njobs:\n  a:\n    steps:\n      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1\n"
	if got := workflowProblems("g.yml", good); len(got) != 0 {
		t.Fatalf("clean workflow flagged: %v", got)
	}
	cases := map[string]string{
		"tag pin":       strings.Replace(good, "3d3c42e5aac5ba805825da76410c181273ba90b1", "v4", 1),
		"no perms":      strings.Replace(good, "permissions: {}\n", "", 1),
		"forbidden trg": "on:\n  pull_request" + "_target:\npermissions: {}\n",
	}
	for name, body := range cases {
		if got := workflowProblems("b.yml", body); len(got) == 0 {
			t.Errorf("%s: planted defect not detected", name)
		}
	}
}
