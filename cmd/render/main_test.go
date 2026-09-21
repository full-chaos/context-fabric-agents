package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/full-chaos/context-fabric-agents/internal/render"
)

// scratchRoot returns a temp repo root holding the skill source and the five
// bundle directories.
func scratchRoot(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(render.SkillSource)))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(filepath.FromSlash(render.SkillSource))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(render.SkillSource)), src, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func runCmd(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestWriteThenCheck(t *testing.T) {
	root := scratchRoot(t)
	if code, _, e := runCmd("-write", "-root", root); code != 0 {
		t.Fatalf("-write exit %d: %s", code, e)
	}
	if code, out, e := runCmd("-check", "-root", root); code != 0 {
		t.Fatalf("-check exit %d: %s%s", code, out, e)
	}
}

func TestCheckFailsOnHandEditedGolden(t *testing.T) {
	root := scratchRoot(t)
	if code, _, e := runCmd("-write", "-root", root); code != 0 {
		t.Fatalf("-write exit %d: %s", code, e)
	}
	p := filepath.Join(root, filepath.FromSlash(render.ConfigPath(render.Cursor, render.Bearer)))
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(data), "mcp.fullchaos.dev", "mcp.example.com", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, e := runCmd("-check", "-root", root)
	if code != 1 || !strings.Contains(e, "differs from the renderer") {
		t.Fatalf("hand edit: exit %d, stderr %q", code, e)
	}
	// Revert: -write restores, -check is green again.
	if code, _, e := runCmd("-write", "-root", root); code != 0 {
		t.Fatalf("-write exit %d: %s", code, e)
	}
	if code, _, e := runCmd("-check", "-root", root); code != 0 {
		t.Fatalf("after revert exit %d: %s", code, e)
	}
}

func TestUsageAndMissingSkill(t *testing.T) {
	root := scratchRoot(t)
	for _, args := range [][]string{{}, {"-write", "-check", "-root", root}} {
		if code, _, _ := runCmd(args...); code != 2 {
			t.Errorf("args %v: exit %d, want 2", args, code)
		}
	}
	if code, _, _ := runCmd("-check", "-root", t.TempDir()); code != 2 {
		t.Errorf("missing skill source must exit 2, got %d", code)
	}
	// A check against a tree with no generated files is a failure, not a pass.
	if code, _, _ := runCmd("-check", "-root", root); code == 0 {
		t.Error("-check on an unrendered tree must fail")
	}
}
