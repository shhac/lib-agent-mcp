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
package agentmcp
