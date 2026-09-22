// Command versiongate fails when a release version differs from any plugin
// version in the tree. Usage: versiongate [-root .] [-allow-none] <tag|version>
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/full-chaos/context-fabric-agents/internal/versiongate"
)

func main() {
	root := flag.String("root", ".", "repository root to scan")
	allowNone := flag.Bool("allow-none", false, "accept zero declarations (only while no client bundle exists yet)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: versiongate [-root .] [-allow-none] <vX.Y.Z[-pre]>")
		os.Exit(2)
	}
	arg := flag.Arg(0)
	if !strings.HasPrefix(arg, "v") {
		arg = "v" + arg
	}
	want, err := versiongate.TagVersion(arg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "versiongate:", err)
		os.Exit(2)
	}
	found, problems, err := versiongate.Collect(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "versiongate:", err)
		os.Exit(2)
	}
	min := 1
	if *allowNone {
		min = 0
		if len(found) == 0 {
			fmt.Println("::warning::versiongate: no plugin manifests found; gate passed vacuously (-allow-none)")
		}
	}
	for _, d := range found {
		fmt.Printf("%s %s = %s\n", d.File, d.Where, d.Version)
	}
	bad := versiongate.Check(want, found, problems, min)
	for _, p := range bad {
		fmt.Printf("::error::versiongate: %s\n", p)
	}
	if len(bad) > 0 {
		os.Exit(1)
	}
	fmt.Printf("versiongate: %d declarations all equal %s\n", len(found), want)
}
