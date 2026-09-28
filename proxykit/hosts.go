// Package proxykit is a small toolkit for PAC-driven intercepting proxies.
//
// Origin: copied unchanged from the proxy-tests demo (../go/proxykit, module
// proxydemo), written in the same session as this project.
//
// A client (for example an iPhone with a PAC URL configured) is steered by a
// generated PAC file to send traffic for a chosen set of hostnames through the
// proxy. Those hosts are TLS-terminated with a caller-provided certificate and
// every request runs through a user Hook that can inspect, modify, replace, or
// simply forward it. Any other CONNECT is blind-tunneled without decryption.
package proxykit

import (
	"net"
	"sort"
	"strings"
	"sync"
)

// Hook is called once per intercepted request. It may call Flow.Respond,
// Flow.Forward, or neither (in which case the request is forwarded and
// streamed back untouched).
type Hook func(f *Flow)

// HostTable is the live set of intercepted hostnames and their hooks.
// It is the single source of truth for the PAC file, the CONNECT
// intercept-or-tunnel decision, and request routing, so editing it at runtime
// immediately changes all three.
type HostTable struct {
	mu sync.RWMutex
	// exact maps "api.example.com" to its hook.
	exact map[string]Hook
	// suffix maps ".example.com" (from pattern "*.example.com") to its hook.
	suffix map[string]Hook
}

// NewHostTable returns an empty table.
func NewHostTable() *HostTable {
	return &HostTable{exact: map[string]Hook{}, suffix: map[string]Hook{}}
}

// wildcardPrefix marks a suffix pattern such as "*.example.com".
const wildcardPrefix = "*."

// Handle adds or replaces a pattern. A pattern is either an exact hostname or
// "*.domain", which matches any subdomain of domain (but not domain itself).
// A nil hook means "intercept, but just forward".
func (t *HostTable) Handle(pattern string, hook Hook) {
	pattern = strings.ToLower(pattern)
	t.mu.Lock()
	defer t.mu.Unlock()
	if strings.HasPrefix(pattern, wildcardPrefix) {
		// Keep the leading dot so matching is a plain HasSuffix.
		t.suffix[pattern[1:]] = hook
		return
	}
	t.exact[pattern] = hook
}

// Remove deletes a pattern previously passed to Handle. Unknown patterns are ignored.
func (t *HostTable) Remove(pattern string) {
	pattern = strings.ToLower(pattern)
	t.mu.Lock()
	defer t.mu.Unlock()
	if strings.HasPrefix(pattern, wildcardPrefix) {
		delete(t.suffix, pattern[1:])
		return
	}
	delete(t.exact, pattern)
}

// Lookup finds the hook for host, which may carry a ":port". Exact matches win
// over wildcards, and the longest wildcard suffix wins among wildcards.
func (t *HostTable) Lookup(host string) (hook Hook, ok bool) {
	host = NormalizeHost(host)
	t.mu.RLock()
	defer t.mu.RUnlock()
	if hook, ok = t.exact[host]; ok {
		return hook, true
	}
	bestLen := 0
	for suffix, h := range t.suffix {
		if len(suffix) > bestLen && strings.HasSuffix(host, suffix) {
			hook, ok, bestLen = h, true, len(suffix)
		}
	}
	return hook, ok
}

// Patterns returns a sorted snapshot of every pattern, wildcards in "*.x" form.
func (t *HostTable) Patterns() []string {
	t.mu.RLock()
	patterns := make([]string, 0, len(t.exact)+len(t.suffix))
	for host := range t.exact {
		patterns = append(patterns, host)
	}
	for suffix := range t.suffix {
		patterns = append(patterns, "*"+suffix)
	}
	t.mu.RUnlock()
	sort.Strings(patterns)
	return patterns
}

// NormalizeHost lowercases host and strips any ":port" (IPv6 brackets included),
// because clients are inconsistent about sending ports in Host/:authority.
func NormalizeHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}
