package agentmcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/shhac/lib-agent-mcp/oauth"
	"github.com/spf13/cobra"
)

// Server serves a cobra command tree over the MCP stdio transport. The tool
// list is derived once at construction (the command tree is static after
// setup) and reused for every tools/list and tools/call.
type Server struct {
	root        *cobra.Command
	opts        options
	tools       []Tool
	toolsByName map[string]*Tool

	// oauth, when non-nil, is the local OAuth server gating the HTTP transport.
	// It is built from --oauth local at serve time, not construction.
	oauth *oauth.Server

	// openOAuthStore, when non-nil, overrides how the local-OAuth secret store is
	// opened — a test seam so the pair commands can run against a MemStore.
	openOAuthStore func() (oauthSecretStore, error)

	// accessLog, when non-nil, records one NDJSON line per HTTP request. It is
	// built from --access-log at serve time.
	accessLog *accessLogger
}

func newServer(root *cobra.Command, opts ...Option) *Server {
	o := options{
		name:            root.Name(),
		version:         rootVersion(root),
		nameSeparator:   "_",
		hiddenFlags:     map[string]bool{},
		fileToolName:    defaultFileToolName,
		fileInlineLimit: defaultFileInlineLimit,
	}
	for _, f := range defaultHiddenFlags {
		o.hiddenFlags[f] = true
	}
	for _, opt := range opts {
		opt(&o)
	}
	s := &Server{root: root, opts: o}
	s.tools = s.buildTools()
	s.toolsByName = make(map[string]*Tool, len(s.tools))
	for i := range s.tools {
		s.toolsByName[s.tools[i].Name] = &s.tools[i]
	}
	return s
}

// Command returns an "mcp" subcommand that serves root's command tree over
// stdio. Add it to your root command:
//
//	root.AddCommand(agentmcp.Command(root))
func Command(root *cobra.Command, opts ...Option) *cobra.Command {
	s := newServer(root, opts...)
	var httpAddr, oauthMode, publicURL, tailscaleMode, accessLogPath string
	var tailscalePort int
	cmd := &cobra.Command{
		Use:         "mcp",
		Short:       "Run as an MCP server (stdio by default, or --http <addr>)",
		Annotations: map[string]string{AnnotationSkip: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if tailscaleMode != "" && httpAddr == "" {
				return errors.New("--tailscale requires --http (it fronts the local HTTP listener)")
			}
			// The Streamable HTTP transport is opt-in via --http; otherwise the
			// server speaks the protocol over stdio.
			if httpAddr != "" {
				return s.runHTTP(cmd.Context(), httpServeConfig{
					addr:          httpAddr,
					oauthMode:     oauthMode,
					publicURL:     publicURL,
					tailscaleMode: tailscaleMode,
					tailscalePort: tailscalePort,
					accessLogPath: accessLogPath,
				})
			}
			return s.runStdio(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&httpAddr, "http", "",
		"Serve the Streamable HTTP transport on this address (e.g. :8000) instead of stdio. "+
			"Unauthenticated unless --oauth is set — bind to loopback or front with an auth proxy.")
	cmd.Flags().StringVar(&oauthMode, "oauth", "",
		`Enable OAuth on the HTTP transport. Only "local" is supported: the server is its own `+
			"OAuth 2.1 authorization server. Requires --http and --public-url.")
	cmd.Flags().StringVar(&publicURL, "public-url", "",
		"Externally-reachable https URL of this server (the OAuth issuer; the /mcp endpoint is the token "+
			"audience). Required with --oauth unless --tailscale derives it.")
	cmd.Flags().StringVar(&tailscaleMode, "tailscale", "",
		`Front the HTTP transport with a Tailscale tunnel: "funnel" (public internet) or "serve" `+
			"(tailnet-private). Derives --public-url from the node's MagicDNS name when unset, and tears the "+
			"tunnel down on exit. Requires --http.")
	cmd.Flags().IntVar(&tailscalePort, "tailscale-port", 443,
		"Public HTTPS port for --tailscale (443, 8443, or 10000).")
	cmd.Flags().StringVar(&accessLogPath, "access-log", "",
		`Write one NDJSON line per HTTP request to this path ("-" for stderr) for debugging `+
			"connector traffic. Authorization/Cookie are redacted. Only applies with --http.")
	cmd.AddCommand(pairCommand(s), usageCommand(s))
	return cmd
}

// httpServeConfig carries the --http transport flags from the cobra command into
// runHTTP, keeping the method signature flat.
type httpServeConfig struct {
	addr          string
	oauthMode     string
	publicURL     string
	tailscaleMode string
	tailscalePort int
	accessLogPath string
}

// runStdio serves the MCP protocol over stdin/stdout — the default transport.
func (s *Server) runStdio(ctx context.Context) error {
	// Boot notice goes to STDERR: stdout carries the JSON-RPC stream and any
	// non-protocol byte there would corrupt the client's parser.
	fmt.Fprintln(os.Stderr, s.startupBanner())
	// When a human ran this directly (a TTY on stdin, no MCP host driving it),
	// they can't do anything useful — print the registration config instead, so
	// the output is self-describing (paste-able into an LLM or a client config).
	if stdinIsInteractive() {
		fmt.Fprintln(os.Stderr, "\n"+s.setupHint())
	}
	return s.Serve(ctx, os.Stdin, os.Stdout)
}

// runHTTP serves the Streamable HTTP transport. It owns SIGINT/SIGTERM (the host
// runs the command with a background context, so Ctrl-C must drain the server
// and tear down any tunnel we started), then layers in optional access logging,
// optional Tailscale fronting (which can derive the public URL), and optional
// local OAuth, before serving until the context is cancelled.
func (s *Server) runHTTP(parent context.Context, cfg httpServeConfig) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.accessLogPath != "" {
		al, err := newAccessLogger(cfg.accessLogPath)
		if err != nil {
			return err
		}
		s.accessLog = al
		defer func() { _ = al.Close() }()
	}

	// --tailscale brings up the funnel/serve tunnel and, when --public-url is
	// unset, derives it from the node's MagicDNS name.
	publicURL, tsDown, err := wireTailscale(ctx, cfg.tailscaleMode, cfg.tailscalePort, cfg.addr, cfg.publicURL)
	if err != nil {
		return err
	}
	if tsDown != nil {
		fmt.Fprintf(os.Stderr, "tailscale %s: %s → %s (will shut down on exit)\n", cfg.tailscaleMode, publicURL, httpURL(cfg.addr))
		defer func() {
			if err := tsDown(); err != nil {
				fmt.Fprintf(os.Stderr, "tailscale %s teardown: %v\n", cfg.tailscaleMode, err)
			} else {
				fmt.Fprintf(os.Stderr, "tailscale %s: shut down\n", cfg.tailscaleMode)
			}
		}()
	}

	if err := s.setupOAuth(cfg.oauthMode, publicURL); err != nil {
		return err
	}
	if err := s.printHTTPStartup(os.Stderr, os.Stdout, cfg.addr, publicURL); err != nil {
		return err
	}
	return s.ServeHTTP(ctx, cfg.addr)
}

// printHTTPStartup writes the boot banner to stderr and, when OAuth is on, the
// connection details (including the pairing code) to stdout. It is the single
// place the OAuth-on/off output decision lives.
func (s *Server) printHTTPStartup(stderr, stdout io.Writer, addr, publicURL string) error {
	if s.oauth == nil {
		_, _ = fmt.Fprintln(stderr, s.startupBannerHTTP(addr))
		return nil
	}
	_, _ = fmt.Fprintln(stderr, s.startupBannerHTTPOAuth(addr))
	return s.writeOAuthBootInfo(stdout, addr, publicURL)
}

// stdinIsInteractive reports whether stdin is a terminal (a human typed the
// command) rather than a pipe (an MCP host is driving the protocol). It uses a
// dependency-free char-device check so the bridge stays lean.
func stdinIsInteractive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// setupHint is the registration guidance shown when the server is run by hand.
// It emits a ready-to-paste MCP client config (the universal mcpServers shape)
// using the server's own absolute path, plus the Claude Code one-liner — so the
// reader (or an LLM they paste it into) has everything needed to wire it up.
func (s *Server) setupHint() string {
	name, exec := s.bannerName(), s.executable()
	return fmt.Sprintf(`This is an MCP server: it speaks JSON-RPC over stdin/stdout and is meant to be
launched by an MCP client, not run by hand. To register it, add this to your MCP
client config (Claude Desktop / Cursor / VS Code / Windsurf / …):

    %s

…or, with the Claude Code CLI:

    %s

Now waiting for an MCP client to connect on stdin — press Ctrl-C to exit.`,
		mcpServersConfig(name, exec, true), claudeMcpAddLine(name, exec))
}
