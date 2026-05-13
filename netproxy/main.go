// ccode-netproxy is a host-side network proxy that the macOS sandbox routes
// all traffic through. It runs OUTSIDE the sandbox, listening on loopback;
// the sandbox profile is configured to deny network egress except to these
// loopback ports.
//
// Two server protocols are supported in parallel:
//
//   - HTTP CONNECT proxy: handles HTTPS (via opaque CONNECT tunnels) and
//     plain HTTP forwarding. Used by curl, git, npm, pip, requests, Node,
//     and anything else that honours HTTP_PROXY/HTTPS_PROXY.
//   - SOCKS5 proxy: handles arbitrary TCP for clients that speak SOCKS
//     (set via ALL_PROXY=socks5h://…), notably ssh and database tools.
//
// Both proxies share a single Policy (loaded from a JSON file) and reject
// connections to hosts that don't match the allowlist. There is no TLS
// termination (no MITM) — filtering is at host-name granularity only. A
// future iteration may add MITM for content-level rules (e.g. per-method
// allowlists for specific APIs).
//
// Output (stdout, single line, key=value):
//
//	HTTP_PORT=<port> SOCKS_PORT=<port> POLICY=<name>
//
// Stderr gets human-readable startup + per-request "ALLOW"/"DENY" log lines.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(os.Stderr)

	var (
		policyPath = flag.String("policy", "", "path to the policy JSON file (required)")
		bindHost   = flag.String("bind", "127.0.0.1", "loopback interface to bind on")
	)
	flag.Parse()

	if *policyPath == "" {
		fatal("--policy is required")
	}

	policy, err := loadPolicy(*policyPath)
	if err != nil {
		fatal("load policy: %v", err)
	}
	log.Printf("policy: %q (%d allowed, %d denied)", policy.Name,
		len(policy.AllowedDomains), len(policy.DeniedDomains))

	httpLn, err := startHTTPProxy(policy, net.JoinHostPort(*bindHost, "0"))
	if err != nil {
		fatal("start HTTP proxy: %v", err)
	}
	socksLn, err := startSOCKSProxy(policy, net.JoinHostPort(*bindHost, "0"))
	if err != nil {
		fatal("start SOCKS proxy: %v", err)
	}

	httpPort := httpLn.Addr().(*net.TCPAddr).Port
	socksPort := socksLn.Addr().(*net.TCPAddr).Port

	// Machine-readable line on stdout for the launcher to parse. Sync so
	// the parent (waiting on read) sees it before we block on accept loops.
	fmt.Printf("HTTP_PORT=%d SOCKS_PORT=%d POLICY=%s\n", httpPort, socksPort, policy.Name)
	_ = os.Stdout.Sync()
	log.Printf("HTTP proxy on %s, SOCKS proxy on %s", httpLn.Addr(), socksLn.Addr())

	// Graceful shutdown on SIGTERM/SIGINT — the launcher kills us when
	// the sandbox exits.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		s := <-sig
		log.Printf("shutdown: signal %s", s)
		_ = httpLn.Close()
		_ = socksLn.Close()
		cancel()
	}()
	<-ctx.Done()
}

// bidirectionalCopy pipes bytes in both directions and returns when either
// side closes. The first close stops both copies cleanly (no leaked
// goroutines). Used by both HTTP CONNECT and SOCKS5 CONNECT.
func bidirectionalCopy(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	closeBoth := func() {
		_ = a.Close()
		_ = b.Close()
	}
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		closeBoth()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		closeBoth()
	}()
	wg.Wait()
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ccode-netproxy: "+strings.TrimRight(format, "\n")+"\n", args...)
	os.Exit(1)
}
