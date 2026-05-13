package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

// hopByHopHeaders are headers that MUST NOT be forwarded by an HTTP proxy
// per RFC 7230 §6.1. They terminate at the proxy and are re-emitted by the
// next hop if needed.
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Keep-Alive",
	"Transfer-Encoding",
	"TE",
	"Trailer",
	"Upgrade",
}

// startHTTPProxy listens on addr and returns the bound listener. CONNECT
// requests get an opaque TCP tunnel after host-allowlist check; plain HTTP
// requests are forwarded directly with hop-by-hop headers stripped.
//
// We do NOT terminate TLS — CONNECT tunnels are byte-pipes between the
// client and the upstream server. Domain filtering is therefore at SNI/
// host-header granularity; we cannot inspect HTTPS content. (Adding MITM
// would require generating a CA the sandbox trusts; punted to a future
// iteration.)
func startHTTPProxy(policy *Policy, addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("http proxy: listen %s: %w", addr, err)
	}

	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			handleConnect(policy, w, r)
			return
		}
		handlePlain(policy, w, r)
	})

	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  0, // CONNECT tunnels can be long-lived.
		WriteTimeout: 0,
	}
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http proxy: serve: %v", err)
		}
	}()
	return ln, nil
}

func handleConnect(policy *Policy, w http.ResponseWriter, r *http.Request) {
	host := hostFromAddr(r.URL.Host)
	if host == "" {
		http.Error(w, "bad CONNECT target", http.StatusBadRequest)
		return
	}
	if !policy.Allows(host) {
		log.Printf("DENY CONNECT %s", r.URL.Host)
		http.Error(w, "blocked by network allowlist", http.StatusForbidden)
		return
	}
	log.Printf("CONNECT %s", r.URL.Host)

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		log.Printf("CONNECT hijack: %v", err)
		return
	}
	defer clientConn.Close()

	upstream, err := net.DialTimeout("tcp", r.URL.Host, 10*time.Second)
	if err != nil {
		log.Printf("CONNECT dial %s: %v", r.URL.Host, err)
		_, _ = clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer upstream.Close()

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	bidirectionalCopy(clientConn, upstream)
}

func handlePlain(policy *Policy, w http.ResponseWriter, r *http.Request) {
	host := hostFromAddr(r.Host)
	if host == "" {
		http.Error(w, "bad Host header", http.StatusBadRequest)
		return
	}
	if !policy.Allows(host) {
		log.Printf("DENY %s %s%s", r.Method, r.Host, r.URL.Path)
		http.Error(w, "blocked by network allowlist", http.StatusForbidden)
		return
	}
	log.Printf("%s http://%s%s", r.Method, r.Host, r.URL.Path)

	// Build absolute URL to forward. Proxied requests usually arrive with
	// r.URL fully populated (proxy protocol), but plain HTTP through a
	// transparent proxy may have only path+query — reconstruct in that
	// case using the Host header.
	upstreamURL := r.URL.String()
	if r.URL.Scheme == "" {
		upstreamURL = "http://" + r.Host + r.URL.RequestURI()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	upReq, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, "upstream build: "+err.Error(), http.StatusBadGateway)
		return
	}
	copyHeaders(upReq.Header, r.Header)
	for _, h := range hopByHopHeaders {
		upReq.Header.Del(h)
	}

	resp, err := http.DefaultTransport.RoundTrip(upReq)
	if err != nil {
		log.Printf("upstream %s: %v", r.Host, err)
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	for _, h := range hopByHopHeaders {
		w.Header().Del(h)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
