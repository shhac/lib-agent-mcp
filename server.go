package agentmcp

import (
	"os"

	"github.com/spf13/cobra"
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
)

type options struct {
	name          string
	version       string
	nameSeparator string
	hiddenFlags   map[string]bool
	executable    string
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

// WithExecutable overrides the binary used to run tool calls. Defaults to the
// running binary (os.Executable); primarily useful in tests.
func WithExecutable(path string) Option {
	return func(o *options) { o.executable = path }
}

// Server serves a cobra command tree over the MCP stdio transport.
type Server struct {
	root *cobra.Command
	opts options
}

var defaultHiddenFlags = []string{"format", "debug", "timeout", "help"}

func newServer(root *cobra.Command, opts ...Option) *Server {
	o := options{
		name:          root.Name(),
		version:       rootVersion(root),
		nameSeparator: "_",
		hiddenFlags:   map[string]bool{},
	}
	for _, f := range defaultHiddenFlags {
		o.hiddenFlags[f] = true
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &Server{root: root, opts: o}
}

// Command returns an "mcp" subcommand that serves root's command tree over
// stdio. Add it to your root command:
//
//	root.AddCommand(agentmcp.Command(root))
func Command(root *cobra.Command, opts ...Option) *cobra.Command {
	s := newServer(root, opts...)
	return &cobra.Command{
		Use:         "mcp",
		Short:       "Run as an MCP server over stdio",
		Annotations: map[string]string{AnnotationSkip: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return s.Serve(cmd.Context(), os.Stdin, os.Stdout)
		},
	}
}

func rootVersion(root *cobra.Command) string {
	if root.Version != "" {
		return root.Version
	}
	return "0.0.0"
}
