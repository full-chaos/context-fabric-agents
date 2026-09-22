// Command login signs a headless or remote client (no browser of its own,
// or on a different machine than the browser that will approve) in to the
// Dev Health hosted MCP server, using an RFC 8628 device authorization
// grant (CHAOS-6233 on the server side; this is CHAOS-6235, the client-side
// deliverable that fact requires: Claude Code and Codex do not request the
// device grant for MCP themselves).
//
//	go run ./cmd/login --client codex
//	go run ./cmd/login --client claude-code
//	go run ./cmd/login --client env      # write the token only, no client config
//	go run ./cmd/login --client stdout   # print the bare token, for scripting
//
// It discovers the authorization server from the MCP endpoint's own 401
// challenge (no prior knowledge of acr needed), registers a public client
// if the server names no CIMD document to reuse, starts a device
// authorization, prints the URL to open in a browser on any machine, polls
// for approval honoring the server's interval and slow_down backoff, and
// writes the resulting bearer where the chosen client reads it. It never
// prints the token except in --client stdout mode, and never writes it to
// a file readable by anyone but the caller.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/full-chaos/context-fabric-agents/internal/devicelogin"
	"github.com/full-chaos/context-fabric-agents/internal/render"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mcpURL := fs.String("mcp-url", render.RemoteURL, "hosted MCP endpoint to sign in to")
	clientFlag := fs.String("client", "", fmt.Sprintf("where to write the token: %s", joinTargets()))
	scope := fs.String("scope", "context:read evidence:read", "space-separated OAuth scopes to request")
	timeout := fs.Duration("timeout", 15*time.Minute, "give up waiting for approval after this long (also bounded by the server's own device-code expiry)")
	insecureLoopback := fs.Bool("insecure-loopback", false, "allow http:// (never https) for --mcp-url and every discovered endpoint, ONLY on a loopback host (127.0.0.1/::1/localhost) -- for local testing against a dev server; a bearer token is never sent over plain http anywhere else")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: login --client <codex|claude-code|env|stdout> [--mcp-url URL] [--scope \"s1 s2\"] [--timeout 15m] [--insecure-loopback]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "login: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *clientFlag == "" {
		fmt.Fprintln(stderr, "login: --client is required")
		fs.Usage()
		return 2
	}
	target, err := devicelogin.ParseTargetClient(*clientFlag)
	if err != nil {
		fmt.Fprintln(stderr, "login:", err)
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, *timeout)
	defer cancelTimeout()

	client := &devicelogin.Client{InsecureLoopback: *insecureLoopback}

	fmt.Fprintf(stderr, "login: discovering authorization server for %s ...\n", *mcpURL)
	discovery, err := client.Discover(ctx, *mcpURL)
	if err != nil {
		fmt.Fprintln(stderr, "login: discovery failed:", err)
		return 1
	}
	fmt.Fprintf(stderr, "login: authorization server %s\n", discovery.Issuer)

	fmt.Fprintln(stderr, "login: registering a client (no prior credential is used or required) ...")
	clientID, err := client.Register(ctx, discovery.RegistrationEndpoint, "context-fabric-agents-login")
	if err != nil {
		return reportAPIError(stderr, "client registration", err)
	}

	device, err := client.StartDeviceAuthorization(ctx, discovery.DeviceAuthorizationEndpoint, clientID, *scope, *mcpURL)
	if err != nil {
		return reportAPIError(stderr, "device_authorization", err)
	}

	fmt.Fprintln(stderr)
	if device.VerificationURIComplete != "" {
		fmt.Fprintf(stderr, "  Open this URL in a browser on ANY machine to approve:\n\n    %s\n\n", device.VerificationURIComplete)
	} else {
		fmt.Fprintf(stderr, "  Open this URL in a browser on ANY machine:\n\n    %s\n\n  and enter this code when asked:\n\n    %s\n\n", device.VerificationURI, device.UserCode)
	}
	fmt.Fprintf(stderr, "  Waiting for approval (expires in %s) ...\n", device.ExpiresIn.Round(time.Second))

	deadline := time.Now().Add(device.ExpiresIn)
	token, err := client.Poll(ctx, discovery.TokenEndpoint, device.DeviceCode, clientID, device.Interval, deadline, func(p devicelogin.PollProgress) {
		if p.Attempt%6 == 0 { // avoid spamming; ~ every 30-60s at the default interval
			fmt.Fprintln(stderr, "  still waiting ...")
		}
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintln(stderr, "login: timed out waiting for approval")
			return 1
		}
		return reportAPIError(stderr, "token", err)
	}
	fmt.Fprintln(stderr, "login: approved")

	// StateDir resolves $HOME/$XDG_CONFIG_HOME, which can fail on a minimal
	// headless/CI box -- resolve it only for the targets that actually need
	// a directory. --client stdout writes no file at all (Write's own
	// TargetStdout case returns immediately), so it must not fail here,
	// after approval already happened, over a directory it will never use.
	var dir string
	if target != devicelogin.TargetStdout {
		dir, err = devicelogin.StateDir()
		if err != nil {
			fmt.Fprintln(stderr, "login:", err)
			return 1
		}
	}
	result, err := devicelogin.Write(ctx, target, dir, token.AccessToken, *mcpURL)
	if err != nil {
		fmt.Fprintln(stderr, "login: writing the credential failed:", err)
		return 1
	}

	if target == devicelogin.TargetStdout {
		fmt.Fprintln(stdout, token.AccessToken)
		return 0
	}
	if result.EnvFile != "" {
		fmt.Fprintf(stderr, "login: wrote %s (%s=<token>, mode 0600)\n", result.EnvFile, render.TokenEnvVar)
	}
	if result.ConfigWritten != "" {
		fmt.Fprintf(stderr, "login: wired %s\n", result.ConfigWritten)
	}
	if result.ManualCommand != "" {
		fmt.Fprintf(stderr, "login: %q not found on PATH; run this yourself:\n\n    %s\n\n", string(target), result.ManualCommand)
	}
	if result.Warning != "" {
		fmt.Fprintf(stderr, "login: WARNING: %s\n", result.Warning)
	}
	if result.EnvFile != "" {
		fmt.Fprintf(stderr, "login: before starting %s, run: source %s\n", target, devicelogin.ShellQuote(result.EnvFile))
	}
	return 0
}

// reportAPIError prints the exact acr-api/oauth error code (never a
// generic wrapper message) and returns the process's exit code.
func reportAPIError(stderr io.Writer, step string, err error) int {
	var apiErr *devicelogin.APIError
	if errors.As(err, &apiErr) {
		fmt.Fprintf(stderr, "login: %s refused: %s\n", step, apiErr.Error())
		return 1
	}
	fmt.Fprintf(stderr, "login: %s failed: %v\n", step, err)
	return 1
}

func joinTargets() string {
	names := make([]string, len(devicelogin.Targets))
	for i, t := range devicelogin.Targets {
		names[i] = string(t)
	}
	return strings.Join(names, "|")
}
