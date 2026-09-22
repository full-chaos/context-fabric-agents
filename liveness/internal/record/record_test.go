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
	live := `{"id":"l1","mode":"live","description":"d","workflow":"liveness.yml","job":"l1"}`
	cases := map[string]string{
		"no legs":               `[]`,
		"no live leg":           `{"id":"c","mode":"static-only","description":"d"}`,
		"live without job":      `{"id":"l1","mode":"live","description":"d","workflow":"liveness.yml"}`,
		"live without workflow": `{"id":"l1","mode":"live","description":"d","job":"l1"}`,
		"live bad workflow":     `{"id":"l1","mode":"live","description":"d","workflow":"../x.yml","job":"l1"}`,
		"static with workflow":  live + `,{"id":"x","mode":"static-only","description":"d","workflow":"liveness.yml"}`,
		"off with workflow":     live + `,{"id":"x","mode":"declared-off","description":"d","reason":"r","issue":"https://linear.app/fullchaos/issue/CHAOS-1","workflow":"liveness.yml"}`,
		"duplicate id":          live + "," + live,
		"bad mode":              live + `,{"id":"x","mode":"sometimes","description":"d"}`,
		"old colon mode":        live + `,{"id":"x","mode":"declared-off:because","description":"d"}`,
		"off without reason":    live + `,{"id":"x","mode":"declared-off","description":"d","issue":"https://linear.app/fullchaos/issue/CHAOS-1"}`,
		"off without issue":     live + `,{"id":"x","mode":"declared-off","description":"d","reason":"r"}`,
		"off with bad issue":    live + `,{"id":"x","mode":"declared-off","description":"d","reason":"r","issue":"http://linear.app/x"}`,
		"off with other host":   live + `,{"id":"x","mode":"declared-off","description":"d","reason":"r","issue":"https://example.com/x"}`,
		"static with job":       live + `,{"id":"x","mode":"static-only","description":"d","job":"x"}`,
		"unknown field":         `{"id":"l1","mode":"live","description":"d","workflow":"liveness.yml","job":"l1","extra":true}`,
		"empty description":     `{"id":"l1","mode":"live","description":" ","workflow":"liveness.yml","job":"l1"}`,
		"uppercase id":          `{"id":"L1","mode":"live","description":"d","workflow":"liveness.yml","job":"l1"}`,
		"shared job":            live + `,{"id":"l2","mode":"live","description":"d","workflow":"liveness.yml","job":"l1"}`,
		"live with off reason":  `{"id":"l1","mode":"live","description":"d","workflow":"liveness.yml","job":"l1","reason":"r"}`,
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

// workflowJobs returns the job ids of a workflow body and the aggregate
// job's needs list.
func workflowJobs(t *testing.T, name, wf string) (jobs, needs map[string]bool) {
	t.Helper()
	jobs = map[string]bool{}
	for _, m := range jobKeyRe.FindAllStringSubmatch(wf, -1) {
		jobs[m[1]] = true
	}
	if !jobs["aggregate"] || !jobs["report"] {
		t.Fatalf("%s: aggregate/report jobs not found (found %v): measurement did not happen", name, jobs)
	}
	needsRe := regexp.MustCompile(`(?m)^  aggregate:\n(?:    .*\n)*?    needs: \[([^\]]*)\]`)
	m := needsRe.FindStringSubmatch(wf)
	if m == nil {
		t.Fatalf("%s: aggregate job has no needs list", name)
	}
	needs = map[string]bool{}
	for _, n := range strings.Split(m[1], ",") {
		needs[strings.TrimSpace(n)] = true
	}
	return jobs, needs
}

// TestWorkflowRunsEveryLiveLeg ties legs.json to the liveness workflows:
// every live leg names a workflow that has its job, whose aggregator needs
// every job, is scoped to that workflow with -workflow, and runs always;
// every aggregate need is a declared leg; each workflow is scheduled
// (L2 offset from L1), dispatch takes no inputs, and only the report job
// may write issues.
func TestWorkflowRunsEveryLiveLeg(t *testing.T) {
	root := repoRoot(t)
	l, err := LoadLegs(filepath.Join(root, "liveness", "legs.json"))
	if err != nil {
		t.Fatal(err)
	}
	byWorkflow := map[string][]Leg{}
	for _, g := range l.Live() {
		byWorkflow[g.Workflow] = append(byWorkflow[g.Workflow], g)
	}
	schedules := map[string]string{
		"liveness.yml":    "cron: '41 */6 * * *'",
		"liveness-l2.yml": "cron: '11 3-21/6 * * *'",
	}
	for _, wfName := range []string{"liveness.yml", "liveness-l2.yml"} {
		if len(byWorkflow[wfName]) == 0 {
			t.Errorf("no live leg declared for %s", wfName)
		}
	}
	for wfName, legs := range byWorkflow {
		data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", wfName))
		if err != nil {
			t.Errorf("live legs name workflow %s: %v", wfName, err)
			continue
		}
		wf := string(data)
		jobs, needs := workflowJobs(t, wfName, wf)
		declared := map[string]bool{}
		for _, g := range legs {
			declared[g.Job] = true
			if !jobs[g.Job] {
				t.Errorf("live leg %s: job %q not in %s", g.ID, g.Job, wfName)
			}
			if !needs[g.Job] {
				t.Errorf("live leg %s: aggregate in %s does not need job %q", g.ID, wfName, g.Job)
			}
		}
		for n := range needs {
			if !declared[n] {
				t.Errorf("%s: aggregate needs %q, which legs.json does not declare as a live leg of %s", wfName, n, wfName)
			}
		}
		sched, ok := schedules[wfName]
		if !ok {
			t.Errorf("%s: no schedule pinned in this test", wfName)
		}
		for _, want := range []string{
			sched,
			"  workflow_dispatch:\n",
			"\npermissions: {}\n",
			"-workflow " + wfName + " -legs liveness/legs.json",
			"if: always() && needs.aggregate.result != 'success'",
		} {
			if !strings.Contains(wf, want) {
				t.Errorf("%s lacks %q", wfName, want)
			}
		}
		if !regexp.MustCompile(`(?m)^  aggregate:\n(?:    .*\n)*?    if: always\(\)`).MatchString(wf) {
			t.Errorf("%s: aggregate job does not run always()", wfName)
		}
		if strings.Contains(wf, "workflow_dispatch:\n    inputs") {
			t.Errorf("%s: workflow_dispatch takes inputs", wfName)
		}
		if n := strings.Count(wf, "issues: write"); n != 1 {
			t.Errorf("%s: issues: write appears %d times, want exactly once (report job only)", wfName, n)
		}
	}
}
