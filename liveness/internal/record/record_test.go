package record

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func TestDecodeLegsRejects(t *testing.T) {
	base := `{"schema_version":"cfa.liveness.legs.v1","legs":[%s]}`
	live := `{"id":"l1","mode":"live","description":"d","job":"l1"}`
	cases := map[string]string{
		"no legs":              `[]`,
		"no live leg":          `{"id":"c","mode":"static-only","description":"d"}`,
		"live without job":     `{"id":"l1","mode":"live","description":"d"}`,
		"duplicate id":         live + "," + live,
		"bad mode":             live + `,{"id":"x","mode":"sometimes","description":"d"}`,
		"old colon mode":       live + `,{"id":"x","mode":"declared-off:because","description":"d"}`,
		"off without reason":   live + `,{"id":"x","mode":"declared-off","description":"d","issue":"https://linear.app/fullchaos/issue/CHAOS-1"}`,
		"off without issue":    live + `,{"id":"x","mode":"declared-off","description":"d","reason":"r"}`,
		"off with bad issue":   live + `,{"id":"x","mode":"declared-off","description":"d","reason":"r","issue":"http://linear.app/x"}`,
		"off with other host":  live + `,{"id":"x","mode":"declared-off","description":"d","reason":"r","issue":"https://example.com/x"}`,
		"static with job":      live + `,{"id":"x","mode":"static-only","description":"d","job":"x"}`,
		"unknown field":        `{"id":"l1","mode":"live","description":"d","job":"l1","extra":true}`,
		"empty description":    `{"id":"l1","mode":"live","description":" ","job":"l1"}`,
		"uppercase id":         `{"id":"L1","mode":"live","description":"d","job":"l1"}`,
		"shared job":           live + `,{"id":"l2","mode":"live","description":"d","job":"l1"}`,
		"live with off reason": `{"id":"l1","mode":"live","description":"d","job":"l1","reason":"r"}`,
	}
	for name, legs := range cases {
		body := strings.Replace(base, "%s", legs, 1)
		if name == "no legs" {
			body = `{"schema_version":"cfa.liveness.legs.v1","legs":[]}`
		}
		if _, err := DecodeLegs([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := DecodeLegs([]byte(strings.Replace(base, "%s", live, 1))); err != nil {
		t.Fatalf("valid legs rejected: %v", err)
	}
}

func TestCommittedLegsFileIsValid(t *testing.T) {
	l, err := LoadLegs(filepath.Join(repoRoot(t), "liveness", "legs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Live()) == 0 {
		t.Fatal("no live leg")
	}
}

var jobKeyRe = regexp.MustCompile(`(?m)^  ([a-z0-9][a-z0-9_-]*):\s*$`)

// TestWorkflowRunsEveryLiveLeg ties legs.json to liveness.yml: every live
// leg has a job, the aggregator needs every job and runs always, the
// workflow is scheduled every 6 h, dispatch takes no inputs, and only the
// report job may write issues.
func TestWorkflowRunsEveryLiveLeg(t *testing.T) {
	root := repoRoot(t)
	l, err := LoadLegs(filepath.Join(root, "liveness", "legs.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "liveness.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wf := string(data)
	jobs := map[string]bool{}
	for _, m := range jobKeyRe.FindAllStringSubmatch(wf, -1) {
		jobs[m[1]] = true
	}
	if !jobs["aggregate"] || !jobs["report"] {
		t.Fatalf("aggregate/report jobs not found (found %v): measurement did not happen", jobs)
	}
	needsRe := regexp.MustCompile(`(?m)^  aggregate:\n(?:    .*\n)*?    needs: \[([^\]]*)\]`)
	m := needsRe.FindStringSubmatch(wf)
	if m == nil {
		t.Fatal("aggregate job has no needs list")
	}
	needs := map[string]bool{}
	for _, n := range strings.Split(m[1], ",") {
		needs[strings.TrimSpace(n)] = true
	}
	for _, g := range l.Live() {
		if !jobs[g.Job] {
			t.Errorf("live leg %s: job %q not in liveness.yml", g.ID, g.Job)
		}
		if !needs[g.Job] {
			t.Errorf("live leg %s: aggregate does not need job %q", g.ID, g.Job)
		}
	}
	for _, want := range []string{
		"cron: '41 */6 * * *'",
		"  workflow_dispatch:\n\npermissions: {}",
		"    if: always()\n",
		"if: always() && needs.aggregate.result != 'success'",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("liveness.yml lacks %q", want)
		}
	}
	if n := strings.Count(wf, "issues: write"); n != 1 {
		t.Errorf("issues: write appears %d times, want exactly once (report job only)", n)
	}
}
