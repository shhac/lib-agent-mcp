package agentmcp

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Tool is an MCP tool descriptor derived from a cobra command.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`

	path  []string       // command path relative to root, e.g. ["item", "get"]
	group bool           // group tool: dispatch subcommands via args[0] + "help" verb
	cmd   *cobra.Command // the cobra command (group or leaf) this tool maps to; the
	//                      call-time --yes decision reads commandConfirms(cmd)

	// handler, when set, makes this a native tool: it is served in-process by the
	// bridge rather than by re-execing a cobra command. cmd/path/group are unused
	// for native tools. The file tool (fs) is the first such tool.
	handler nativeHandler
}

// nativeHandler serves a native tool call in-process. args is the positional
// argument vector (args[0] is the verb for a group-shaped native tool); opts is
// the typed options object. It returns a fully-formed tool result.
type nativeHandler func(ctx context.Context, args []string, opts map[string]any) toolResult

var skipCommands = map[string]bool{
	"help":             true,
	"completion":       true,
	"__complete":       true,
	"__completeNoDesc": true,
	"mcp":              true,
}

// buildTools derives the tool list. When any command opts in via Expose, only
// the exposed boundaries become tools (a group → one coarse tool with subcommand
// dispatch, a leaf → its own tool). When nothing is exposed, it falls back to
// legacy reflect-all (one tool per runnable leaf), so un-migrated CLIs keep
// working. Hidden, plumbing, and Skip'd commands are always excluded.
func (s *Server) buildTools() []Tool {
	var tools []Tool
	if s.anyExposed() {
		tools = s.buildExposedTools()
	} else {
		tools = s.buildLegacyTools()
	}
	return append(tools, s.nativeTools()...)
}

func (s *Server) buildLegacyTools() []Tool {
	var tools []Tool
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, sub := range visibleSubs(cmd) {
			if sub.Runnable() {
				tools = append(tools, s.toolFor(sub))
			}
			walk(sub)
		}
	}
	walk(s.root)
	return tools
}

// buildExposedTools emits one tool per Expose'd boundary: a group node becomes a
// coarse tool that dispatches its subcommands; a leaf becomes its own tool. An
// exposed node is a boundary — its subtree is reached through it, not as more
// tools — so place Expose at exactly the granularity you want.
func (s *Server) buildExposedTools() []Tool {
	var tools []Tool
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, sub := range visibleSubs(cmd) {
			if isExposed(sub) {
				if hasRunnableSub(sub) {
					tools = append(tools, s.toolForGroup(sub))
				} else if sub.Runnable() {
					tools = append(tools, s.toolFor(sub))
				}
				continue // boundary: do not descend into an exposed node
			}
			walk(sub)
		}
	}
	walk(s.root)
	return tools
}

func (s *Server) toolFor(cmd *cobra.Command) Tool {
	parts := commandPathParts(cmd)

	desc := cmd.Short
	if cmd.Long != "" {
		desc = cmd.Long
	}

	optionProps := map[string]any{}
	var optionRequired []string

	// Local flags plus inherited persistent flags, so a domain-level persistent
	// flag (e.g. a root --project / --workspace / --profile) is a usable tool
	// input; the noisy infra globals (format/debug/timeout/help and any
	// WithHiddenFlags) and --yes are dropped by the flag filter.
	s.visitFlags(cmd, func(f *pflag.Flag, required bool) {
		optionProps[f.Name] = flagSchema(f)
		if required {
			optionRequired = append(optionRequired, f.Name)
		}
	})

	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"args":    argsArraySchema(positionalDescription(cmd)),
			"options": optionsObjectSchema(optionProps, optionRequired),
		},
	}

	t := Tool{
		Name:        strings.Join(parts, s.opts.nameSeparator),
		Description: desc,
		InputSchema: input,
		path:        parts,
		cmd:         cmd,
	}
	t.Annotations = leafAnnotations(cmd)
	return t
}

// toolForGroup builds a coarse tool for an exposed group command: one tool that
// dispatches the group's subcommands via args[0], with a "help" verb (also the
// default when args is empty or names an unknown subcommand) that lists the
// subcommands and their flags. lin/issue, args:["get","ENG-123"] runs
// `lin issue get ENG-123`.
func (s *Server) toolForGroup(group *cobra.Command) Tool {
	parts := commandPathParts(group)
	name := strings.Join(parts, s.opts.nameSeparator)

	desc := strings.TrimSpace(group.Short)
	if subs := subNames(group); len(subs) > 0 {
		desc = strings.TrimSpace(desc + " — subcommands: " + strings.Join(subs, ", ") +
			`. Call with args:["<subcommand>", …]; use args:["help"] for full usage and flags.`)
	}

	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"args": argsArraySchema(`The subcommand and its arguments/flags, e.g. ["get","ENG-123"] or ` +
				`["create","--title","X"]. Use ["help"] to list subcommands and flags.`),
		},
		"required": []string{"args"},
	}

	t := Tool{
		Name:        name,
		Description: desc,
		InputSchema: input,
		path:        parts,
		group:       true,
		cmd:         group,
	}
	if groupHasDestructive(group) {
		t.Annotations = map[string]any{"destructiveHint": true}
	}
	return t
}


