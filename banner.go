package agentmcp

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// startupBanner is the one-line notice written to stderr when the stdio server
// boots, so an operator watching the process sees that it came up, what it is,
// and how it's listening.
func (s *Server) startupBanner() string {
	return s.bannerCore("stdio", "")
}

// bannerCore builds the common boot-banner line shared by every transport:
// "<name> <version> — MCP server ready · transport: <t> [· <location>] · <tools>
// · protocol <v>". The transport variants append their own warning or suffix.
func (s *Server) bannerCore(transport, location string) string {
	parts := []string{
		fmt.Sprintf("%s %s — MCP server ready", s.bannerName(), s.bannerVersion()),
		"transport: " + transport,
	}
	if location != "" {
		parts = append(parts, location)
	}
	parts = append(parts, s.toolCountPhrase(), "protocol "+defaultProtocolVersion)
	return strings.Join(parts, " · ")
}

// startupBannerHTTP is the boot notice for the Streamable HTTP transport. It
// names the URL and carries an unmissable warning, since this transport has no
// authorization of its own.
func (s *Server) startupBannerHTTP(addr string) string {
	return s.bannerCore("streamable-http", httpURL(addr)) +
		"\n  ⚠ UNAUTHENTICATED: anyone who can reach this address can call every tool — " +
		"bind to loopback or front with an auth proxy/tunnel."
}

// bannerName / bannerVersion / toolCountPhrase are the shared pieces of every
// boot banner, so the stdio and HTTP variants can't drift.
func (s *Server) bannerName() string {
	if s.opts.name == "" {
		return "mcp"
	}
	return s.opts.name
}

func (s *Server) bannerVersion() string {
	if s.opts.version == "" {
		return "dev"
	}
	return s.opts.version
}

func (s *Server) toolCountPhrase() string {
	word := "tools"
	if len(s.tools) == 1 {
		word = "tool"
	}
	return fmt.Sprintf("%d %s", len(s.tools), word)
}

// httpURL renders the MCP endpoint URL for a listen address, defaulting a bare
// ":port" to localhost for a copy-pasteable banner.
func httpURL(addr string) string {
	host := addr
	if len(addr) > 0 && addr[0] == ':' {
		host = "localhost" + addr
	}
	return "http://" + host + mcpHTTPPath
}

func rootVersion(root *cobra.Command) string {
	if root.Version != "" {
		return root.Version
	}
	return "0.0.0"
}
