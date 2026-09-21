package aggregate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

const legsJSON = `{
  "schema_version": "cfa.liveness.legs.v1",
  "legs": [
    {"id": "l1", "mode": "live", "description": "probe", "job": "l1", "required_steps": ["a", "b"]},
    {"id": "cursor", "mode": "static-only", "description": "config parse only"},
    {"id": "l1-trial", "mode": "declared-off", "description": "trial", "reason": "behind Access", "issue": "https://linear.app/fullchaos/issue/CHAOS-1"}
  ]
}`

const passJSON = `{
  "schema_version": "cfa.liveness.result.v1",
  "leg": "l1",
  "status": "pass",
  "host": "mcp.example.dev",
  "started_at": "2026-09-21T00:00:00Z",
  "finished_at": "2026-09-21T00:01:00Z",
  "steps": [{"id": "a", "status": "pass", "detail": "ok"}, {"id": "b", "status": "pass", "detail": "ok"}],
  "negotiated": {"2026-07-28": "2026-07-28", "2025-06-18": "2025-06-18"},
  "server_version": "1.0.0",
  "request_count": 8
}`

func legs(t *testing.T) *record.Legs {
	t.Helper()
	l, err := record.DecodeLegs([]byte(legsJSON))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func dirWith(t *testing.T, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for name, body := range files {
		p := filepath.Join(d, name)
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

var ok = map[string]Need{"l1": {Result: "success"}}

func TestGreen(t *testing.T) {
	rep := Run(legs(t), ok, dirWith(t, map[string]string{"l1.json": passJSON}))
	if !rep.Green() {
		t.Fatalf("problems: %v", rep.Problems)
	}
	md := rep.Markdown()
	for _, want := range []string{"Liveness: GREEN", "| l1 | live | l1 | success | pass | 2026-07-28, 2025-06-18 |", "| cursor | static-only |", "| l1-trial | declared-off |", "behind Access"} {
		if !strings.Contains(md, want) {
			t.Errorf("summary lacks %q:\n%s", want, md)
		}
	}
}

func TestRed(t *testing.T) {
	cases := map[string]struct {
		needs map[string]Need
		files map[string]string
		want  string
	}{
		"leg removed from workflow": {map[string]Need{}, map[string]string{"l1.json": passJSON}, `job "l1" is not in the workflow's needs`},
		"job failed":                {map[string]Need{"l1": {Result: "failure"}}, map[string]string{"l1.json": passJSON}, `result is "failure"`},
		"job skipped":               {map[string]Need{"l1": {Result: "skipped"}}, map[string]string{"l1.json": passJSON}, `result is "skipped"`},
		"job cancelled":             {map[string]Need{"l1": {Result: "cancelled"}}, map[string]string{"l1.json": passJSON}, `result is "cancelled"`},
		"no result file":            {ok, map[string]string{}, "no result file l1.json"},
		"undeclared job":            {map[string]Need{"l1": {Result: "success"}, "l9": {Result: "success"}}, map[string]string{"l1.json": passJSON}, `job "l9" is not declared`},
		"extra result file":         {ok, map[string]string{"l1.json": passJSON, "l2.json": passJSON}, `unexpected file "l2.json"`},
		"result for static leg":     {ok, map[string]string{"l1.json": passJSON, "cursor.json": passJSON}, `unexpected file "cursor.json"`},
		"stray subdirectory":        {ok, map[string]string{"l1.json": passJSON, "nested/": ""}, `unexpected file "nested/"`},
		"malformed json":            {ok, map[string]string{"l1.json": "{not json"}, "malformed l1.json"},
		"unknown field":             {ok, map[string]string{"l1.json": strings.Replace(passJSON, `"request_count": 8`, `"request_count": 8, "extra": 1`, 1)}, "malformed l1.json"},
		"trailing value":            {ok, map[string]string{"l1.json": passJSON + "{}"}, "malformed l1.json"},
		"wrong schema":              {ok, map[string]string{"l1.json": strings.Replace(passJSON, "result.v1", "result.v0", 1)}, "malformed l1.json"},
		"claims other leg":          {ok, map[string]string{"l1.json": strings.Replace(passJSON, `"leg": "l1"`, `"leg": "l2"`, 1)}, `claims leg "l2"`},
		"status fail":               {ok, map[string]string{"l1.json": strings.Replace(strings.Replace(passJSON, `"status": "pass",`+"\n  \"host\"", `"status": "fail",`+"\n  \"host\"", 1), `{"id": "b", "status": "pass"`, `{"id": "b", "status": "fail"`, 1)}, "status fail"},
		"pass with failing step":    {ok, map[string]string{"l1.json": strings.Replace(passJSON, `{"id": "b", "status": "pass"`, `{"id": "b", "status": "not_run"`, 1)}, "malformed l1.json"},
		"required step missing":     {ok, map[string]string{"l1.json": strings.Replace(passJSON, `, {"id": "b", "status": "pass", "detail": "ok"}`, "", 1)}, "required step b missing"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rep := Run(legs(t), tc.needs, dirWith(t, tc.files))
			if rep.Green() {
				t.Fatal("run is green; want red")
			}
			if !strings.Contains(strings.Join(rep.Problems, "\n"), tc.want) {
				t.Errorf("problems %v lack %q", rep.Problems, tc.want)
			}
			if !strings.Contains(rep.Markdown(), "Liveness: RED") {
				t.Error("summary not RED")
			}
		})
	}
}

func TestMissingResultsDirIsRed(t *testing.T) {
	rep := Run(legs(t), ok, filepath.Join(t.TempDir(), "absent"))
	if rep.Green() {
		t.Fatal("missing results directory read as green")
	}
}

func TestParseNeeds(t *testing.T) {
	if _, err := ParseNeeds(""); err == nil {
		t.Error("empty needs accepted")
	}
	if _, err := ParseNeeds("nope"); err == nil {
		t.Error("garbage needs accepted")
	}
	n, err := ParseNeeds(`{"l1":{"result":"success","outputs":{}}}`)
	if err != nil || n["l1"].Result != "success" {
		t.Fatalf("n=%v err=%v", n, err)
	}
}
