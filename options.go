package agentmcp

import output "github.com/shhac/lib-agent-output"

// File tool defaults.
const (
	// defaultFileToolName is the name of the read-only file tool when a CLI opts
	// in via WithFileRoots without overriding it.
	defaultFileToolName = "fs"
	// defaultFileInlineLimit caps the bytes a single get will base64-inline.
	// Kept small: inlined bytes are base64-expanded into the client's context.
	defaultFileInlineLimit = 5 << 20 // 5 MiB
)

// defaultHiddenFlags are the flags hidden from every tool schema unless a CLI
// re-surfaces them; WithHiddenFlags adds to this set.
var defaultHiddenFlags = []string{"format", "debug", "timeout", "help"}

type options struct {
	name          string
	version       string
	nameSeparator string
	hiddenFlags   map[string]bool
	executable    string

	fileRoots       []output.FileRoot
	fileToolName    string
	fileInlineLimit int64

	// oauthKeyringService overrides the keyring service id under which the
	// local-OAuth secrets are stored (default "<name>.mcp"). It is kept distinct
	// from a CLI's own API-credential service so the two never mix.
	oauthKeyringService string
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

// WithOAuthKeyringService overrides the keyring service id under which the
// local-OAuth layer stores its secrets (default "<root-name>.mcp"). Set it to a
// reverse-DNS id matching your app's convention; it must differ from the CLI's
// own credential service so the two trust domains stay separate.
func WithOAuthKeyringService(service string) Option {
	return func(o *options) { o.oauthKeyringService = service }
}

// WithExecutable overrides the binary used to run tool calls. Defaults to the
// running binary (os.Executable); primarily useful in tests.
func WithExecutable(path string) Option {
	return func(o *options) { o.executable = path }
}
