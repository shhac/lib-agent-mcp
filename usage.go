package agentmcp

import (
	"fmt"

	"github.com/spf13/cobra"
)

// usageCommand is `mcp usage`: an LLM-optimized card explaining how to run and
// connect the MCP server (transports, registration, remote OAuth, Tailscale),
// not just flag syntax. It mirrors the family's `<domain> usage` convention.
func usageCommand(s *Server) *cobra.Command {
	return &cobra.Command{
		Use:         "usage",
		Short:       "How to run and connect this MCP server (LLM-optimized)",
		Annotations: map[string]string{AnnotationSkip: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), s.usageCard())
			return err
		},
	}
}

// usageCard renders the usage text for the current binary.
func (s *Server) usageCard() string {
	name, exec := s.bannerName(), s.executable()
	return fmt.Sprintf(`%[1]s mcp — run %[1]s's command tree as an MCP (Model Context Protocol) server.

TRANSPORTS
  stdio (default)      %[1]s mcp
      JSON-RPC over stdin/stdout. Launched BY an MCP client, not run by hand.
  Streamable HTTP      %[1]s mcp --http :8000
      One endpoint at http://<host>:8000/mcp. Unauthenticated unless --oauth —
      bind to loopback or front it with an auth proxy.

REGISTER (stdio)
  Desktop clients (Claude Desktop / Cursor / VS Code / Windsurf): add to the
  mcpServers config —
    %[3]s
  Claude Code CLI:
    %[4]s

REMOTE (Streamable HTTP + self-contained OAuth 2.1)
  %[1]s mcp --http :8000 --oauth local --public-url https://host
      The server is its OWN OAuth authorization + resource server (no third
      party). Register the FULL /mcp URL in the connector — "https://host/mcp",
      not the bare host: the path is the resource identifier, and the bare host
      makes the client POST to "/" and fail.
      A pairing code is printed at boot; the human enters it once on the browser
      approval page. It is reusable across clients.
        %[1]s mcp pair rotate   → new pairing code (if it leaks; keeps tokens)
        %[1]s mcp pair reset     → wipe ALL state: signing key + clients + tokens
                                  (requires --yes; every client must re-pair)

TAILSCALE (auto-expose)
  %[1]s mcp --http :8000 --oauth local --tailscale funnel
      Brings up a Tailscale funnel (public internet) or serve (tailnet-only) in
      front of the listener, derives --public-url from the node's MagicDNS name,
      and tears the tunnel down on Ctrl-C. --tailscale-port 443|8443|10000
      (default 443). Needs the tailscale CLI on PATH and Funnel enabled.

DEBUG
  --access-log <path>   NDJSON access log, one line per HTTP request (method,
                        path, status, duration, Origin, MCP-Protocol-Version,
                        User-Agent, X-Forwarded-For). Use "-" for stderr.
                        Authorization/Cookie are redacted.

KEY FLAGS
  --http <addr>             serve Streamable HTTP instead of stdio
  --oauth local             self-contained OAuth 2.1 (needs --http + a public URL)
  --public-url <url>        externally-reachable https URL (issuer; /mcp = audience)
  --tailscale funnel|serve  auto-expose via Tailscale and derive --public-url
  --tailscale-port <port>   public HTTPS port for --tailscale (443|8443|10000)
  --access-log <path>       NDJSON request-log destination ("-" = stderr)`,
		name, exec, mcpServersConfig(name, exec, false), claudeMcpAddLine(name, exec))
}
