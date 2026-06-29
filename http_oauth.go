package agentmcp

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shhac/lib-agent-mcp/oauth"
)

// oauthService is the keyring service id the local-OAuth secrets live under —
// shared by the running server (setupOAuth) and the `pair` maintenance commands,
// so they always target the same namespace. Defaults to "<name>.mcp"; the host
// app should override it (WithOAuthKeyringService) to match its own reverse-DNS
// keyring service, e.g. "app.example.agent-foo.mcp".
func (s *Server) oauthService() string {
	if s.opts.oauthKeyringService != "" {
		return s.opts.oauthKeyringService
	}
	return s.opts.name + ".mcp"
}

// oauthSecretStore is the store surface the OAuth layer and the `pair`
// maintenance commands need: the persisted secrets, plus the namespace-wide wipe
// for `pair reset`. Both *oauth.KeyringStore and *oauth.MemStore satisfy it.
type oauthSecretStore interface {
	oauth.SecretStore
	DeleteAll() error
}

// oauthStore opens the keyring-backed secret store the local-OAuth layer uses,
// erroring when no OS keyring is available (the secrets can't be read or changed).
// It is the single definition of how the OAuth store is opened and checked; tests
// inject openOAuthStore to avoid a real keyring.
func (s *Server) oauthStore() (oauthSecretStore, error) {
	if s.openOAuthStore != nil {
		return s.openOAuthStore()
	}
	store := oauth.NewKeyringStore(s.oauthService())
	if !store.Available() {
		return nil, errors.New("no OS keyring is available on this host, so the local-OAuth secrets can't be read")
	}
	return store, nil
}

// mcpEndpoint is the canonical /mcp resource URL for a public root URL — the
// protected resource and token audience (the client binds the audience to the
// exact URL it calls, so it's the endpoint, not the bare host).
func mcpEndpoint(publicURL string) string {
	return strings.TrimRight(publicURL, "/") + mcpHTTPPath
}

// setupOAuth builds the local OAuth server when --oauth local is set, validating
// the flag combination. A mode of "" leaves OAuth off (s.oauth stays nil).
func (s *Server) setupOAuth(mode, publicURL string) error {
	if mode == "" {
		return nil
	}
	if mode != "local" {
		return fmt.Errorf(`--oauth: only "local" is supported, got %q`, mode)
	}
	if publicURL == "" {
		return errors.New("--oauth local requires --public-url <https-url> (the externally-reachable URL of this server)")
	}

	store, err := s.oauthStore()
	if err != nil {
		return fmt.Errorf("--oauth local needs an OS keyring to store its signing key: %w", err)
	}

	osrv, err := oauth.New(oauth.Config{Store: store, PublicURL: publicURL, Resource: mcpEndpoint(publicURL)})
	if err != nil {
		return err
	}
	s.oauth = osrv
	return nil
}

// startupBannerHTTPOAuth is the stderr boot banner for the OAuth-gated HTTP
// transport — like the plain HTTP banner but without the unauthenticated
// warning, since access is now gated.
func (s *Server) startupBannerHTTPOAuth(addr string) string {
	return s.bannerCore("streamable-http", httpURL(addr)) + " · authorization: OAuth 2.1 (local)"
}

// writeOAuthBootInfo prints the human-facing connection details to stdout. In
// HTTP mode stdout is not the protocol channel, so this is safe. The pairing
// code is a secret — it is shown because only the operator who launched the
// server sees this output.
func (s *Server) writeOAuthBootInfo(w io.Writer, addr, publicURL string) error {
	code, err := s.oauth.PairingCode()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, `Connect this MCP server (OAuth 2.1, local — it is its own authorization server):
  connector URL  : %s          ← add this exact URL (with /mcp) to the connector
  local listener : %s
  pairing code   : %s
  ⚠ Treat the pairing code like a password. Enter it once on the browser
    approval page when a client connects; it is reusable across clients.
`, mcpEndpoint(publicURL), httpURL(addr), code)
	return err
}
