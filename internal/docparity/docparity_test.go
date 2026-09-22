package docparity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/full-chaos/context-fabric-agents/internal/repocheck"
)

// minMarkers is a floor, not the true count: every client bundle this repo
// ships (claude-code, codex, opencode, cursor, vscode) documents at least
// one real config snippet. A scan that finds fewer did not measure the
// docs (verification rule 4).
const minMarkers = 5

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestDocsMatchArtifacts is the guard: every docparity-marked block in every
// tracked Markdown file must equal the artifact it claims to mirror, and the
// scan must have found a realistic number of markers.
func TestDocsMatchArtifacts(t *testing.T) {
	root := repoRoot(t)
	files, err := repocheck.ListFiles(root)
	if err != nil {
		t.Fatalf("cannot enumerate files (measurement did not happen): %v", err)
	}
	var mdFiles []string
	for _, f := range files {
		if filepath.Ext(f) == ".md" {
			mdFiles = append(mdFiles, f)
		}
	}
	if len(mdFiles) == 0 {
		t.Fatal("enumerated 0 Markdown files (measurement did not happen)")
	}

	res, err := CheckFiles(root, mdFiles)
	if err != nil {
		t.Fatal(err)
	}
	if res.Markers < minMarkers {
		t.Fatalf("found %d docparity markers across %d Markdown files, want at least %d (measurement did not happen)", res.Markers, len(mdFiles), minMarkers)
	}
	for _, m := range res.Mismatches {
		t.Errorf("%s", m)
	}
}

// TestDocparityDetectsPlantedDefects proves the guard can fail: a matching
// block passes, and each planted defect (content drift, missing fence,
// unclosed fence, artifact deleted) is caught.
func TestDocparityDetectsPlantedDefects(t *testing.T) {
	write := func(t *testing.T, dir, rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	const artifactContent = `{"hello":"world"}` + "\n"

	cases := []struct {
		name    string
		doc     string
		wantMin int
	}{
		{
			name: "matching block passes",
			doc: "<!-- docparity:configs/thing.json -->\n```json\n" +
				`{"hello":"world"}` + "\n```\n",
			wantMin: 0,
		},
		{
			name: "matching block with trailing blank line before fence",
			doc: "<!-- docparity:configs/thing.json -->\n\n```json\n" +
				`{"hello":"world"}` + "\n```\n",
			wantMin: 0,
		},
		{
			name: "content drift is caught",
			doc: "<!-- docparity:configs/thing.json -->\n```json\n" +
				`{"hello":"there"}` + "\n```\n",
			wantMin: 1,
		},
		{
			name:    "marker with no fence at all",
			doc:     "<!-- docparity:configs/thing.json -->\nsome prose, no fence\n",
			wantMin: 1,
		},
		{
			name: "fence never closed",
			doc: "<!-- docparity:configs/thing.json -->\n```json\n" +
				`{"hello":"world"}` + "\n",
			wantMin: 1,
		},
		{
			name: "marker points at a file that does not exist",
			doc: "<!-- docparity:configs/missing.json -->\n```json\n" +
				`{"hello":"world"}` + "\n```\n",
			wantMin: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "configs/thing.json", artifactContent)
			write(t, dir, "doc.md", c.doc)
			res, err := CheckFile(dir, "doc.md")
			if err != nil {
				t.Fatal(err)
			}
			if res.Markers != 1 {
				t.Fatalf("markers = %d, want 1", res.Markers)
			}
			if len(res.Mismatches) < c.wantMin {
				t.Fatalf("mismatches = %v, want at least %d", res.Mismatches, c.wantMin)
			}
			if c.wantMin == 0 && len(res.Mismatches) != 0 {
				t.Fatalf("clean case flagged: %v", res.Mismatches)
			}
		})
	}
}

func TestCheckFileFailsOnUnreadableDoc(t *testing.T) {
	if _, err := CheckFile(t.TempDir(), "missing.md"); err == nil {
		t.Fatal("want error for a doc file that cannot be read")
	}
}

func TestNoMarkersIsZeroNotError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("# no markers here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := CheckFile(dir, "doc.md")
	if err != nil {
		t.Fatal(err)
	}
	if res.Markers != 0 || len(res.Mismatches) != 0 {
		t.Fatalf("got %+v, want zero markers and zero mismatches", res)
	}
}
