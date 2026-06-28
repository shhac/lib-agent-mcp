// Package agentmcp exposes an existing spf13/cobra command tree as a Model
// Context Protocol (MCP) server over stdio.
//
// It derives the MCP tool list and input schemas by reflecting over the cobra
// tree — one tool per runnable leaf command, with flag types becoming typed
// parameters — and runs a tool call by executing the same binary as a
// subprocess. Output is interpreted with the lib-agent-output NDJSON contract:
// bare stdout records become structuredContent, @-prefixed lines become
// metadata, and a non-zero exit with a structured stderr error is surfaced as
// an MCP error carrying its fixable_by classification.
//
// Wire it into a CLI with a single line:
//
//	root.AddCommand(agentmcp.Command(root))
//
// The server speaks stdio by default; `mcp --http <addr>` serves the same tools
// over the Streamable HTTP transport (POST /mcp) instead. The HTTP transport is
// unauthenticated — bind it to loopback or front it with an auth proxy.
//
// Beyond the reflected cobra tools, the bridge can serve a native, read-only
// file tool ("fs") for clients without filesystem access. Opt in with
// WithFileRoots; the tool lists and reads files relative to a named root (never
// the host path), and the bridge rewrites absolute paths under a configured
// root in tool output into the same fetchable FileRef shape. See
// design-docs/file-access.md.
package agentmcp
