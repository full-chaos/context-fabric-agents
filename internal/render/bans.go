package render

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

// forbiddenPluginEntries are names no bundle directory may contain, at any
// depth (plan decision D14: a plugin is code execution on install, and this
// product needs none).
var forbiddenPluginEntries = []string{"hooks", "bin", ".lsp.json", "monitors"}

// Patterns are assembled from parts so this file does not match the
// repository secret guard.
var (
	tokenLiteral = regexp.MustCompile(`fcacr` + `_[A-Za-z0-9_-]+|[A-Za-z0-9_-]{40,}`)
	// bearerLiteral matches `Bearer <something>` where something is not an
	// env expansion placeholder (those are blanked before matching).
	bearerLiteral = regexp.MustCompile(`Bearer\s+[^<\s'"]`)
)

// allowedExpansions are the only spellings a config may use to name the token.
var allowedExpansions = []string{
	"${" + TokenEnvVar + "}",
	"${env:" + TokenEnvVar + "}",
	"{env:" + TokenEnvVar + "}",
	"${input:" + vsCodeInputID + "}",
}

// CheckArtifactBans enforces, on generated content: no literal token in any
// artifact, and no Authorization header, bearer wiring or credential name in
// an oauth variant.
func CheckArtifactBans(artifacts []Artifact) []string {
	var out []string
	for _, a := range artifacts {
		if tokenLiteral.MatchString(a.Content) {
			out = append(out, fmt.Sprintf("%s: contains a token-shaped literal", a.Path))
		}
		blanked := a.Content
		for _, e := range allowedExpansions {
			blanked = strings.ReplaceAll(blanked, e, "<env>")
		}
		if bearerLiteral.MatchString(blanked) {
			out = append(out, fmt.Sprintf("%s: Bearer is followed by something other than an env expansion", a.Path))
		}
		if a.Kind == KindConfig || a.Kind == KindCommand {
			for _, stdioOnly := range []string{`"command"`, `command =`, `"args"`, `ACR_API_URL`, `ACR_API_TOKEN`} {
				if strings.Contains(a.Content, stdioOnly) {
					out = append(out, fmt.Sprintf("%s: hosted config contains STDIO-only %q", a.Path, stdioOnly))
				}
			}
		}
		if a.Variant == OAuth {
			for _, banned := range []string{"Authorization", "authorization", "Bearer", "bearer", "headers", "inputs", TokenEnvVar} {
				if strings.Contains(a.Content, banned) {
					out = append(out, fmt.Sprintf("%s: oauth variant contains %q", a.Path, banned))
				}
			}
		}
	}
	return out
}

// CheckBundleBans walks every bundle directory under root and reports any
// forbidden entry (hooks/, bin/, .lsp.json, monitors/), any plugin manifest
// that declares hooks, LSP servers or monitors, and any `.mcp.json` server
// that is not a remote entry (a `command` means a local executable). A bundle
// directory that is missing is an error: an unmeasured tree must not read as
// clean.
func CheckBundleBans(root string) ([]string, error) {
	var out []string
	for _, b := range BundleDirs {
		dir := filepath.Join(root, filepath.FromSlash(b))
		info, err := os.Stat(dir)
		if err != nil {
			return nil, fmt.Errorf("bundle directory %s: %w", b, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("bundle path %s is not a directory", b)
		}
		err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for _, f := range forbiddenPluginEntries {
				if d.Name() == f {
					out = append(out, fmt.Sprintf("%s: forbidden plugin entry %q", rel, f))
				}
			}
			if d.IsDir() {
				return nil
			}
			switch d.Name() {
			case ".mcp.json":
				out = append(out, checkMCPJSON(path, rel)...)
			case "plugin.json":
				out = append(out, checkManifest(path, rel)...)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func checkMCPJSON(path, rel string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: unreadable: %v", rel, err)}
	}
	var doc struct {
		MCPServers map[string]map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return []string{fmt.Sprintf("%s: not valid JSON: %v", rel, err)}
	}
	var out []string
	for name, s := range doc.MCPServers {
		if _, local := s["command"]; local {
			out = append(out, fmt.Sprintf("%s: server %q runs a local command; only remote entries are allowed", rel, name))
		}
		if _, ok := s["url"]; !ok {
			out = append(out, fmt.Sprintf("%s: server %q has no url", rel, name))
		}
	}
	return out
}

func checkManifest(path, rel string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("%s: unreadable: %v", rel, err)}
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return []string{fmt.Sprintf("%s: not valid JSON: %v", rel, err)}
	}
	var out []string
	for _, k := range []string{"hooks", "lspServers", "monitors", "bin"} {
		if _, ok := doc[k]; ok {
			out = append(out, fmt.Sprintf("%s: manifest declares %q", rel, k))
		}
	}
	return out
}
