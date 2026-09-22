// Package aggregate decides whether a liveness run is green. It fails
// loudly: a live leg with no job, a job that did not succeed (failure,
// skipped, cancelled), a missing, malformed or failing result record, a
// result file nobody declared, or a job nobody declared are all red.
package aggregate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

// Need is one entry of the workflow's `needs` context.
type Need struct {
	Result string `json:"result"`
}

// Row is one line of the summary table.
type Row struct {
	Leg, Mode, Job, JobResult, Status, Revision, Notes string
}

// Report is the aggregator's verdict.
type Report struct {
	Rows     []Row
	Problems []string
}

// Green reports whether the run passed.
func (r *Report) Green() bool { return len(r.Problems) == 0 }

// ParseNeeds decodes toJSON(needs).
func ParseNeeds(data string) (map[string]Need, error) {
	if strings.TrimSpace(data) == "" {
		return nil, errors.New("needs context is empty")
	}
	var n map[string]Need
	if err := json.Unmarshal([]byte(data), &n); err != nil {
		return nil, fmt.Errorf("needs context: %w", err)
	}
	return n, nil
}

// Run checks the live legs that workflow judges against the job results and
// the results directory. Live legs of another workflow are listed, not judged.
func Run(legs *record.Legs, workflow string, needs map[string]Need, resultsDir string) *Report {
	rep := &Report{}
	problem := func(format string, a ...any) { rep.Problems = append(rep.Problems, fmt.Sprintf(format, a...)) }

	liveJobs := map[string]bool{}
	expectedFiles := map[string]string{}
	if len(legs.LiveIn(workflow)) == 0 {
		problem("workflow %q judges no live leg in legs.json", workflow)
	}
	for _, g := range legs.LiveIn(workflow) {
		liveJobs[g.Job] = true
		expectedFiles[record.ResultFile(g.ID)] = g.ID
	}
	jobs := make([]string, 0, len(needs))
	for j := range needs {
		jobs = append(jobs, j)
	}
	sort.Strings(jobs)
	for _, j := range jobs {
		if !liveJobs[j] {
			problem("job %q is not declared as a live leg of %s in legs.json", j, workflow)
		}
	}

	present, err := listResults(resultsDir)
	if err != nil {
		problem("results directory: %v", err)
	}
	for _, name := range present {
		if _, ok := expectedFiles[name]; !ok {
			problem("unexpected file %q in results (only declared live legs may write a result)", name)
		}
	}
	has := map[string]bool{}
	for _, name := range present {
		has[name] = true
	}

	for _, g := range legs.Legs {
		row := Row{Leg: g.ID, Mode: g.Mode, Job: g.Job, Status: "-", Revision: "-"}
		switch g.Mode {
		case record.ModeStaticOnly:
			row.Notes = "validated offline in PR CI"
		case record.ModeDeclaredOff:
			row.Notes = g.Reason + " (" + g.Issue + ")"
		case record.ModeLive:
			if g.Workflow != workflow {
				row.Notes = "judged by " + g.Workflow
				break
			}
			checkLive(g, needs, has[record.ResultFile(g.ID)], resultsDir, &row, problem)
		}
		rep.Rows = append(rep.Rows, row)
	}
	return rep
}

func checkLive(g record.Leg, needs map[string]Need, present bool, dir string, row *Row, problem func(string, ...any)) {
	need, ok := needs[g.Job]
	switch {
	case !ok:
		row.JobResult = "absent"
		problem("live leg %s: job %q is not in the workflow's needs (leg removed from the workflow?)", g.ID, g.Job)
	case need.Result != "success":
		row.JobResult = need.Result
		problem("live leg %s: job %q result is %q, want success", g.ID, g.Job, need.Result)
	default:
		row.JobResult = need.Result
	}
	file := record.ResultFile(g.ID)
	if !present {
		row.Status = "missing"
		problem("live leg %s: no result file %s", g.ID, file)
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		row.Status = "unreadable"
		problem("live leg %s: read %s: %v", g.ID, file, err)
		return
	}
	res, err := record.DecodeResult(data)
	if err != nil {
		row.Status = "malformed"
		problem("live leg %s: malformed %s: %v", g.ID, file, err)
		return
	}
	row.Status = res.Status
	row.Revision = revisions(res.Negotiated)
	if res.Leg != g.ID {
		problem("live leg %s: %s claims leg %q", g.ID, file, res.Leg)
	}
	if res.Status != record.StatusPass {
		problem("live leg %s: status %s", g.ID, res.Status)
	}
	steps := map[string]record.Step{}
	var failed []string
	for _, s := range res.Steps {
		steps[s.ID] = s
		if s.Status != record.StatusPass {
			failed = append(failed, s.ID+"="+s.Status+" ("+s.Detail+")")
			problem("live leg %s: step %s %s: %s", g.ID, s.ID, s.Status, s.Detail)
		}
	}
	for _, id := range g.RequiredSteps {
		if _, ok := steps[id]; !ok {
			problem("live leg %s: required step %s missing from the result", g.ID, id)
			failed = append(failed, id+"=missing")
		}
	}
	if len(failed) > 0 {
		row.Notes = strings.Join(failed, "; ")
	} else {
		row.Notes = fmt.Sprintf("%d steps pass, %d requests, server %s", len(res.Steps), res.RequestCount, res.ServerVersion)
	}
}

// listResults returns every entry name in dir. A missing directory is empty
// (every live leg is then reported missing); a subdirectory is reported as
// an entry so it fails as unexpected.
func listResults(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func revisions(n map[string]string) string {
	if len(n) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(n))
	for k := range n {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, n[k])
	}
	return strings.Join(parts, ", ")
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return "-"
	}
	return s
}

// Markdown renders the job summary.
func (r *Report) Markdown() string {
	var b strings.Builder
	verdict := "GREEN"
	if !r.Green() {
		verdict = "RED"
	}
	fmt.Fprintf(&b, "## Liveness: %s\n\n", verdict)
	b.WriteString("| Leg | Mode | Job | Job result | Status | Revision | Notes |\n|---|---|---|---|---|---|---|\n")
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", cell(row.Leg), cell(row.Mode), cell(row.Job), cell(row.JobResult), cell(row.Status), cell(row.Revision), cell(row.Notes))
	}
	if len(r.Problems) > 0 {
		b.WriteString("\n### Failures\n\n")
		for _, p := range r.Problems {
			fmt.Fprintf(&b, "- %s\n", cell(p))
		}
	}
	return b.String()
}
