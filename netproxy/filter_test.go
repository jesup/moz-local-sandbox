package main

import "testing"

func TestMatchHost(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		// Exact matches.
		{"api.anthropic.com", "api.anthropic.com", true},
		{"api.anthropic.com", "API.ANTHROPIC.COM", false}, // matchHost is case-sensitive; loadPolicy lowercases.
		{"api.anthropic.com", "other.anthropic.com", false},
		{"api.anthropic.com", "anthropic.com", false},

		// Wildcard subdomain.
		{"*.github.com", "raw.github.com", true},
		{"*.github.com", "api.github.com", true},
		{"*.github.com", "deep.nested.github.com", true},
		{"*.github.com", "github.com", false}, // bare domain not matched
		{"*.github.com", "evilgithub.com", false},
		{"*.github.com", "notgithub.com", false},

		// Pattern requires at least one char of subdomain.
		{"*.example.com", ".example.com", false},

		// IPs — only exact match.
		{"127.0.0.1", "127.0.0.1", true},
		{"127.0.0.1", "127.0.0.2", false},
	}
	for _, c := range cases {
		got := matchHost(c.pattern, c.host)
		if got != c.want {
			t.Errorf("matchHost(%q, %q) = %v, want %v", c.pattern, c.host, got, c.want)
		}
	}
}

func TestPolicyAllows(t *testing.T) {
	p := &Policy{
		Name:           "test",
		AllowedDomains: []string{"api.anthropic.com", "*.github.com"},
		DeniedDomains:  []string{"evil.github.com"},
	}

	cases := []struct {
		host string
		want bool
	}{
		{"api.anthropic.com", true},
		{"API.ANTHROPIC.COM", true}, // Allows() lowercases
		{"raw.github.com", true},
		{"evil.github.com", false}, // explicit deny beats wildcard allow
		{"attacker.com", false},
		{"github.com", false}, // bare domain not implied by wildcard
		{"", false},
	}
	for _, c := range cases {
		got := p.Allows(c.host)
		if got != c.want {
			t.Errorf("Allows(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestEmptyAllowlistBlocksEverything(t *testing.T) {
	p := &Policy{Name: "empty", AllowedDomains: []string{}}
	for _, h := range []string{"api.anthropic.com", "github.com", "anywhere.test"} {
		if p.Allows(h) {
			t.Errorf("empty allowlist should block %q, got allowed", h)
		}
	}
}

func TestHostFromAddr(t *testing.T) {
	cases := []struct {
		addr, want string
	}{
		{"example.com:443", "example.com"},
		{"127.0.0.1:80", "127.0.0.1"},
		{"[::1]:8080", "::1"},
		{"plain-host", "plain-host"},
	}
	for _, c := range cases {
		if got := hostFromAddr(c.addr); got != c.want {
			t.Errorf("hostFromAddr(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}
