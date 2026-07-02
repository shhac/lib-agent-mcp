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

func runPairSub(t *testing.T, s *Server, name string, args ...string) (string, error) {
	t.Helper()
	sub := findSub(pairCommand(s), name)
	if sub == nil {
		t.Fatalf("pair %s subcommand missing", name)
	}
	var out bytes.Buffer
	sub.SetOut(&out)
	if err := sub.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	err := sub.RunE(sub, sub.Flags().Args())
	return out.String(), err
}

func TestPairAddListRemovePrincipal(t *testing.T) {
	store := oauth.NewMemStore()
	s := newServer(testRoot())
	s.openOAuthStore = func() (oauthSecretStore, error) { return store, nil }

	out, err := runPairSub(t, s, "add", "alice", "--bind", "workspace=alice-acme")
	if err != nil {
		t.Fatalf("pair add: %v", err)
	}
	if !strings.Contains(out, "mcp-") {
		t.Errorf("add output should show the pairing code:\n%s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("add output should name the principal:\n%s", out)
	}

	// The code verifies as alice with the binding attached.
	grant, ok, err := oauth.NewPairing(store).VerifyPrincipal(extractPairingCode(t, out))
	if err != nil || !ok {
		t.Fatalf("verify minted code: ok=%v err=%v", ok, err)
	}
	if grant.Name != "alice" || grant.Binding["workspace"] != "alice-acme" {
		t.Errorf("grant = %+v", grant)
	}

	listOut, err := runPairSub(t, s, "list")
	if err != nil {
		t.Fatalf("pair list: %v", err)
	}
	if !strings.Contains(listOut, "alice") || !strings.Contains(listOut, "workspace=alice-acme") {
		t.Errorf("list output = %s", listOut)
	}
	if strings.Contains(listOut, "mcp-") {
		t.Errorf("list must never print pairing codes:\n%s", listOut)
	}

	if _, err := runPairSub(t, s, "remove", "alice"); err != nil {
		t.Fatalf("pair remove: %v", err)
	}
	if _, ok, _ := oauth.NewPairing(store).VerifyPrincipal(extractPairingCode(t, out)); ok {
		t.Error("removed principal's code still verifies")
	}
}

// extractPairingCode pulls the mcp-… code out of command output.
func extractPairingCode(t *testing.T, out string) string {
	t.Helper()
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "mcp-") {
			return strings.TrimRight(f, ".,\n")
		}
	}
	t.Fatalf("no pairing code in output:\n%s", out)
	return ""
}
