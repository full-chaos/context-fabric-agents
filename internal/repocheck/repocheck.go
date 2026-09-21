// Package repocheck holds repository-wide guards for this public repo:
// no credential literals and no absolute local paths in tracked files.
package repocheck

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Finding is one forbidden pattern hit. It never carries the matched text,
// so a failing run does not print a secret into CI logs.
type Finding struct {
	File string
	Line int
	Rule string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Rule)
}

type rule struct {
	name string
	re   *regexp.Regexp
}

// Patterns are assembled from parts so this file does not match itself.
var rules = []rule{
	{"fcacr_ token literal", regexp.MustCompile(`fcacr` + `_[A-Za-z0-9]`)},
	{"Bearer literal credential", regexp.MustCompile(`Bearer` + `\s+[A-Za-z0-9._~+/=-]{20,}`)},
	{"absolute local path", regexp.MustCompile(`(/home/[a-z_][a-z0-9_-]*/|/Users/[A-Za-z][A-Za-z0-9._-]*/)`)},
}

// ScanFiles scans the named files (relative to root). A file it cannot read
// is an error: an unmeasured file must not read as clean.
func ScanFiles(root string, files []string) ([]Finding, error) {
	var out []Finding
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		if bytes.IndexByte(data, 0) >= 0 {
			continue // binary
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, r := range rules {
				if r.re.MatchString(line) {
					out = append(out, Finding{File: rel, Line: i + 1, Rule: r.name})
				}
			}
		}
	}
	return out, nil
}

// ListFiles returns tracked plus untracked-not-ignored files under root.
func ListFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, f := range strings.Split(string(raw), "\x00") {
		if f == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			continue // deleted in working tree
		}
		files = append(files, f)
	}
	return files, nil
}
