// Command l3 is the liveness L3 OAuth discovery chain probe (CHAOS-6208).
//
//	go run ./liveness/l3 -out results/l3.json
//
// The endpoint is a compiled constant, not a flag: the whole chain runs
// unauthenticated, so no credential and no flag can change what it proves.
// The result file is written on every run, pass or fail; the exit code is 0
// only on pass.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/full-chaos/context-fabric-agents/liveness/internal/l3"
	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

// endpoint is the production hosted MCP. Deliberately not a flag.
//
// KILL-PROOF-TEMP (CHAOS-6208): pointed at a path with no PRM on purpose, to
// prove the L3 leg goes red on a real dispatch. Revert before merge.
const endpoint = "https://mcp.fullchaos.dev/kill-proof-no-prm-here"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("l3", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "results/l3.json", "result file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res := l3.Run(ctx, l3.Config{Endpoint: endpoint})

	if err := write(*out, res); err != nil {
		fmt.Fprintf(stderr, "write result: %v\n", err)
		return 1
	}
	for _, s := range res.Steps {
		fmt.Fprintf(stdout, "%-10s %-7s %s\n", s.ID, s.Status, s.Detail)
	}
	fmt.Fprintf(stdout, "l3 %s: %d requests\n", res.Status, res.RequestCount)
	if res.Status != record.StatusPass {
		return 1
	}
	return 0
}

func write(path string, res *record.Result) error {
	data, err := res.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
