// Command probe is the liveness L1 protocol probe (CHAOS-6204).
//
//	go run ./liveness/probe -snapshot contracts/acr-mcp/snapshot.json -out results/l1.json
//
// The host and the repository are compiled constants, not flags. The
// credential comes only from the environment. The result file is written
// on every run, pass or fail; the exit code is 0 only on pass.
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
	"github.com/full-chaos/context-fabric-agents/liveness/internal/l1"
	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

const (
	// endpoint is the production hosted MCP. Deliberately not a flag.
	endpoint = "https://mcp.fullchaos.dev"
	// grantedRepository is the one repository the CI credential is granted.
	grantedRepository = "full-chaos/dev-health-acr"
	// tokenEnv is the only place the credential is read from.
	tokenEnv = "ACR_MCP_CI_BEARER"

	// Per-org budget: at most 30 serial requests, at least 6 s apart
	// (a run uses about 8), so one run stays under 10 calls per minute.
	maxRequests = 30
	minInterval = 6 * time.Second
	retryWait   = 30 * time.Second
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	snapPath := fs.String("snapshot", "contracts/acr-mcp/snapshot.json", "committed contract snapshot")
	out := fs.String("out", "results/l1.json", "result file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := l1.Config{
		Endpoint:    endpoint,
		Token:       os.Getenv(tokenEnv),
		TokenName:   tokenEnv,
		Slug:        grantedRepository,
		MaxRequests: maxRequests,
		MinInterval: minInterval,
		RetryWait:   retryWait,
	}
	snap, err := snapshot.Load(*snapPath)
	if err != nil {
		fmt.Fprintf(stderr, "load snapshot: %v\n", err)
	} else {
		cfg.Snapshot = snap
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	res := l1.Run(ctx, cfg)

	if err := write(*out, res); err != nil {
		fmt.Fprintf(stderr, "write result: %v\n", err)
		return 1
	}
	for _, s := range res.Steps {
		fmt.Fprintf(stdout, "%-13s %-7s %s\n", s.ID, s.Status, s.Detail)
	}
	fmt.Fprintf(stdout, "l1 %s: %d requests, negotiated=%v, server=%s\n", res.Status, res.RequestCount, res.Negotiated, res.ServerVersion)
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
