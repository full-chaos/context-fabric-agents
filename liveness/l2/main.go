// Command l2 runs one liveness L2 client leg (CHAOS-6205).
//
//	go run ./liveness/l2 -client claude-code -proxy-url http://127.0.0.1:18765/mcp \
//	    -records "$RUNNER_TEMP/proxy-claude-code.jsonl" -out results/l2-claude-code.json
//
// Run it from the repository root, with the pinned client on PATH and the
// recording proxy (liveness/proxy) listening at -proxy-url. The credential
// comes only from ACR_MCP_CI_BEARER and reaches the client as ACR_MCP_TOKEN.
// The result file is written on every run; the exit code is 0 only on pass.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/full-chaos/context-fabric-agents/internal/snapshot"
	"github.com/full-chaos/context-fabric-agents/liveness/internal/l2"
	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

const tokenEnv = "ACR_MCP_CI_BEARER"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("l2", flag.ContinueOnError)
	fs.SetOutput(stderr)
	client := fs.String("client", "", "client id: claude-code | codex | opencode-v2")
	proxyURL := fs.String("proxy-url", "", "loopback proxy endpoint, http://127.0.0.1:<port>/mcp")
	records := fs.String("records", "", "the proxy's record file")
	compatPath := fs.String("compat", "contracts/acr-mcp/compat.json", "compat.json")
	snapPath := fs.String("snapshot", "contracts/acr-mcp/snapshot.json", "committed contract snapshot (tool names)")
	out := fs.String("out", "", "result file (default results/l2-<client>.json)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *client == "" || *records == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "-client and -records are required; no positional arguments")
		return 2
	}
	if *out == "" {
		*out = filepath.Join("results", record.ResultFile(l2.LegID(*client)))
	}
	root, err := filepath.Abs(".")
	if err != nil {
		fmt.Fprintf(stderr, "repo root: %v\n", err)
		return 1
	}
	cfg := l2.Config{
		Client:      *client,
		ProxyURL:    *proxyURL,
		RecordsPath: *records,
		RepoRoot:    root,
		Token:       os.Getenv(tokenEnv),
		Path:        os.Getenv("PATH"),
		Log:         stdout,
	}
	if c, err := l2.LoadCompat(*compatPath); err != nil {
		fmt.Fprintf(stderr, "compat: %v\n", err)
	} else {
		cfg.Compat = c
	}
	if s, err := snapshot.Load(*snapPath); err != nil {
		fmt.Fprintf(stderr, "snapshot: %v\n", err)
	} else {
		for _, t := range s.Tools {
			cfg.Tools = append(cfg.Tools, t.Name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	res := l2.Run(ctx, cfg)

	data, err := res.Marshal()
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(*out), 0o755); err == nil {
			err = os.WriteFile(*out, data, 0o644)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "write result: %v\n", err)
		return 1
	}
	for _, s := range res.Steps {
		fmt.Fprintf(stdout, "%-11s %-7s %s\n", s.ID, s.Status, s.Detail)
	}
	fmt.Fprintf(stdout, "%s %s: %d requests recorded, negotiated=%v\n", res.Leg, res.Status, res.RequestCount, res.Negotiated)
	if res.Status != record.StatusPass {
		return 1
	}
	return 0
}
