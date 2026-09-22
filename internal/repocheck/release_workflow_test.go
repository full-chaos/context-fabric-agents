package repocheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	jobRe       = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`)
	privilegeRe = regexp.MustCompile(`^\s+(id-token|attestations):\s*write\s*$|^\s+contents:\s*write\s*$`)
)

// privilegedJobs maps each job name to the write privileges it declares.
func privilegedJobs(body string) map[string][]string {
	out := map[string][]string{}
	job, inJobs := "", false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "jobs:") {
			inJobs = true
			continue
		}
		if !inJobs {
			if privilegeRe.MatchString(line) {
				out["<top-level>"] = append(out["<top-level>"], strings.TrimSpace(line))
			}
			continue
		}
		if m := jobRe.FindStringSubmatch(line); m != nil {
			job = m[1]
			continue
		}
		if privilegeRe.MatchString(line) {
			out[job] = append(out[job], strings.TrimSpace(line))
		}
	}
	return out
}

func releaseProblems(body string) []string {
	var out []string
	if !strings.Contains(body, "tags: ['v*']") {
		out = append(out, "release must trigger on tags v*")
	}
	if !strings.Contains(body, "workflow_dispatch:") {
		out = append(out, "release must have a workflow_dispatch dry run")
	}
	if regexp.MustCompile(`(?m)^  workflow_dispatch:\s*\n\s+inputs:`).MatchString(body) {
		out = append(out, "workflow_dispatch must not take inputs")
	}
	got := privilegedJobs(body)
	for job, privs := range got {
		if job != "publish" {
			out = append(out, "job "+job+" holds write privilege: "+strings.Join(privs, ", "))
		}
	}
	want := map[string]bool{"contents: write": false, "id-token: write": false, "attestations: write": false}
	for _, p := range got["publish"] {
		want[strings.Join(strings.Fields(p), " ")] = true
	}
	for p, ok := range want {
		if !ok {
			out = append(out, "publish job lacks "+p)
		}
	}
	return out
}

func TestReleaseWorkflowLeastPrivilege(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range releaseProblems(string(data)) {
		t.Error(p)
	}
}

func TestReleasePolicyDetectsPlantedDefects(t *testing.T) {
	good := "on:\n  push:\n    tags: ['v*']\n  workflow_dispatch:\npermissions: {}\njobs:\n  build:\n    permissions:\n      contents: read\n  publish:\n    permissions:\n      contents: write\n      id-token: write\n      attestations: write\n"
	if got := releaseProblems(good); len(got) != 0 {
		t.Fatalf("clean workflow flagged: %v", got)
	}
	cases := map[string]string{
		"id-token on build":  strings.Replace(good, "contents: read", "id-token: write", 1),
		"top-level write":    strings.Replace(good, "permissions: {}", "permissions:\n  contents: write", 1),
		"publish loses attn": strings.Replace(good, "      attestations: write\n", "", 1),
		"no tag trigger":     strings.Replace(good, "tags: ['v*']", "branches: [main]", 1),
		"dispatch input":     strings.Replace(good, "workflow_dispatch:\n", "workflow_dispatch:\n    inputs:\n      x:\n", 1),
	}
	for name, body := range cases {
		if got := releaseProblems(body); len(got) == 0 {
			t.Errorf("%s: planted defect not detected", name)
		}
	}
}
