package l1

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/full-chaos/context-fabric-agents/internal/snapshot"
)

func editSchema(t *testing.T, s *snapshot.Snapshot, tool string, edit func(m map[string]any)) {
	t.Helper()
	for i := range s.Tools {
		if s.Tools[i].Name != tool {
			continue
		}
		raw, err := json.Marshal(s.Tools[i].InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		edit(m)
		d, err := snapshot.Digest(m)
		if err != nil {
			t.Fatal(err)
		}
		s.Tools[i].InputSchema = m
		s.Tools[i].InputSchemaDigest = d
		return
	}
	t.Fatalf("no tool %s", tool)
}

func props(m map[string]any) map[string]any { return m["properties"].(map[string]any) }

func TestProbeContractSeverity(t *testing.T) {
	rows := []struct {
		name  string
		edit  func(t *testing.T, s *snapshot.Snapshot)
		major bool
		note  string
	}{
		{"live has an extra tool", func(t *testing.T, s *snapshot.Snapshot) {
			s.Tools = s.Tools[:1]
		}, false, "tool `source_evidence` added"},
		{"live has an extra resource", func(t *testing.T, s *snapshot.Snapshot) {
			s.Resources = nil
		}, false, "resource `guide` added"},
		{"live input schema is wider (required dropped)", func(t *testing.T, s *snapshot.Snapshot) {
			editSchema(t, s, "context_for_task", func(m map[string]any) { m["required"] = []any{"goal", "mode"} })
		}, false, "input schema widened"},
		{"live input schema is wider (enum member added)", func(t *testing.T, s *snapshot.Snapshot) {
			editSchema(t, s, "context_for_task", func(m map[string]any) {
				props(m)["mode"].(map[string]any)["enum"] = []any{"a"}
			})
		}, false, "input schema widened"},
		{"live tool is gone", func(t *testing.T, s *snapshot.Snapshot) {
			s.Tools = append(s.Tools, snapshot.Tool{Name: "gone_tool", InputSchemaDigest: "sha256:00", InputSchema: map[string]any{"type": "object"}})
		}, true, "tool `gone_tool` removed or renamed"},
		{"live tool is renamed", func(t *testing.T, s *snapshot.Snapshot) {
			s.Tools[0].Name = "old_name"
		}, true, "removed or renamed"},
		{"live resource is gone", func(t *testing.T, s *snapshot.Snapshot) {
			s.Resources = append(s.Resources, snapshot.Resource{Name: "gone", URI: "acr://gone"})
		}, true, "resource `gone` removed or renamed"},
		{"live prompt is gone", func(t *testing.T, s *snapshot.Snapshot) {
			s.Prompts = append(s.Prompts, snapshot.Prompt{Name: "gone"})
		}, true, "prompt `gone` removed or renamed"},
		{"live prompt gained a required argument", func(t *testing.T, s *snapshot.Snapshot) {
			s.Prompts[0].Arguments = nil
		}, true, "gained required argument"},
		{"live input schema is narrower (required added)", func(t *testing.T, s *snapshot.Snapshot) {
			editSchema(t, s, "context_for_task", func(m map[string]any) { m["required"] = []any{} })
		}, true, "tightened"},
		{"live input schema is narrower (enum member removed)", func(t *testing.T, s *snapshot.Snapshot) {
			editSchema(t, s, "context_for_task", func(m map[string]any) {
				props(m)["mode"].(map[string]any)["enum"] = []any{"a", "b", "c"}
			})
		}, true, "tightened"},
		{"live input schema is narrower (property removed)", func(t *testing.T, s *snapshot.Snapshot) {
			editSchema(t, s, "context_for_task", func(m map[string]any) {
				props(m)["extra"] = map[string]any{"type": "string"}
			})
		}, true, "tightened"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ts := newFake(t, fakeOpts{})
			s := pin(t, ts)
			row.edit(t, s)
			var notes bytes.Buffer
			c := cfg(ts, s)
			c.Notes = &notes
			r := run(t, c)
			d := step(t, r, StepContract).Detail
			if row.major {
				wantOnlyFailing(t, r, StepContract)
				if strings.Contains(d, "ahead of the snapshot") {
					t.Errorf("major drift reported as ahead: %q", d)
				}
				return
			}
			wantOnlyFailing(t, r)
			if !strings.Contains(d, "ahead of the snapshot") {
				t.Errorf("detail = %q", d)
			}
			if !strings.Contains(notes.String(), row.note) {
				t.Errorf("notes = %q, want %q", notes.String(), row.note)
			}
		})
	}
}
