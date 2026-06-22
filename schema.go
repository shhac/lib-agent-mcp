package agentmcp

import (
	"fmt"
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

	path          []string       // command path relative to root, e.g. ["item", "get"]
	injectConfirm bool           // leaf: command has a --yes flag → inject it when called
	group         bool           // group tool: dispatch subcommands via args[0] + "help" verb
	cmd           *cobra.Command // the cobra command (group or leaf) this tool maps to
}

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
	if s.anyExposed() {
		return s.buildExposedTools()
	}
	return s.buildLegacyTools()
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
			if sub.Annotations[AnnotationExpose] == "true" {
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

// excluded reports whether a command is always kept out of the tool surface.
func excluded(cmd *cobra.Command) bool {
	return cmd.Hidden || skipCommands[cmd.Name()] || cmd.Annotations[AnnotationSkip] == "true"
}

// visibleSubs returns cmd's immediate subcommands that aren't excluded from the
// tool surface. It is the single definition of "the commands a walk should see",
// so the exclusion filter can't drift across the several callers that walk the
// tree.
func visibleSubs(cmd *cobra.Command) []*cobra.Command {
	subs := cmd.Commands()
	kept := subs[:0:0]
	for _, sub := range subs {
		if !excluded(sub) {
			kept = append(kept, sub)
		}
	}
	return kept
}

// anyCommand reports whether pred holds for any command reachable from cmd
// (descending through visible subcommands), short-circuiting on the first match.
func anyCommand(cmd *cobra.Command, pred func(*cobra.Command) bool) bool {
	for _, sub := range visibleSubs(cmd) {
		if pred(sub) || anyCommand(sub, pred) {
			return true
		}
	}
	return false
}

func (s *Server) anyExposed() bool {
	return anyCommand(s.root, isExposed)
}

// isExposed reports whether cmd is marked as an MCP tool boundary.
func isExposed(cmd *cobra.Command) bool {
	return cmd.Annotations[AnnotationExpose] == "true"
}

// commandConfirms reports whether cmd defines a --yes confirmation flag — the
// single signal for "inject --yes on a host-confirmed call". Both schema
// generation and call-time dispatch read this one predicate so they can't drift.
func commandConfirms(cmd *cobra.Command) bool {
	return cmd.Flags().Lookup("yes") != nil
}

// commandDestructive reports whether cmd should carry destructiveHint: it either
// gates itself with --yes or is annotated mcp.destructive. Broader than
// commandConfirms — a command can be destructive without a --yes flag to inject.
func commandDestructive(cmd *cobra.Command) bool {
	return commandConfirms(cmd) || cmd.Annotations[AnnotationDestructive] == "true"
}

// hasRunnableSub reports whether cmd has at least one reachable runnable
// subcommand (so it should be a group tool rather than a leaf tool).
func hasRunnableSub(cmd *cobra.Command) bool {
	return anyCommand(cmd, (*cobra.Command).Runnable)
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

	annotations := map[string]any{}
	if cmd.Annotations[AnnotationReadOnly] == "true" {
		annotations["readOnlyHint"] = true
	}
	if commandDestructive(cmd) {
		annotations["destructiveHint"] = true
	}

	t := Tool{
		Name:          strings.Join(parts, s.opts.nameSeparator),
		Description:   desc,
		InputSchema:   input,
		path:          parts,
		injectConfirm: commandConfirms(cmd),
		cmd:           cmd,
	}
	if len(annotations) > 0 {
		t.Annotations = annotations
	}
	return t
}

// commandPathParts is the command path relative to root (root's own name
// dropped), e.g. ["item", "get"].
func commandPathParts(cmd *cobra.Command) []string {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) > 0 {
		parts = parts[1:]
	}
	return parts
}

// visitFlags calls fn for each schema-visible flag of cmd — local plus inherited
// persistent, de-duped, minus the flags flagSkipped hides (infra globals, --yes,
// mcp.hidden).
func (s *Server) visitFlags(cmd *cobra.Command, fn func(f *pflag.Flag, required bool)) {
	seen := map[string]bool{}
	visit := func(f *pflag.Flag) {
		if s.flagSkipped(f) || seen[f.Name] {
			return
		}
		seen[f.Name] = true
		fn(f, flagRequired(f))
	}
	cmd.LocalFlags().VisitAll(visit)
	cmd.InheritedFlags().VisitAll(visit)
}

// visitLocalFlags visits a command's own (non-inherited) schema-visible flags
// only. Group help uses it so each subcommand line shows just its distinctive
// flags; the flags every subcommand inherits are listed once at the group level.
func (s *Server) visitLocalFlags(cmd *cobra.Command, fn func(f *pflag.Flag, required bool)) {
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if s.flagSkipped(f) {
			return
		}
		fn(f, flagRequired(f))
	})
}

// flagSkipped reports whether a flag is kept out of every schema/help surface:
// the --yes confirm flag (the bridge injects it, the model must not set it),
// cobra-hidden flags, infra globals (format/debug/timeout/help or
// WithHiddenFlags), or flags marked mcp.hidden.
func (s *Server) flagSkipped(f *pflag.Flag) bool {
	return f.Name == "yes" || f.Hidden || s.opts.hiddenFlags[f.Name] ||
		f.Annotations[AnnotationFlagHidden] != nil
}

// flagRequired reports whether cobra marked f as a required flag.
func flagRequired(f *pflag.Flag) bool {
	_, required := f.Annotations[cobra.BashCompOneRequiredFlag]
	return required
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

// subNames lists the immediate visible subcommand names of cmd.
func subNames(cmd *cobra.Command) []string {
	subs := visibleSubs(cmd)
	names := make([]string, 0, len(subs))
	for _, sub := range subs {
		names = append(names, sub.Name())
	}
	return names
}

// groupHasDestructive reports whether any reachable subcommand of group is
// destructive (defines --yes or is annotated destructive).
func groupHasDestructive(group *cobra.Command) bool {
	return anyCommand(group, commandDestructive)
}

// groupHelp renders the usage for an exposed group: each subcommand with its
// positional args, short description, and schema-visible flags. It is the
// payload of the "help" verb (and the fallback for an empty/unknown subcommand).
func (s *Server) groupHelp(group *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n\n", strings.Join(commandPathParts(group), " "), group.Short)

	// Flags every subcommand inherits (e.g. domain persistent flags) are listed
	// once here rather than repeated on each subcommand line below.
	var common []string
	group.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if s.flagSkipped(f) {
			return
		}
		common = append(common, "  "+formatFlagLine(f, false))
	})
	if len(common) > 0 {
		b.WriteString("Common flags (apply to every subcommand):\n")
		b.WriteString(strings.Join(common, "\n"))
		b.WriteString("\n\n")
	}

	b.WriteString("Subcommands (pass as args, e.g. args:[\"get\",\"ENG-123\"]):\n")
	var render func(cmd *cobra.Command, depth int)
	render = func(cmd *cobra.Command, depth int) {
		for _, sub := range cmd.Commands() {
			if excluded(sub) {
				continue
			}
			indent := strings.Repeat("  ", depth+1)
			fmt.Fprintf(&b, "%s%s — %s\n", indent, strings.TrimSpace(sub.Use), sub.Short)
			if sub.Runnable() {
				s.visitLocalFlags(sub, func(f *pflag.Flag, required bool) {
					fmt.Fprintf(&b, "%s    %s\n", indent, formatFlagLine(f, required))
				})
			}
			if hasRunnableSub(sub) {
				render(sub, depth+1)
			}
		}
	}
	render(group, 0)
	return b.String()
}

// formatFlagLine renders one flag as "--name <type>  usage", with a trailing
// " (required)" when required. Callers prepend their own indentation.
func formatFlagLine(f *pflag.Flag, required bool) string {
	req := ""
	if required {
		req = " (required)"
	}
	return fmt.Sprintf("--%s <%s>  %s%s", f.Name, f.Value.Type(), f.Usage, req)
}
