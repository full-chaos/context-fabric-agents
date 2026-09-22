// Command opencodeschema validates one or more OpenCode config files against
// the vendor's own JSON Schema (https://opencode.ai/config.json by default).
//
// The schema is fetched fresh on every run; this repository never keeps a
// local copy, so a change to the upstream shape is caught the next time this
// runs instead of being silently vouched for by a stale pin (CHAOS-6203).
//
// This is in addition to, not instead of, the doc-derived shape checks in
// internal/render/validate.go: this program checks against opencode's own
// published schema, which internal/render does not have.
//
// Exit codes:
//
//	0  every file validates against the schema
//	1  a file is invalid JSON, or fails schema validation
//	2  the schema could not be fetched or parsed (network, HTTP status, or
//	   the document itself is not a schema). The caller decides what a 2
//	   means; it must never be treated the same as a pass.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("opencodeschema", flag.ContinueOnError)
	fs.SetOutput(stderr)
	schemaURL := fs.String("schema-url", "https://opencode.ai/config.json", "vendor JSON Schema URL to validate against")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout for fetching the schema")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "opencodeschema: usage: opencodeschema [-schema-url URL] [-timeout D] file.json [file.json ...]")
		return 2
	}

	rs, err := fetchSchema(*schemaURL, *timeout)
	if err != nil {
		fmt.Fprintf(stderr, "opencodeschema: schema unreachable: %v\n", err)
		return 2
	}

	fail := false
	for _, p := range paths {
		if err := validateFile(rs, p); err != nil {
			fmt.Fprintf(stderr, "opencodeschema: %s: FAIL: %v\n", p, err)
			fail = true
			continue
		}
		fmt.Fprintf(stdout, "opencodeschema: %s: OK\n", p)
	}
	if fail {
		return 1
	}
	return 0
}

// fetchSchema fetches and compiles the schema at rawURL. Any failure here
// (network, non-200, invalid JSON, invalid schema, an unreachable remote
// $ref anywhere in the schema) is a "schema unreachable" condition,
// distinct from a config file failing validation.
//
// The vendor schema $refs at least one further remote schema (unrelated to
// the mcp shape this program cares about); Resolve walks every $ref in the
// document eagerly, so a Loader that can fetch over HTTP is required even
// though this program only ever validates the mcp slice.
func fetchSchema(rawURL string, timeout time.Duration) (*jsonschema.Resolved, error) {
	client := &http.Client{Timeout: timeout}
	fetch := func(u string) ([]byte, error) {
		resp, err := client.Get(u)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", u, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch %s: HTTP %d", u, resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	}
	body, err := fetch(rawURL)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(body, &schema); err != nil {
		return nil, fmt.Errorf("parse schema from %s: %w", rawURL, err)
	}
	loader := func(u *url.URL) (*jsonschema.Schema, error) {
		remoteBody, err := fetch(u.String())
		if err != nil {
			return nil, err
		}
		var remote jsonschema.Schema
		if err := json.Unmarshal(remoteBody, &remote); err != nil {
			return nil, fmt.Errorf("parse remote schema %s: %w", u, err)
		}
		return &remote, nil
	}
	rs, err := schema.Resolve(&jsonschema.ResolveOptions{Loader: loader})
	if err != nil {
		return nil, fmt.Errorf("resolve schema from %s: %w", rawURL, err)
	}
	return rs, nil
}

// validateFile reads path as JSON and validates it against rs. A file that
// is not valid JSON, or that the schema rejects, is a validation failure
// (exit 1), never a "schema unreachable" (exit 2).
func validateFile(rs *jsonschema.Resolved, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var instance any
	if err := json.Unmarshal(data, &instance); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return rs.Validate(instance)
}
