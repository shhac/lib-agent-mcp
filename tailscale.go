package agentmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Tailscale auto-wiring: when --tailscale funnel|serve is set, the server brings
// up a Tailscale tunnel in front of the local HTTP listener and tears it down on
// shutdown, so a single command yields a public (funnel) or tailnet-private
// (serve) https URL with no separate `tailscale` step.
//
//   funnel — exposed on the public internet (what a cloud MCP connector needs)
//   serve  — reachable only from the tailnet
//
// Tailscale terminates TLS and serves on one of three public ports; the URL
// carries the port unless it is the default 443.

// tailscalePorts are the ports Tailscale Funnel is allowed to expose.
var tailscalePorts = map[int]bool{443: true, 8443: true, 10000: true}

const tailscaleOpTimeout = 15 * time.Second

// cmdRunner runs an external command; injected so tests exercise the wiring
// without a real `tailscale` binary or network.
type cmdRunner interface {
	run(ctx context.Context, name string, args ...string) error
	output(ctx context.Context, name string, args ...string) ([]byte, error)
}

// osRunner is the production cmdRunner backed by os/exec.
type osRunner struct{}

func (osRunner) run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func (osRunner) output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// validateTailscale checks the --tailscale mode and --tailscale-port combination.
func validateTailscale(mode string, port int) error {
	if mode != "funnel" && mode != "serve" {
		return fmt.Errorf(`--tailscale: only "funnel" or "serve" is supported, got %q`, mode)
	}
	if !tailscalePorts[port] {
		return fmt.Errorf("--tailscale-port: must be 443, 8443, or 10000, got %d", port)
	}
	return nil
}

// localPort extracts the port from an --http listen address (":8330" → "8330").
func localPort(httpAddr string) (string, error) {
	_, port, err := net.SplitHostPort(httpAddr)
	if err != nil || port == "" {
		return "", fmt.Errorf("--tailscale needs a port in --http %q (e.g. :8330)", httpAddr)
	}
	return port, nil
}

// tailscalePublicURL derives the externally-reachable root URL from the node's
// MagicDNS name, carrying the public port when it is not the default 443.
func tailscalePublicURL(ctx context.Context, r cmdRunner, bin string, port int) (string, error) {
	out, err := r.output(ctx, bin, "status", "--json")
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	var st struct {
		Self struct{ DNSName string }
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return "", fmt.Errorf("parsing tailscale status: %w", err)
	}
	host := strings.TrimSuffix(st.Self.DNSName, ".")
	if host == "" {
		return "", errors.New("tailscale status reported no MagicDNS name for this node")
	}
	url := "https://" + host
	if port != 443 {
		url += ":" + strconv.Itoa(port)
	}
	return url, nil
}

// wireTailscale validates the flags, derives the public URL from the node's
// MagicDNS name when one wasn't supplied, brings up the tunnel in front of the
// --http listener, and returns the public URL plus a teardown func. A mode of ""
// is a no-op that echoes publicURL back with a nil teardown.
func wireTailscale(ctx context.Context, mode string, port int, httpAddr, publicURL string) (string, func() error, error) {
	if mode == "" {
		return publicURL, nil, nil
	}
	if err := validateTailscale(mode, port); err != nil {
		return "", nil, err
	}
	lport, err := localPort(httpAddr)
	if err != nil {
		return "", nil, err
	}
	bin, err := exec.LookPath("tailscale")
	if err != nil {
		return "", nil, errors.New("--tailscale needs the `tailscale` CLI on PATH; install Tailscale or drop --tailscale")
	}
	var r cmdRunner = osRunner{}
	if publicURL == "" {
		if publicURL, err = tailscalePublicURL(ctx, r, bin, port); err != nil {
			return "", nil, err
		}
	}
	teardown, err := startTailscale(ctx, r, bin, mode, port, lport)
	if err != nil {
		return "", nil, err
	}
	return publicURL, teardown, nil
}

// startTailscale brings up the funnel/serve tunnel in front of localPort and
// returns a teardown func. Start uses ctx; teardown runs on its own background
// context because it fires during shutdown, when ctx is already cancelled.
func startTailscale(ctx context.Context, r cmdRunner, bin, mode string, port int, localPort string) (func() error, error) {
	args := []string{mode, "--bg", "--yes", "--https=" + strconv.Itoa(port), localPort}
	if err := r.run(ctx, bin, args...); err != nil {
		return nil, fmt.Errorf("starting tailscale %s: %w", mode, err)
	}
	teardown := func() error {
		tctx, cancel := context.WithTimeout(context.Background(), tailscaleOpTimeout)
		defer cancel()
		if err := r.run(tctx, bin, mode, "--https="+strconv.Itoa(port), "off"); err != nil {
			return fmt.Errorf("stopping tailscale %s: %w", mode, err)
		}
		return nil
	}
	return teardown, nil
}
