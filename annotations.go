package agentmcp

import "github.com/spf13/cobra"

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
