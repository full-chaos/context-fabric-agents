// Package versiongate enforces that a release tag equals every plugin
// version declared in the repository (Claude Code plugin.json, marketplace
// entries, Codex plugin.json). Discovery is find-driven: it walks the tree,
// so it stays correct as client bundle directories are added or moved.
package versiongate

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var tagRe = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

// TagVersion validates a release tag (vX.Y.Z[-pre]) and returns X.Y.Z[-pre].
func TagVersion(tag string) (string, error) {
	if !tagRe.MatchString(tag) {
		return "", fmt.Errorf("tag %q is not vX.Y.Z or vX.Y.Z-pre", tag)
	}
	return strings.TrimPrefix(tag, "v"), nil
}

// Declared is one version declaration found in a manifest.
type Declared struct {
	File    string // path relative to the scan root, slash separated
	Where   string // JSON location, e.g. "version" or "plugins[0].version"
	Version string
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "testdata": true, "dist": true}

// manifestKind classifies a repo-relative slash path. Empty means "not a manifest".
func manifestKind(rel string) string {
	dir, base := filepath.ToSlash(filepath.Dir(rel)), filepath.Base(rel)
	parent := filepath.Base(dir)
	switch {
	case base == "plugin.json" && (parent == ".claude-plugin" || parent == ".codex-plugin"):
		return "plugin"
	case base == "marketplace.json" && (parent == ".claude-plugin" || strings.HasSuffix(dir, ".agents/plugins")):
		return "marketplace"
	}
	return ""
}

// Collect walks root and returns every version declaration, plus problems for
// manifests that are unreadable, malformed or (plugin.json) lack a version.
func Collect(root string) ([]Declared, []string, error) {
	var found []Declared
	var problems []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		kind := manifestKind(rel)
		if kind == "" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			problems = append(problems, fmt.Sprintf("%s: invalid JSON: %v", rel, err))
			return nil
		}
		switch kind {
		case "plugin":
			v, ok := doc["version"].(string)
			if !ok || v == "" {
				problems = append(problems, rel+": plugin manifest has no string \"version\"")
				return nil
			}
			found = append(found, Declared{rel, "version", v})
		case "marketplace":
			if md, ok := doc["metadata"].(map[string]any); ok {
				if v, ok := md["version"].(string); ok {
					found = append(found, Declared{rel, "metadata.version", v})
				}
			}
			if v, ok := doc["version"].(string); ok {
				found = append(found, Declared{rel, "version", v})
			}
			if ps, ok := doc["plugins"].([]any); ok {
				for i, p := range ps {
					if pm, ok := p.(map[string]any); ok {
						if v, ok := pm["version"].(string); ok {
							found = append(found, Declared{rel, fmt.Sprintf("plugins[%d].version", i), v})
						}
					}
				}
			}
		}
		return nil
	})
	sort.Slice(found, func(i, j int) bool {
		if found[i].File != found[j].File {
			return found[i].File < found[j].File
		}
		return found[i].Where < found[j].Where
	})
	return found, problems, err
}

// Check compares every declaration to want. minDeclared is the number of
// declarations that must exist: a scan that measured nothing must not pass.
func Check(want string, found []Declared, problems []string, minDeclared int) []string {
	out := append([]string(nil), problems...)
	if len(found) < minDeclared {
		out = append(out, fmt.Sprintf("found %d version declarations, need at least %d (nothing measured)", len(found), minDeclared))
	}
	for _, d := range found {
		if d.Version != want {
			out = append(out, fmt.Sprintf("%s: %s = %q, release version is %q", d.File, d.Where, d.Version, want))
		}
	}
	return out
}
