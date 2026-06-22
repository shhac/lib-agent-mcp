package agentmcp

import (
	"strings"
	"testing"
)

// TestStartupBanner — the boot notice names the server, its version, the stdio
// transport, and the tool count (singular/plural), so an operator sees what came
// up. It is informational text for stderr, never the JSON-RPC stream.
func TestStartupBanner(t *testing.T) {
	banner := newServer(testRoot()).startupBanner()
	for _, want := range []string{"widget", "1.0.0", "ready", "stdio", "protocol"} {
		if !strings.Contains(banner, want) {
			t.Errorf("banner missing %q: %s", want, banner)
		}
	}
	// testRoot exposes nothing, so legacy reflect-all yields several leaf tools.
	if n := len(newServer(testRoot()).tools); !strings.Contains(banner, "tools") || n < 2 {
		t.Errorf("banner should report %d tools (plural): %s", n, banner)
	}

	// Singular when exactly one tool, and sensible fallbacks for an unnamed root.
	single := newServer(optInRoot()).startupBanner() // optInRoot exposes one group tool
	if !strings.Contains(single, "1 tool ") {
		t.Errorf("single-tool banner should say '1 tool': %s", single)
	}
}
