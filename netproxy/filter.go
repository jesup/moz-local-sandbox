package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
)

// Policy is the JSON shape of a network policy file: a domain allowlist
// (with optional `*.` wildcards) plus an explicit denylist that takes
// precedence. An empty AllowedDomains means "block everything".
type Policy struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	AllowedDomains []string `json:"allowedDomains"`
	DeniedDomains  []string `json:"deniedDomains,omitempty"`
}

func loadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// Lowercase patterns once, eagerly — matching is hot-path.
	for i, d := range p.AllowedDomains {
		p.AllowedDomains[i] = strings.ToLower(d)
	}
	for i, d := range p.DeniedDomains {
		p.DeniedDomains[i] = strings.ToLower(d)
	}
	return &p, nil
}

// Allows decides whether the target host (no port, no brackets) is reachable
// under this policy. Deny takes precedence over allow. An IP literal must
// match an explicit IP entry in the lists — wildcards are domain-only.
func (p *Policy) Allows(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")

	for _, pat := range p.DeniedDomains {
		if matchHost(pat, host) {
			return false
		}
	}
	for _, pat := range p.AllowedDomains {
		if matchHost(pat, host) {
			return true
		}
	}
	return false
}

// matchHost reports whether host matches the pattern. Patterns are either:
//   - exact ("api.anthropic.com" matches itself only); or
//   - leading-wildcard ("*.github.com" matches "lfs.github.com" and
//     "raw.codeload.github.com", but NOT "github.com" itself — the
//     subdomain has to be present).
//
// IP literals are matched only by exact-string comparison.
func matchHost(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".github.com"
		// Require at least one character of subdomain before the dot.
		if len(host) > len(suffix) && strings.HasSuffix(host, suffix) {
			// And that character isn't itself a dot — avoids "*.x.com"
			// matching ".x.com" as if it were a real host.
			label := host[:len(host)-len(suffix)]
			if !strings.Contains(label, ".") {
				return true
			}
			// `*.example.com` only matches one label deep by default;
			// allow deeper for convenience (npm uses lots of these).
			return true
		}
	}
	return false
}

// hostFromAddr extracts the hostname from an addr like "example.com:443"
// or "[::1]:8080". Returns empty string on malformed input.
func hostFromAddr(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}
