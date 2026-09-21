// Command aggregate is the fail-loud liveness aggregator (CHAOS-6204).
//
//	NEEDS='${{ toJSON(needs) }}' go run ./liveness/aggregate \
//	    -legs liveness/legs.json -results results -summary "$GITHUB_STEP_SUMMARY"
//
// Exit 0 only when every live leg's job succeeded and wrote a passing,
// well-formed result, and nothing undeclared appeared.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/full-chaos/context-fabric-agents/liveness/internal/aggregate"
	"github.com/full-chaos/context-fabric-agents/liveness/internal/record"
)

func main() { os.Exit(run(os.Args[1:], os.Getenv("NEEDS"), os.Stdout, os.Stderr)) }

func run(args []string, needsJSON string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("aggregate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	legsPath := fs.String("legs", "liveness/legs.json", "leg declarations")
	results := fs.String("results", "results", "directory holding <leg>.json result records")
	summary := fs.String("summary", "", "append the markdown summary to this file (GITHUB_STEP_SUMMARY)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	legs, err := record.LoadLegs(*legsPath)
	if err != nil {
		fmt.Fprintf(stderr, "::error::legs: %v\n", err)
		appendSummary(*summary, "## Liveness: RED\n\nlegs.json invalid: "+err.Error()+"\n", stderr)
		return 1
	}
	needs, err := aggregate.ParseNeeds(needsJSON)
	if err != nil {
		fmt.Fprintf(stderr, "::error::%v\n", err)
		appendSummary(*summary, "## Liveness: RED\n\n"+err.Error()+"\n", stderr)
		return 1
	}
	rep := aggregate.Run(legs, needs, *results)
	md := rep.Markdown()
	fmt.Fprint(stdout, md)
	appendSummary(*summary, md, stderr)
	if !rep.Green() {
		for _, p := range rep.Problems {
			fmt.Fprintf(stderr, "::error::%s\n", p)
		}
		return 1
	}
	return 0
}

func appendSummary(path, md string, stderr io.Writer) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "summary: %v\n", err)
		return
	}
	defer f.Close()
	_, _ = io.WriteString(f, md)
}
