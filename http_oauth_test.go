package agentmcp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keyring "github.com/shhac/lib-agent-keyring"
	"github.com/shhac/lib-agent-mcp/oauth"
)

func TestSetupOAuthValidation(t *testing.T) {
	s := newServer(testRoot())

	if err := s.setupOAuth("", ""); err != nil || s.oauth != nil {
		t.Errorf("oauth off: err=%v oauth=%v, want nil/nil", err, s.oauth)
	}
	if err := s.setupOAuth("bogus", "https://x"); err == nil {
		t.Error("unsupported --oauth mode should error")
	}
	if err := s.setupOAuth("local", ""); err == nil {
		t.Error("--oauth local without --public-url should error")
	}

	// With the keyring opted out, local OAuth can't store its signing key.
	t.Setenv(keyring.NoKeychainEnv, "1")
	if err := s.setupOAuth("local", "https://mcp.example"); err == nil {
		t.Error("--oauth local with no usable keyring should error")
	}
}

func TestHTTPHandlerOAuthGatesMCP(t *testing.T) {
	s := newServer(testRoot())
	osrv, err := oauth.New(oauth.Config{Store: oauth.NewMemStore(), PublicURL: "https://mcp.example"})
	if err != nil {
		t.Fatalf("oauth.New: %v", err)
	}
	s.oauth = osrv

	ts := httptest.NewServer(s.httpHandler())
	defer ts.Close()

	// /mcp now requires a token.
	resp, err := http.Post(ts.URL+mcpHTTPPath, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/mcp without token = %d, want 401", resp.StatusCode)
	}

	// The discovery document is reachable (so the client can start OAuth).
	md, err := http.Get(ts.URL + oauth.ProtectedResourceMetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	md.Body.Close()
	if md.StatusCode != http.StatusOK {
		t.Errorf("PRM metadata = %d, want 200", md.StatusCode)
	}
}

func TestStartupBannerHTTPOAuth(t *testing.T) {
	b := newServer(testRoot()).startupBannerHTTPOAuth(":8000")
	for _, want := range []string{"streamable-http", "OAuth 2.1 (local)", "localhost:8000", "MCP server ready"} {
		if !strings.Contains(b, want) {
			t.Errorf("OAuth banner %q missing %q", b, want)
		}
	}
}

func TestWriteOAuthBootInfo(t *testing.T) {
	s := newServer(testRoot())
	osrv, err := oauth.New(oauth.Config{Store: oauth.NewMemStore(), PublicURL: "https://pub.example"})
	if err != nil {
		t.Fatal(err)
	}
	s.oauth = osrv

	var buf bytes.Buffer
	if err := s.writeOAuthBootInfo(&buf, ":8000", "https://pub.example"); err != nil {
		t.Fatalf("writeOAuthBootInfo: %v", err)
	}
	out := buf.String()
	code, _ := osrv.PairingCode()
	for _, want := range []string{"https://pub.example", "pairing code", code, "/mcp"} {
		if !strings.Contains(out, want) {
			t.Errorf("boot info missing %q:\n%s", want, out)
		}
	}
}

func TestHTTPHandlerNoOAuthLeavesMCPOpen(t *testing.T) {
	s := newServer(testRoot()) // no oauth
	ts := httptest.NewServer(s.httpHandler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+mcpHTTPPath, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/mcp without oauth = %d, want 200 (open)", resp.StatusCode)
	}
}
