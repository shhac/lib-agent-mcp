package agentmcp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/lib-agent-mcp/oauth"
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

func TestPairRotateSuccess(t *testing.T) {
	store := oauth.NewMemStore()
	s := newServer(testRoot())
	s.openOAuthStore = func() (oauthSecretStore, error) { return store, nil }

	old, err := oauth.NewPairing(store).Code() // seed an initial code
	if err != nil {
		t.Fatal(err)
	}

	rotate := findSub(pairCommand(s), "rotate")
	var out bytes.Buffer
	rotate.SetOut(&out)
	if err := rotate.RunE(rotate, nil); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if !strings.Contains(out.String(), "new pairing code:") {
		t.Errorf("rotate output missing the code line: %q", out.String())
	}

	// The stored code changed; the old one no longer verifies, the new one does.
	newCode, _ := oauth.NewPairing(store).Code()
	if newCode == old {
		t.Error("rotate did not change the pairing code")
	}
	if ok, _ := oauth.NewPairing(store).Verify(old); ok {
		t.Error("the old pairing code still verifies after rotate")
	}
	if ok, _ := oauth.NewPairing(store).Verify(newCode); !ok {
		t.Error("the new pairing code does not verify")
	}
}

func TestPairResetSuccess(t *testing.T) {
	store := oauth.NewMemStore()
	_ = store.Set("signing-key", "secret")
	_ = store.Set("clients", "{}")
	s := newServer(testRoot())
	s.openOAuthStore = func() (oauthSecretStore, error) { return store, nil }

	reset := findSub(pairCommand(s), "reset")
	if err := reset.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	reset.SetOut(&out)
	if err := reset.RunE(reset, nil); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if v, ok, _ := store.Get("signing-key"); ok || v != "" {
		t.Errorf("reset did not wipe the store: signing-key=%q ok=%v", v, ok)
	}
	if !strings.Contains(out.String(), "cleared") {
		t.Errorf("reset output missing the confirmation: %q", out.String())
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
