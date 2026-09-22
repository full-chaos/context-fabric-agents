// Package docparity checks that a config or command block embedded in a
// Markdown doc, marked with a `<!-- docparity:<path> -->` comment on its own
// line immediately before a fenced code block, matches the checked-in
// artifact at that repo-relative path byte-for-byte (trailing newline
// aside). A doc author copies real content in by hand; nothing then keeps it
// in sync with `cmd/render -write` when the renderer changes. This is the
// CHAOS-6206 acceptance gate: "a doc-parity test fails if a config block in
// the docs differs from the rendered artifact."
//
// Marker syntax, one line, exactly:
//
//	<!-- docparity:plugins/configs/claude-code.bearer.mcp.json -->
//	```json
//	{ ... byte-for-byte copy of the artifact ... }
//	```
//
// The path is repo-relative, forward-slash separated. Blank lines between
// the marker and the opening fence are allowed; nothing else is.
package docparity

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var markerRe = regexp.MustCompile(`^<!--\s*docparity:(\S+)\s*-->\s*$`)

// Mismatch is one doc block that failed its parity check, or a marker that
// could not be checked at all (which is itself a failure: a marker that
// silently checks nothing is worse than no marker).
type Mismatch struct {
	DocFile  string // repo-relative path of the Markdown file
	DocLine  int    // 1-indexed line of the marker
	Artifact string // repo-relative path the marker named
	Reason   string
}

func (m Mismatch) String() string {
	return fmt.Sprintf("%s:%d: docparity %s: %s", m.DocFile, m.DocLine, m.Artifact, m.Reason)
}

// Result is the outcome of checking one Markdown file: every Mismatch found,
// plus how many markers were seen at all (so a caller can fail loudly if a
// scan that should find markers finds none — verification rule 4: a
// measurement that did not happen must fail, not pass silently).
type Result struct {
	Mismatches []Mismatch
	Markers    int
}

// CheckFile reads root/docFile (a repo-relative Markdown path) and checks
// every docparity marker in it against root/<artifact>. An artifact path
// that cannot be read is a Mismatch (not a Go error): a stale marker
// pointing at a deleted file must show up in the same report as a content
// drift, not abort the whole scan.
func CheckFile(root, docFile string) (Result, error) {
	data, err := os.ReadFile(filepath.Join(root, docFile))
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", docFile, err)
	}
	return checkBody(root, docFile, string(data)), nil
}

func checkBody(root, docFile, body string) Result {
	var res Result
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		m := markerRe.FindStringSubmatch(strings.TrimRight(lines[i], "\r"))
		if m == nil {
			continue
		}
		res.Markers++
		artifact := m[1]
		lineNo := i + 1

		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
			res.Mismatches = append(res.Mismatches, Mismatch{docFile, lineNo, artifact, "marker is not followed by a fenced code block"})
			continue
		}

		end := -1
		for k := j + 1; k < len(lines); k++ {
			if strings.TrimSpace(lines[k]) == "```" {
				end = k
				break
			}
		}
		if end == -1 {
			res.Mismatches = append(res.Mismatches, Mismatch{docFile, lineNo, artifact, "fenced code block is never closed"})
			continue
		}

		blockContent := strings.Join(lines[j+1:end], "\n")
		data, err := os.ReadFile(filepath.Join(root, artifact))
		if err != nil {
			res.Mismatches = append(res.Mismatches, Mismatch{docFile, lineNo, artifact, "artifact cannot be read: " + err.Error()})
			continue
		}
		want := strings.TrimRight(string(data), "\n")
		got := strings.TrimRight(blockContent, "\n")
		if want != got {
			res.Mismatches = append(res.Mismatches, Mismatch{docFile, lineNo, artifact, "block content does not match the artifact on disk"})
		}
	}
	return res
}

// CheckFiles runs CheckFile over every path in docFiles and combines the
// results. It returns a Go error only when a doc file itself cannot be
// read (a measurement that did not happen); a stale or drifted marker is
// always a Mismatch, never a Go error.
func CheckFiles(root string, docFiles []string) (Result, error) {
	var total Result
	for _, f := range docFiles {
		r, err := CheckFile(root, f)
		if err != nil {
			return Result{}, err
		}
		total.Markers += r.Markers
		total.Mismatches = append(total.Mismatches, r.Mismatches...)
	}
	return total, nil
}
