package agentmcp

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shhac/lib-agent-mcp/oauth"
)

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

	service := s.opts.oauthKeyringService
	if service == "" {
		service = s.opts.name + ".mcp"
	}
	store := oauth.NewKeyringStore(service)
	if !store.Available() {
		return errors.New("--oauth local needs an OS keyring to store its signing key, but none is available on this host")
	}

	// The protected resource is the /mcp endpoint, not the bare host: the client
	// binds the token audience to the exact URL it calls.
	resource := strings.TrimRight(publicURL, "/") + mcpHTTPPath
	osrv, err := oauth.New(oauth.Config{Store: store, PublicURL: publicURL, Resource: resource})
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
	endpoint := strings.TrimRight(publicURL, "/") + mcpHTTPPath
	_, err = fmt.Fprintf(w, `Connect this MCP server (OAuth 2.1, local — it is its own authorization server):
  connector URL  : %s          ← add this exact URL (with /mcp) to the connector
  local listener : %s
  pairing code   : %s
  ⚠ Treat the pairing code like a password. Enter it once on the browser
    approval page when a client connects; it is reusable across clients.
`, endpoint, httpURL(addr), code)
	return err
}
