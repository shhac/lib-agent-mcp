package agentmcp

import (
	"context"
	"strings"
	"testing"
)

// fakeRunner records invocations and returns canned output/errors.
type fakeRunner struct {
	calls      [][]string
	statusJSON string
	runErr     error
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.runErr
}

func (f *fakeRunner) output(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return []byte(f.statusJSON), nil
}

func argsEqual(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestValidateTailscale(t *testing.T) {
	cases := []struct {
		mode string
		port int
		ok   bool
	}{
		{"funnel", 443, true},
		{"serve", 8443, true},
		{"funnel", 10000, true},
		{"bogus", 443, false},
		{"", 443, false},
		{"funnel", 80, false},
		{"serve", 3000, false},
	}
	for _, c := range cases {
		err := validateTailscale(c.mode, c.port)
		if (err == nil) != c.ok {
			t.Errorf("validateTailscale(%q,%d) err=%v, want ok=%v", c.mode, c.port, err, c.ok)
		}
	}
}

func TestLocalPort(t *testing.T) {
	ok := map[string]string{":8330": "8330", "127.0.0.1:9000": "9000", "0.0.0.0:443": "443"}
	for in, want := range ok {
		got, err := localPort(in)
		if err != nil || got != want {
			t.Errorf("localPort(%q) = %q,%v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"8330", "", "localhost"} {
		if _, err := localPort(bad); err == nil {
			t.Errorf("localPort(%q) should error", bad)
		}
	}
}

func TestTailscalePublicURL(t *testing.T) {
	r := &fakeRunner{statusJSON: `{"Self":{"DNSName":"box.tail123.ts.net."}}`}
	if got, err := tailscalePublicURL(context.Background(), r, "tailscale", 443); err != nil || got != "https://box.tail123.ts.net" {
		t.Errorf("port 443 → %q,%v; want https://box.tail123.ts.net", got, err)
	}
	if got, err := tailscalePublicURL(context.Background(), r, "tailscale", 8443); err != nil || got != "https://box.tail123.ts.net:8443" {
		t.Errorf("port 8443 → %q,%v; want …:8443", got, err)
	}

	empty := &fakeRunner{statusJSON: `{"Self":{"DNSName":""}}`}
	if _, err := tailscalePublicURL(context.Background(), empty, "tailscale", 443); err == nil {
		t.Error("empty MagicDNS name should error")
	}
}

func TestStartTailscaleAndTeardown(t *testing.T) {
	r := &fakeRunner{}
	teardown, err := startTailscale(context.Background(), r, "tailscale", "funnel", 443, "8330")
	if err != nil {
		t.Fatalf("startTailscale: %v", err)
	}
	if len(r.calls) != 1 || !argsEqual(r.calls[0], "tailscale", "funnel", "--bg", "--yes", "--https=443", "8330") {
		t.Errorf("start argv = %v", r.calls)
	}
	if err := teardown(); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if len(r.calls) != 2 || !argsEqual(r.calls[1], "tailscale", "funnel", "--https=443", "off") {
		t.Errorf("teardown argv = %v", r.calls)
	}
}

func TestStartTailscalePropagatesError(t *testing.T) {
	r := &fakeRunner{runErr: context.DeadlineExceeded}
	if _, err := startTailscale(context.Background(), r, "tailscale", "serve", 8443, "8330"); err == nil {
		t.Error("startTailscale should surface the runner error")
	} else if !strings.Contains(err.Error(), "serve") {
		t.Errorf("error %q should mention the mode", err)
	}
}
