// Command proxy is the liveness L2 loopback recording proxy (CHAOS-6205).
//
//	go run ./liveness/proxy -listen 127.0.0.1:18765 -record "$RUNNER_TEMP/proxy-claude-code.jsonl"
//
// It prints "ready <url>" once it listens and runs until SIGINT/SIGTERM.
// The upstream (https://mcp.fullchaos.dev) is a compiled constant: there is
// no flag or environment variable for it. The listen host must be a loopback
// IP literal.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/full-chaos/context-fabric-agents/liveness/internal/proxy"
)

func main() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, sig))
}

func newFlags(stderr io.Writer) (*flag.FlagSet, *proxy.Options) {
	o := &proxy.Options{}
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.Listen, "listen", "127.0.0.1:18765", "loopback IP:port to listen on")
	fs.StringVar(&o.RecordPath, "record", "", "JSON Lines record file (one record per request)")
	fs.IntVar(&o.MaxRequests, "max-requests", 12, "forwarded request cap; later requests get a local 429")
	fs.DurationVar(&o.MinInterval, "min-interval", time.Second, "minimum spacing between forwarded requests")
	return fs, o
}

func run(args []string, stdout, stderr io.Writer, stop <-chan os.Signal) int {
	fs, opts := newFlags(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %v\n", fs.Args())
		return 2
	}
	s, err := proxy.Start(*opts)
	if err != nil {
		fmt.Fprintf(stderr, "proxy: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "ready %s -> %s\n", s.URL(), s.Upstream())
	<-stop
	if err := s.Close(); err != nil {
		fmt.Fprintf(stderr, "proxy close: %v\n", err)
		return 1
	}
	return 0
}
