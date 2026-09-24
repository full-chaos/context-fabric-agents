package snapshot

import (
	"encoding/json"
	"strings"
	"testing"
)

func schema(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func snapWith(t *testing.T, name, sch string) *Snapshot {
	t.Helper()
	v := schema(t, sch)
	d, _ := Digest(v)
	s := &Snapshot{SchemaVersion: SchemaVersion, Host: "h", ServerInfo: ServerInfo{Name: "n", Version: "1"},
		Negotiations: []Negotiation{{"2026-07-28", "server/discover", "2026-07-28"}, {"2025-06-18", "initialize", "2025-06-18"}},
		Tools:        []Tool{{Name: name, InputSchemaDigest: d, InputSchema: v}}}
	s.Normalize()
	return s
}

const base = `{"type":"object","properties":{"a":{"type":"string","description":"x"},"b":{"type":"integer"}},"required":["a"]}`

func TestSchemaSeverity(t *testing.T) {
	cases := []struct {
		name string
		next string
		want Severity
	}{
		{"identical", base, None},
		{"annotation only", strings.Replace(base, `"description":"x"`, `"description":"changed"`, 1), Patch},
		{"optional property added", strings.Replace(base, `"b":{"type":"integer"}`, `"b":{"type":"integer"},"c":{"type":"string"}`, 1), Minor},
		{"required removed (widened)", strings.Replace(base, `,"required":["a"]`, ``, 1), Minor},
		{"required added (tightened)", strings.Replace(base, `["a"]`, `["a","b"]`, 1), Major},
		{"new required property", strings.Replace(strings.Replace(base, `"b":{"type":"integer"}`, `"b":{"type":"integer"},"c":{"type":"string"}`, 1), `["a"]`, `["a","c"]`, 1), Major},
		{"property removed", strings.Replace(base, `,"b":{"type":"integer"}`, ``, 1), Major},
		{"type changed", strings.Replace(base, `"b":{"type":"integer"}`, `"b":{"type":"string"}`, 1), Major},
		{"constraint added", strings.Replace(base, `"b":{"type":"integer"}`, `"b":{"type":"integer","maximum":5}`, 1), Major},
		{"additionalProperties false added", strings.Replace(base, `"required"`, `"additionalProperties":false,"required"`, 1), Major},
	}
	for _, c := range cases {
		got := Compare(snapWith(t, "t", base), snapWith(t, "t", c.next)).Severity()
		if got != c.want {
			t.Errorf("%s: severity %s, want %s", c.name, got, c.want)
		}
	}
}

func TestEnumSeverity(t *testing.T) {
	e := func(vals string) *Snapshot {
		return snapWith(t, "t", `{"type":"object","properties":{"k":{"enum":[`+vals+`]}}}`)
	}
	if got := Compare(e(`"a","b"`), e(`"a"`)).Severity(); got != Major {
		t.Errorf("enum shrink: %s", got)
	}
	if got := Compare(e(`"a"`), e(`"a","b"`)).Severity(); got != Minor {
		t.Errorf("enum grow: %s", got)
	}
}

func TestToolAddRemoveRename(t *testing.T) {
	old := snapWith(t, "old_name", base)
	renamed := snapWith(t, "new_name", base)
	rep := Compare(old, renamed)
	if rep.Severity() != Major {
		t.Fatalf("rename severity %s", rep.Severity())
	}
	var removed, added bool
	for _, c := range rep.Changes {
		removed = removed || strings.Contains(c.Text, "`old_name` removed")
		added = added || strings.Contains(c.Text, "`new_name` added")
	}
	if !removed || !added {
		t.Errorf("rename must show as removal + addition: %s", rep.Markdown())
	}
	grown := snapWith(t, "old_name", base)
	grown.Tools = append(grown.Tools, Tool{Name: "extra", InputSchemaDigest: "sha256:x", InputSchema: map[string]any{}})
	if got := Compare(old, grown).Severity(); got != Minor {
		t.Errorf("tool added: %s", got)
	}
}

func TestPromptAndResourceSeverity(t *testing.T) {
	mk := func() *Snapshot {
		s := snapWith(t, "t", base)
		s.Resources = []Resource{{"guide", "acr://guide"}}
		s.Prompts = []Prompt{{Name: "p", Arguments: []PromptArgument{{"q", true}}}}
		return s
	}
	o := mk()
	n := mk()
	n.Resources = nil
	if got := Compare(o, n).Severity(); got != Major {
		t.Errorf("resource removed: %s", got)
	}
	n = mk()
	n.Prompts[0].Arguments = append(n.Prompts[0].Arguments, PromptArgument{"w", true})
	if got := Compare(o, n).Severity(); got != Major {
		t.Errorf("required prompt arg added: %s", got)
	}
	n = mk()
	n.Prompts[0].Arguments = append(n.Prompts[0].Arguments, PromptArgument{"w", false})
	if got := Compare(o, n).Severity(); got != Minor {
		t.Errorf("optional prompt arg added: %s", got)
	}
	n = mk()
	n.Prompts[0].Arguments = nil
	if got := Compare(o, n).Severity(); got != Major {
		t.Errorf("prompt arg removed: %s", got)
	}
}

func TestNegotiatedRevisionChangeIsMajorEitherWay(t *testing.T) {
	o := snapWith(t, "t", base)
	for _, rev := range []string{"2025-11-25", "2099-01-01"} {
		n := snapWith(t, "t", base)
		n.Negotiations[1].Negotiated = rev
		if got := Compare(o, n).Severity(); got != Major {
			t.Errorf("legacy path negotiated %s: %s, want major", rev, got)
		}
	}
}

func TestServerVersionAndCaptureTimeAreNotDrift(t *testing.T) {
	o := snapWith(t, "t", base)
	n := snapWith(t, "t", base)
	o.CapturedAt, n.CapturedAt = "2026-01-01T00:00:00Z", "2026-02-02T00:00:00Z"
	if Compare(o, n).Drifted() {
		t.Fatal("captured_at must not count as drift")
	}
	n.ServerInfo.Version = "2"
	if rep := Compare(o, n); rep.Drifted() {
		t.Fatalf("server version must not count as drift: %s", rep.Markdown())
	}
	n.ServerInfo.Title = "other"
	if Compare(o, n).Severity() != Patch {
		t.Fatal("server title change must still be patch drift")
	}
}

func TestMarshalIsCanonicalAndStable(t *testing.T) {
	a := snapWith(t, "t", `{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"string"}}}`)
	b := snapWith(t, "t", `{"properties":{"a":{"type":"string"},"z":{"type":"string"}},"type":"object"}`)
	ma, _ := a.Marshal()
	mb, _ := b.Marshal()
	if string(ma) != string(mb) {
		t.Fatalf("key order leaked into output:\n%s\n%s", ma, mb)
	}
	if strings.Index(string(ma), `"a"`) > strings.Index(string(ma), `"z"`) {
		t.Errorf("keys not sorted:\n%s", ma)
	}
	if !strings.HasSuffix(string(ma), "}\n") {
		t.Error("missing trailing newline")
	}
}
