package agentmcp

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestOAuthServiceDerivation(t *testing.T) {
	// Default: "<root-name>.mcp".
	if got := newServer(testRoot()).oauthService(); got != "widget.mcp" {
		t.Errorf("default oauthService = %q, want widget.mcp", got)
	}
	// The host app overrides it with its reverse-DNS service.
	s := newServer(testRoot(), WithOAuthKeyringService("app.paulie.agent-slack.mcp"))
	if got := s.oauthService(); got != "app.paulie.agent-slack.mcp" {
		t.Errorf("overridden oauthService = %q", got)
	}
}

func findSub(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func TestPairResetRequiresYes(t *testing.T) {
	reset := findSub(pairCommand(newServer(testRoot())), "reset")
	if reset == nil {
		t.Fatal("pair reset subcommand missing")
	}
	// Without --yes the gate fires before any keyring access.
	if err := reset.RunE(reset, nil); err == nil {
		t.Error("pair reset without --yes should error")
	}
}

func TestPairHasRotateAndReset(t *testing.T) {
	pair := pairCommand(newServer(testRoot()))
	for _, name := range []string{"rotate", "reset"} {
		if findSub(pair, name) == nil {
			t.Errorf("pair is missing the %q subcommand", name)
		}
	}
}

func TestUsageCardCoversConnectionModel(t *testing.T) {
	card := newServer(testRoot()).usageCard()
	for _, want := range []string{
		"mcp --http", "--oauth local", "/mcp", "pair rotate", "pair reset",
		"--tailscale funnel", "--access-log", "claude mcp add",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("usage card missing %q", want)
		}
	}
}
