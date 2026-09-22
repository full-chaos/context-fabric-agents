package versiongate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture builds a tree with all three manifest shapes at version v.
func fixture(t *testing.T, claude, market, codex string) string {
	root := t.TempDir()
	write(t, root, "plugins/dev-health/.claude-plugin/plugin.json", `{"name":"dev-health","version":"`+claude+`"}`)
	write(t, root, ".claude-plugin/marketplace.json", `{"name":"m","plugins":[{"name":"dev-health","source":"./plugins/dev-health","version":"`+market+`"}]}`)
	write(t, root, "codex/.codex-plugin/plugin.json", `{"name":"dev-health","version":"`+codex+`"}`)
	// Goldens under testdata must not be scanned.
	write(t, root, "internal/render/testdata/x/.claude-plugin/plugin.json", `{"version":"9.9.9"}`)
	return root
}

func run(t *testing.T, root, tag string, min int) []string {
	t.Helper()
	want, err := TagVersion(tag)
	if err != nil {
		t.Fatal(err)
	}
	found, problems, err := Collect(root)
	if err != nil {
		t.Fatal(err)
	}
	return Check(want, found, problems, min)
}

func TestAllEqualPasses(t *testing.T) {
	root := fixture(t, "1.2.3", "1.2.3", "1.2.3")
	found, _, _ := Collect(root)
	if len(found) != 3 {
		t.Fatalf("found %d declarations, want 3 (testdata must be skipped): %v", len(found), found)
	}
	if got := run(t, root, "v1.2.3", 1); len(got) != 0 {
		t.Fatalf("unexpected problems: %v", got)
	}
}

// Failing-first pair: bumping only one manifest turns the gate red.
func TestSingleBumpedManifestFails(t *testing.T) {
	for name, tc := range map[string][3]string{
		"claude plugin": {"1.2.4", "1.2.3", "1.2.3"},
		"marketplace":   {"1.2.3", "1.2.4", "1.2.3"},
		"codex plugin":  {"1.2.3", "1.2.3", "1.2.4"},
	} {
		got := run(t, fixture(t, tc[0], tc[1], tc[2]), "v1.2.3", 1)
		if len(got) != 1 || !strings.Contains(got[0], `"1.2.4"`) {
			t.Errorf("%s: want exactly one mismatch, got %v", name, got)
		}
	}
}

func TestTagWithoutMatchingManifestsFails(t *testing.T) {
	if got := run(t, fixture(t, "1.2.3", "1.2.3", "1.2.3"), "v1.3.0", 1); len(got) != 3 {
		t.Fatalf("want 3 mismatches, got %v", got)
	}
}

func TestNothingMeasuredFails(t *testing.T) {
	if got := run(t, t.TempDir(), "v1.0.0", 1); len(got) != 1 || !strings.Contains(got[0], "nothing measured") {
		t.Fatalf("empty tree must fail: %v", got)
	}
	if got := run(t, t.TempDir(), "v1.0.0", 0); len(got) != 0 {
		t.Fatalf("min 0 must accept an empty tree: %v", got)
	}
}

func TestPluginWithoutVersionOrBadJSONFails(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/.claude-plugin/plugin.json", `{"name":"x"}`)
	write(t, root, "b/.codex-plugin/plugin.json", `{not json`)
	write(t, root, ".claude-plugin/marketplace.json", `{"plugins":[{"name":"x","version":"1.0.0"}]}`)
	if got := run(t, root, "v1.0.0", 1); len(got) != 2 {
		t.Fatalf("want 2 problems (no version, bad JSON), got %v", got)
	}
}

func TestMarketplaceMetadataAndAgentsPath(t *testing.T) {
	root := t.TempDir()
	write(t, root, "codex/.agents/plugins/marketplace.json", `{"metadata":{"version":"2.0.0"},"plugins":[{"name":"x","version":"1.0.0"}]}`)
	got := run(t, root, "v1.0.0", 1)
	if len(got) != 1 || !strings.Contains(got[0], "metadata.version") {
		t.Fatalf("want metadata.version mismatch, got %v", got)
	}
}

func TestTagVersion(t *testing.T) {
	for tag, want := range map[string]string{"v0.0.0-rc.1": "0.0.0-rc.1", "v1.2.3": "1.2.3"} {
		if got, err := TagVersion(tag); err != nil || got != want {
			t.Errorf("%s: got %q, %v", tag, got, err)
		}
	}
	for _, bad := range []string{"1.2.3", "v1.2", "v01.2.3", "vx", "v1.2.3+b", ""} {
		if _, err := TagVersion(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
