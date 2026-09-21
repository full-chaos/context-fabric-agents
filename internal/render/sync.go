package render

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadArtifacts reads the skill source under root and returns every
// generated artifact.
func LoadArtifacts(root string) ([]Artifact, error) {
	skill, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(SkillSource)))
	if err != nil {
		return nil, fmt.Errorf("read skill source: %w", err)
	}
	return Artifacts(string(skill))
}

// ValidateAll validates every artifact and applies the artifact bans. Every
// problem is returned, not just the first.
func ValidateAll(artifacts []Artifact) []string {
	var problems []string
	for _, a := range artifacts {
		if err := Validate(a); err != nil {
			problems = append(problems, err.Error())
		}
	}
	return append(problems, CheckArtifactBans(artifacts)...)
}

// Write validates and writes every artifact under root. Nothing is written
// when validation fails.
func Write(root string, artifacts []Artifact) error {
	if problems := ValidateAll(artifacts); len(problems) > 0 {
		return errors.New("refusing to write invalid artifacts:\n  " + strings.Join(problems, "\n  "))
	}
	for _, a := range artifacts {
		path := filepath.Join(root, filepath.FromSlash(a.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Check compares the tree under root with the artifacts and returns every
// difference: a missing file, a file whose content differs, a file in a
// managed directory that no artifact accounts for, plus validation and ban
// problems (generated content and the bundle tree). An empty result means
// the tree is exactly what the renderer produces.
func Check(root string, artifacts []Artifact) ([]string, error) {
	var problems []string
	want := map[string]bool{}
	for _, a := range artifacts {
		want[a.Path] = true
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(a.Path)))
		if errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, fmt.Sprintf("%s: missing (run cmd/render -write)", a.Path))
			continue
		}
		if err != nil {
			return nil, err
		}
		if string(got) != a.Content {
			problems = append(problems, fmt.Sprintf("%s: differs from the renderer (%s) (run cmd/render -write)", a.Path, firstDiff(string(got), a.Content)))
		}
	}
	for _, dir := range ManagedDirs() {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if errors.Is(err, fs.ErrNotExist) {
			continue // reported per missing file above
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			p := dir + "/" + e.Name()
			if !want[p] {
				problems = append(problems, fmt.Sprintf("%s: not produced by the renderer", p))
			}
		}
	}
	problems = append(problems, ValidateAll(artifacts)...)
	bans, err := CheckBundleBans(root)
	if err != nil {
		return nil, err
	}
	problems = append(problems, bans...)
	sort.Strings(problems)
	return problems, nil
}

func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("line %d: file %q, render %q", i+1, gl, wl)
		}
	}
	return "no line differs"
}
