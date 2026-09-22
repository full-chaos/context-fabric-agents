// Command render regenerates or verifies the client configs and skill copies.
//
//	go run ./cmd/render -write   regenerate every artifact under the repo root
//	go run ./cmd/render -check   fail (exit 1) when any committed artifact
//	                             differs from the renderer, or a ban is violated
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/full-chaos/context-fabric-agents/internal/render"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(stderr)
	write := fs.Bool("write", false, "regenerate every artifact")
	check := fs.Bool("check", false, "fail when a committed artifact differs from the renderer")
	root := fs.String("root", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *write == *check {
		fmt.Fprintln(stderr, "render: pass exactly one of -write or -check")
		return 2
	}
	artifacts, err := render.LoadArtifacts(*root)
	if err != nil {
		fmt.Fprintln(stderr, "render:", err)
		return 2
	}
	if *write {
		if err := render.Write(*root, artifacts); err != nil {
			fmt.Fprintln(stderr, "render:", err)
			return 1
		}
		fmt.Fprintf(stdout, "render: wrote %d artifacts\n", len(artifacts))
		return 0
	}
	problems, err := render.Check(*root, artifacts)
	if err != nil {
		fmt.Fprintln(stderr, "render:", err)
		return 2
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(stderr, "render: FAIL", p)
		}
		return 1
	}
	fmt.Fprintf(stdout, "render: %d artifacts match the renderer\n", len(artifacts))
	return 0
}
