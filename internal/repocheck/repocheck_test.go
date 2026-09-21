package repocheck

import (
	"os"
	"path/filepath"
	"testing"
)

const minFiles = 10 // a scan that saw almost nothing did not measure the repo

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRepoHasNoForbiddenLiterals is the guard: it fails on any credential
// literal or absolute local path in the working tree, and fails if it
// could not enumerate files.
func TestRepoHasNoForbiddenLiterals(t *testing.T) {
	root := repoRoot(t)
	files, err := ListFiles(root)
	if err != nil {
		t.Fatalf("cannot enumerate files (measurement did not happen): %v", err)
	}
	if len(files) < minFiles {
		t.Fatalf("enumerated %d files, want at least %d (measurement did not happen)", len(files), minFiles)
	}
	findings, err := ScanFiles(root, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		t.Errorf("forbidden content: %s", f)
	}
}

// TestScannerDetectsPlantedDefects proves the guard can fail: each planted
// defect must be found, and the clean control must not be.
func TestScannerDetectsPlantedDefects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"fcacr literal", "token = " + "fcacr" + "_abc123\n", 1},
		{"bearer literal", "Authorization: Bearer " + "abcdefghijklmnopqrstuvwxyz012345\n", 1},
		{"local path", "cd /home" + "/someone/project\n", 1},
		{"env var bearer is allowed", "Authorization: Bearer ${ACR_MCP_TOKEN}\n", 0},
		{"opencode env bearer is allowed", "Authorization: Bearer {env:ACR_MCP_TOKEN}\n", 0},
		{"prose is allowed", "Use a Bearer token from the environment.\n", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := ScanFiles(dir, []string{"f.txt"})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != c.want {
				t.Fatalf("findings = %d (%v), want %d", len(got), got, c.want)
			}
		})
	}
}

func TestScanFilesFailsOnUnreadableFile(t *testing.T) {
	if _, err := ScanFiles(t.TempDir(), []string{"missing.txt"}); err == nil {
		t.Fatal("want error for a file that cannot be read")
	}
}
