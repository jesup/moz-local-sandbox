package main

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// startProxiesForTest spins up the HTTP and SOCKS proxies with the given
// policy on auto-allocated ports and returns "http://host:port" + "host:port"
// strings plus a cleanup function.
func startProxiesForTest(t *testing.T, p *Policy) (httpAddr, socksHostPort string, cleanup func()) {
	t.Helper()
	httpLn, err := startHTTPProxy(p, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start http proxy: %v", err)
	}
	socksLn, err := startSOCKSProxy(p, "127.0.0.1:0")
	if err != nil {
		_ = httpLn.Close()
		t.Fatalf("start socks proxy: %v", err)
	}
	httpAddr = "http://" + httpLn.Addr().String()
	socksHostPort = socksLn.Addr().String()
	cleanup = func() {
		_ = httpLn.Close()
		_ = socksLn.Close()
	}
	return
}

func TestHTTPProxyForwardsAllowedPlain(t *testing.T) {
	// Allowed upstream that just echoes a known body.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "upstream-ok")
	}))
	defer up.Close()

	upHost := hostFromAddr(strings.TrimPrefix(up.URL, "http://"))
	p := &Policy{Name: "t", AllowedDomains: []string{upHost}}
	proxyURL, _, done := startProxiesForTest(t, p)
	defer done()

	pu, _ := url.Parse(proxyURL)
	cli := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
		Timeout:   5 * time.Second,
	}
	resp, err := cli.Get(up.URL + "/")
	if err != nil {
		t.Fatalf("GET via proxy: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "upstream-ok") {
		t.Fatalf("unexpected response: status=%d body=%q", resp.StatusCode, body)
	}
}

func TestHTTPProxyBlocksDeniedPlain(t *testing.T) {
	// We don't actually reach the upstream — the proxy should 403 first.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should never be reached")
	}))
	defer up.Close()

	// Empty allowlist = block everything.
	p := &Policy{Name: "t", AllowedDomains: []string{}}
	proxyURL, _, done := startProxiesForTest(t, p)
	defer done()

	pu, _ := url.Parse(proxyURL)
	cli := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
		Timeout:   5 * time.Second,
	}
	resp, err := cli.Get(up.URL + "/")
	if err != nil {
		t.Fatalf("GET via proxy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403, got %d body=%q", resp.StatusCode, body)
	}
}

func TestHTTPProxyConnectTunnelsToAllowedHTTPS(t *testing.T) {
	// httptest.NewTLSServer gives us a real TLS upstream on 127.0.0.1.
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "tls-upstream-ok")
	}))
	defer up.Close()

	upHost := hostFromAddr(strings.TrimPrefix(up.URL, "https://"))
	p := &Policy{Name: "t", AllowedDomains: []string{upHost}}
	proxyURL, _, done := startProxiesForTest(t, p)
	defer done()

	pu, _ := url.Parse(proxyURL)
	cli := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(pu),
			// httptest's TLS cert is self-signed.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 5 * time.Second,
	}
	resp, err := cli.Get(up.URL + "/")
	if err != nil {
		t.Fatalf("GET https via CONNECT: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "tls-upstream-ok") {
		t.Fatalf("unexpected response: status=%d body=%q", resp.StatusCode, body)
	}
}

func TestHTTPProxyConnectBlocksDeniedHTTPS(t *testing.T) {
	// Empty allowlist. The CONNECT should return 403; the client's TLS
	// handshake never starts.
	p := &Policy{Name: "t", AllowedDomains: []string{}}
	proxyURL, _, done := startProxiesForTest(t, p)
	defer done()

	pu, _ := url.Parse(proxyURL)
	cli := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
		Timeout:   5 * time.Second,
	}
	// Target host doesn't need to exist — the proxy must refuse before
	// dialing.
	_, err := cli.Get("https://example.com/")
	if err == nil {
		t.Fatalf("expected error for blocked CONNECT, got nil")
	}
	if !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("error should mention 403/Forbidden, got: %v", err)
	}
}

// TestSOCKSProxyTunnelsAllowed exercises the SOCKS5 path end-to-end. We
// connect via a hand-rolled SOCKS5 client because Go's stdlib does not
// ship a SOCKS5 dialer that integrates with http.Transport cleanly enough
// to be worth importing here — the protocol is small.
func TestSOCKSProxyTunnelsAllowed(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "socks-ok")
	}))
	defer up.Close()

	upHost, upPort, _ := net.SplitHostPort(strings.TrimPrefix(up.URL, "http://"))
	p := &Policy{Name: "t", AllowedDomains: []string{upHost}}
	_, socksAddr, done := startProxiesForTest(t, p)
	defer done()

	conn, err := net.DialTimeout("tcp", socksAddr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial socks: %v", err)
	}
	defer conn.Close()

	// Greeting: VER=5, NMETHODS=1, METHOD=0
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("greet: %v", err)
	}
	greet := make([]byte, 2)
	if _, err := io.ReadFull(conn, greet); err != nil {
		t.Fatalf("read greet reply: %v", err)
	}
	if greet[0] != 5 || greet[1] != 0 {
		t.Fatalf("unexpected greet reply: %v", greet)
	}

	// Request: VER=5, CMD=1, RSV=0, ATYP=3 (domain), len, host..., port
	port, _ := net.LookupPort("tcp", upPort)
	req := []byte{5, 1, 0, 3, byte(len(upHost))}
	req = append(req, upHost...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	req = append(req, portBytes...)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("send CONNECT: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read CONNECT reply: %v", err)
	}
	if reply[1] != 0 {
		t.Fatalf("SOCKS CONNECT failed: rep=%d", reply[1])
	}

	// Send a minimal HTTP request, read response.
	fmt.Fprintf(conn, "GET / HTTP/1.0\r\nHost: %s\r\n\r\n", upHost)
	resp, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read upstream resp: %v", err)
	}
	if !strings.Contains(string(resp), "socks-ok") {
		t.Fatalf("unexpected upstream response: %q", resp)
	}
}

func TestSOCKSProxyRejectsDenied(t *testing.T) {
	p := &Policy{Name: "t", AllowedDomains: []string{}}
	_, socksAddr, done := startProxiesForTest(t, p)
	defer done()

	conn, err := net.DialTimeout("tcp", socksAddr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial socks: %v", err)
	}
	defer conn.Close()

	// Greet.
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("greet: %v", err)
	}
	greet := make([]byte, 2)
	if _, err := io.ReadFull(conn, greet); err != nil {
		t.Fatalf("read greet: %v", err)
	}

	// CONNECT to example.com:80 (denied).
	host := "example.com"
	req := []byte{5, 1, 0, 3, byte(len(host))}
	req = append(req, host...)
	req = append(req, 0, 80)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("send CONNECT: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read CONNECT reply: %v", err)
	}
	if reply[1] != socksRepNotAllowed {
		t.Fatalf("expected SOCKS rep=%d (not allowed), got %d", socksRepNotAllowed, reply[1])
	}
}
