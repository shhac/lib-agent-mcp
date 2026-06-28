package agentmcp

import (
	"fmt"
	"os"

	output "github.com/shhac/lib-agent-output"
	"github.com/spf13/cobra"
)

// File tool defaults.
const (
	// defaultFileToolName is the name of the read-only file tool when a CLI opts
	// in via WithFileRoots without overriding it.
	defaultFileToolName = "fs"
	// defaultFileInlineLimit caps the bytes a single get will base64-inline.
	// Kept small: inlined bytes are base64-expanded into the client's context.
	defaultFileInlineLimit = 5 << 20 // 5 MiB
)

// Annotation keys recognised on cobra commands and flags.
const (
	// AnnotationSkip on a command hides it (and only it) from the tool list.
	AnnotationSkip = "mcp.skip"
	// AnnotationReadOnly marks a command as side-effect-free; surfaced as the
	// MCP readOnlyHint annotation.
	AnnotationReadOnly = "mcp.readonly"
	// AnnotationDestructive marks a command destructive even without a --yes
	// flag; surfaced as destructiveHint and triggers --yes injection on call.
	AnnotationDestructive = "mcp.destructive"
	// AnnotationFlagHidden on a flag hides it from the generated input schema.
	AnnotationFlagHidden = "mcp.hidden"
	// AnnotationExpose marks a command as an MCP tool boundary (opt-in). A group
	// command becomes one coarse tool that dispatches its subcommands via args
	// (with a "help" verb); a leaf becomes its own tool. When NO command in the
	// tree is exposed, the server falls back to legacy reflect-all (one tool per
	// runnable leaf), so un-migrated CLIs keep working.
	AnnotationExpose = "mcp.expose"
)

// Expose marks cmd as an MCP tool boundary (opt-in): the agent-facing surface is
// only what you Expose, so credential/config/usage commands stay invisible to
// agents unless deliberately surfaced. Expose a group to get one coarse tool with
// subcommand dispatch + a "help" verb; expose a leaf for a standalone tool. The
// annotation is MCP-only — cobra ignores it for CLI help and execution.
func Expose(cmd *cobra.Command) { setAnnotation(cmd, AnnotationExpose, "true") }

// Skip hides cmd (and only it) from the generated tool list / a group's
// subcommand dispatch. MCP-only; the CLI is unaffected.
func Skip(cmd *cobra.Command) { setAnnotation(cmd, AnnotationSkip, "true") }

// Destructive marks cmd as destructive, surfacing the MCP destructiveHint so the
// host confirms before the call. Use it for mutating commands that have no --yes
// confirmation flag of their own; commands that DO define --yes are detected
// automatically. MCP-only; the CLI is unaffected.
func Destructive(cmd *cobra.Command) { setAnnotation(cmd, AnnotationDestructive, "true") }

// ReadOnly marks cmd as side-effect-free, surfacing the MCP readOnlyHint.
// MCP-only; the CLI is unaffected.
func ReadOnly(cmd *cobra.Command) { setAnnotation(cmd, AnnotationReadOnly, "true") }

func setAnnotation(cmd *cobra.Command, key, val string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[key] = val
}

type options struct {
	name          string
	version       string
	nameSeparator string
	hiddenFlags   map[string]bool
	executable    string

	fileRoots       []output.FileRoot
	fileToolName    string
	fileInlineLimit int64
}

// Option configures the MCP server.
type Option func(*options)

// WithName overrides the server name reported during initialize (defaults to
// the root command's name).
func WithName(name string) Option { return func(o *options) { o.name = name } }

// WithVersion overrides the server version reported during initialize.
func WithVersion(v string) Option { return func(o *options) { o.version = v } }

// WithNameSeparator sets the separator joining a command path into a tool name
// (default "_", producing e.g. item_get).
func WithNameSeparator(sep string) Option {
	return func(o *options) { o.nameSeparator = sep }
}

// WithHiddenFlags hides additional flags (by name) from every tool's schema,
// on top of the defaults (format, debug, timeout, help).
func WithHiddenFlags(names ...string) Option {
	return func(o *options) {
		for _, n := range names {
			o.hiddenFlags[n] = true
		}
	}
}

// WithFileRoots opts the server into the read-only file tool (named "fs" by
// default; see WithFileToolName), exposing each named root for the agent to
// list and read files from. Without at least one root the tool is absent, so
// file access is strictly opt-in per CLI. Paths are always addressed relative
// to a root; the host path is never shown to the agent.
func WithFileRoots(roots ...output.FileRoot) Option {
	return func(o *options) { o.fileRoots = append(o.fileRoots, roots...) }
}

// WithFileToolName overrides the file tool's name (default "fs"). Useful when a
// CLI already has a command that would collide, or prefers a domain name.
func WithFileToolName(name string) Option {
	return func(o *options) { o.fileToolName = name }
}

// WithFileInlineLimit caps the byte size the file tool will base64-inline in a
// single get (default defaultFileInlineLimit). It is deliberately far below any
// upload ceiling because inlined bytes cost the client context tokens; a get
// over the cap returns a structured error rather than a giant payload.
func WithFileInlineLimit(bytes int64) Option {
	return func(o *options) { o.fileInlineLimit = bytes }
}

// WithExecutable overrides the binary used to run tool calls. Defaults to the
// running binary (os.Executable); primarily useful in tests.
func WithExecutable(path string) Option {
	return func(o *options) { o.executable = path }
}

// Server serves a cobra command tree over the MCP stdio transport. The tool
// list is derived once at construction (the command tree is static after
// setup) and reused for every tools/list and tools/call.
type Server struct {
	root        *cobra.Command
	opts        options
	tools       []Tool
	toolsByName map[string]*Tool
}

var defaultHiddenFlags = []string{"format", "debug", "timeout", "help"}

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
	var httpAddr string
	cmd := &cobra.Command{
		Use:         "mcp",
		Short:       "Run as an MCP server (stdio by default, or --http <addr>)",
		Annotations: map[string]string{AnnotationSkip: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The Streamable HTTP transport is unauthenticated: it is opt-in via
			// --http and prints a warning so it isn't exposed unguarded.
			if httpAddr != "" {
				fmt.Fprintln(os.Stderr, s.startupBannerHTTP(httpAddr))
				return s.ServeHTTP(cmd.Context(), httpAddr)
			}
			// Boot notice goes to STDERR: stdout carries the JSON-RPC stream and
			// any non-protocol byte there would corrupt the client's parser.
			fmt.Fprintln(os.Stderr, s.startupBanner())
			// When a human ran this directly (a TTY on stdin, no MCP host driving
			// it), they can't do anything useful — the server just waits for
			// JSON-RPC. Print the config needed to register it instead, so the
			// output is self-describing (paste-able into an LLM or a client config).
			if stdinIsInteractive() {
				fmt.Fprintln(os.Stderr, "\n"+s.setupHint())
			}
			return s.Serve(cmd.Context(), os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&httpAddr, "http", "",
		"Serve the Streamable HTTP transport on this address (e.g. :8000) instead of stdio. "+
			"Unauthenticated — bind to loopback or front with an auth proxy.")
	return cmd
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
	name := s.opts.name
	if name == "" {
		name = "mcp"
	}
	exec := s.executable()
	return fmt.Sprintf(`This is an MCP server: it speaks JSON-RPC over stdin/stdout and is meant to be
launched by an MCP client, not run by hand. To register it, add this to your MCP
client config (Claude Desktop / Cursor / VS Code / Windsurf / …):

    {
      "mcpServers": {
        %q: {
          "command": %q,
          "args": ["mcp"]
        }
      }
    }

…or, with the Claude Code CLI:

    claude mcp add %s -- %s mcp

Now waiting for an MCP client to connect on stdin — press Ctrl-C to exit.`,
		name, exec, name, exec)
}

// startupBanner is the one-line notice written to stderr when the stdio server
// boots, so an operator watching the process sees that it came up, what it is,
// and how it's listening.
func (s *Server) startupBanner() string {
	return fmt.Sprintf("%s %s — MCP server ready · transport: stdio · %s · protocol %s",
		s.bannerName(), s.bannerVersion(), s.toolCountPhrase(), defaultProtocolVersion)
}

// startupBannerHTTP is the boot notice for the Streamable HTTP transport. It
// names the URL and carries an unmissable warning, since this transport has no
// authorization of its own.
func (s *Server) startupBannerHTTP(addr string) string {
	return fmt.Sprintf("%s %s — MCP server ready · transport: streamable-http · %s · %s · protocol %s\n"+
		"  ⚠ UNAUTHENTICATED: anyone who can reach this address can call every tool — "+
		"bind to loopback or front with an auth proxy/tunnel.",
		s.bannerName(), s.bannerVersion(), httpURL(addr), s.toolCountPhrase(), defaultProtocolVersion)
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
