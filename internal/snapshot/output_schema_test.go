package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const outputBase = `{"type":"object","properties":{"status":{"type":"string","enum":["complete","partial"],"description":"x"},"items":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"label":{"type":"string"}},"required":["id"]}},"note":{"type":"string"}},"required":["status","items"]}`

func loadWithOutput(t *testing.T, output string) *Snapshot {
	t.Helper()
	tool := `{"name":"t","description_digest":"sha256:d","input_schema_digest":"sha256:i","input_schema":{"type":"object"}`
	if output != "" {
		tool += `,"output_schema":` + output
	}
	raw := `{"schema_version":"mcp_tools.v1","captured_at":"x","host":"h","server_info":{"name":"n","version":"1"},"negotiations":[],"tools":[` + tool + `}],"resources":[],"prompts":[]}`
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		digest, err := Digest(s.Tools[0].OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		s.Tools[0].OutputSchemaDigest = digest
	}
	return s
}

func TestOutputSchemaSeverity(t *testing.T) {
	cases := []struct {
		name string
		next string
		want Severity
	}{
		{"identical", outputBase, None},
		{"annotation only", strings.Replace(outputBase, `"description":"x"`, `"description":"changed"`, 1), Patch},
		{"optional field added", strings.Replace(outputBase, `"note":{"type":"string"}`, `"note":{"type":"string"},"extra":{"type":"string"}`, 1), Minor},
		{"required entry added", strings.Replace(outputBase, `["status","items"]`, `["status","items","note"]`, 1), Minor},
		{"enum member added", strings.Replace(outputBase, `["complete","partial"]`, `["complete","partial","degraded"]`, 1), Minor},
		{"top-level field removed", strings.Replace(outputBase, `,"note":{"type":"string"}`, ``, 1), Major},
		{"nested field removed", strings.Replace(outputBase, `,"label":{"type":"string"}`, ``, 1), Major},
		{"required entry dropped", strings.Replace(outputBase, `["status","items"]`, `["status"]`, 1), Major},
		{"enum member removed", strings.Replace(outputBase, `["complete","partial"]`, `["complete"]`, 1), Major},
		{"type changed", strings.Replace(outputBase, `"note":{"type":"string"}`, `"note":{"type":"integer"}`, 1), Major},
	}
	for _, c := range cases {
		report := Compare(loadWithOutput(t, outputBase), loadWithOutput(t, c.next))
		if got := report.Severity(); got != c.want {
			t.Errorf("%s: severity %s, want %s\n%s", c.name, got, c.want, report.Markdown())
		}
		if c.want >= Minor && !strings.Contains(report.Markdown(), "output schema") {
			t.Errorf("%s: the report does not name the output schema:\n%s", c.name, report.Markdown())
		}
	}
}

func TestOutputSchemaPinnedAndUnpinned(t *testing.T) {
	if got := Compare(loadWithOutput(t, ""), loadWithOutput(t, outputBase)).Severity(); got != Minor {
		t.Errorf("first pin: %s, want minor", got)
	}
	if got := Compare(loadWithOutput(t, outputBase), loadWithOutput(t, "")).Severity(); got != Major {
		t.Errorf("output schema dropped: %s, want major", got)
	}
	if Compare(loadWithOutput(t, ""), loadWithOutput(t, "")).Drifted() {
		t.Error("two snapshots without output schemas must not drift")
	}
}

func TestSnapshotWithoutOutputSchemaMarshalsWithoutOutputKeys(t *testing.T) {
	raw, err := loadWithOutput(t, "").Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "output_schema") {
		t.Errorf("output keys written for a tool with no output schema:\n%s", raw)
	}
}
