// Command snapshot captures the hosted MCP contract and compares captures.
//
//	snapshot capture -out FILE      capture the live server (token from env)
//	snapshot diff -old A -new B     compare; exit 10 on drift, 0 if equal
//
// The host is a compiled constant. The token comes only from the environment
// and neither headers nor bodies are ever printed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/full-chaos/context-fabric-agents/internal/snapshot"
)

const (
	// endpoint is the production hosted MCP. It is deliberately not a flag.
	endpoint = "https://mcp.fullchaos.dev"
	// tokenEnv is the only place the credential is read from.
	tokenEnv = "ACR_MCP_CI_BEARER"

	exitDrift = 10
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: snapshot capture -out FILE | snapshot diff -old A -new B")
		return 2
	}
	switch args[0] {
	case "capture":
		return capture(args[1:], stderr)
	case "diff":
		return diff(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown command %q\n", args[0])
	return 2
}

func capture(args []string, stderr interface{ Write([]byte) (int, error) }) int {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	out := fs.String("out", "", "write the snapshot here")
	if err := fs.Parse(args); err != nil || *out == "" {
		fmt.Fprintln(stderr, "capture: -out FILE is required")
		return 2
	}
	token := os.Getenv(tokenEnv)
	if token == "" {
		fmt.Fprintf(stderr, "%s missing\n", tokenEnv)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	snap, err := snapshot.Capture(ctx, endpoint, token, time.Now())
	if err != nil {
		if errors.Is(err, snapshot.ErrTokenMissing) {
			fmt.Fprintf(stderr, "%s missing\n", tokenEnv)
			return 1
		}
		fmt.Fprintf(stderr, "capture failed: %v\n", err)
		return 1
	}
	data, err := snap.Marshal()
	if err != nil {
		fmt.Fprintf(stderr, "marshal: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "write: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "captured %d tools, %d resources, %d prompts from %s\n", len(snap.Tools), len(snap.Resources), len(snap.Prompts), snap.Host)
	return 0
}

func diff(args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	oldPath := fs.String("old", "", "committed snapshot")
	newPath := fs.String("new", "", "fresh capture")
	if err := fs.Parse(args); err != nil || *oldPath == "" || *newPath == "" {
		fmt.Fprintln(stderr, "diff: -old and -new are required")
		return 2
	}
	o, err := snapshot.Load(*oldPath)
	if err != nil {
		fmt.Fprintf(stderr, "load old: %v\n", err)
		return 1
	}
	n, err := snapshot.Load(*newPath)
	if err != nil {
		fmt.Fprintf(stderr, "load new: %v\n", err)
		return 1
	}
	rep := snapshot.Compare(o, n)
	if !rep.Drifted() {
		fmt.Fprintln(stdout, "no drift")
		return 0
	}
	fmt.Fprint(stdout, rep.Markdown())
	return exitDrift
}
