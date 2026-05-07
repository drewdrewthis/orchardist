// Tests for probeAddr — the helper that rewrites the health-probe
// target address to avoid Go's IPv6-first resolution of `localhost`.
// Resolves issue #405.

package daemon

import "testing"

func TestProbeAddr_LocalhostWithPortBecomes127(t *testing.T) {
	got := probeAddr("localhost:7777")
	if got != "127.0.0.1:7777" {
		t.Errorf("probeAddr(localhost:7777) = %q, want 127.0.0.1:7777", got)
	}
}

func TestProbeAddr_BareLocalhostBecomes127(t *testing.T) {
	got := probeAddr("localhost")
	if got != "127.0.0.1" {
		t.Errorf("probeAddr(localhost) = %q, want 127.0.0.1", got)
	}
}

func TestProbeAddr_ExplicitIP4PassesThrough(t *testing.T) {
	got := probeAddr("127.0.0.1:7777")
	if got != "127.0.0.1:7777" {
		t.Errorf("probeAddr(127.0.0.1:7777) = %q, want unchanged", got)
	}
}

func TestProbeAddr_ZeroAddrPassesThrough(t *testing.T) {
	// `--addr 0.0.0.0:8000` is a valid override. We don't rewrite it
	// because the daemon probably bound 0.0.0.0 deliberately.
	got := probeAddr("0.0.0.0:8000")
	if got != "0.0.0.0:8000" {
		t.Errorf("probeAddr(0.0.0.0:8000) = %q, want unchanged", got)
	}
}

func TestProbeAddr_FqdnPassesThrough(t *testing.T) {
	got := probeAddr("orchard.internal:7777")
	if got != "orchard.internal:7777" {
		t.Errorf("probeAddr(orchard.internal:7777) = %q, want unchanged", got)
	}
}

func TestProbeAddr_IPv6BracketPassesThrough(t *testing.T) {
	// IPv6 listener address is its own choice; don't rewrite.
	got := probeAddr("[::1]:7777")
	if got != "[::1]:7777" {
		t.Errorf("probeAddr([::1]:7777) = %q, want unchanged", got)
	}
}

func TestProbeAddr_LocalhostPrefixOnHostnameNotRewritten(t *testing.T) {
	// Pathological hostname `localhost.example.com:7777` should not
	// match — only the exact `localhost:` prefix triggers rewriting.
	got := probeAddr("localhost.example.com:7777")
	if got != "localhost.example.com:7777" {
		t.Errorf("probeAddr(localhost.example.com:7777) = %q, want unchanged", got)
	}
}
