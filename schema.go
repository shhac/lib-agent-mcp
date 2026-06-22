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

	path          []string        // command path relative to root, e.g. ["item", "get"]
	injectConfirm bool            // leaf: command has a --yes flag → inject it when called
	group         bool            // group tool: dispatch subcommands via args[0] + "help" verb
	cmd           *cobra.Command  // the cobra command (group or leaf) this tool maps to
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
		for _, sub := range cmd.Commands() {
			if excluded(sub) {
				continue
			}
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
		for _, sub := range cmd.Commands() {
			if excluded(sub) {
				continue
			}
			if sub.Annotations[AnnotationExpose] == "true" {
				if s.hasRunnableSub(sub) {
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

func (s *Server) anyExposed() bool {
	found := false
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, sub := range cmd.Commands() {
			if found {
				return
			}
			if sub.Annotations[AnnotationExpose] == "true" {
				found = true
				return
			}
			walk(sub)
		}
	}
	walk(s.root)
	return found
}

// hasRunnableSub reports whether cmd has at least one reachable runnable
// subcommand (so it should be a group tool rather than a leaf tool).
func (s *Server) hasRunnableSub(cmd *cobra.Command) bool {
	for _, sub := range cmd.Commands() {
		if excluded(sub) {
			continue
		}
		if sub.Runnable() || s.hasRunnableSub(sub) {
			return true
		}
	}
	return false
}

func (s *Server) toolFor(cmd *cobra.Command) Tool {
	parts := commandPathParts(cmd)

	desc := cmd.Short
	if cmd.Long != "" {
		desc = cmd.Long
	}

	optionProps := map[string]any{}
	var optionRequired []string
	annotatedDestructive := cmd.Annotations[AnnotationDestructive] == "true"

	// Local flags plus inherited persistent flags, so a domain-level persistent
	// flag (e.g. a root --project / --workspace / --profile) is a usable tool
	// input; the noisy infra globals (format/debug/timeout/help and any
	// WithHiddenFlags) are dropped by the hidden-flag filter.
	hasConfirm := s.visitFlags(cmd, func(f *pflag.Flag, required bool) {
		optionProps[f.Name] = flagSchema(f)
		if required {
			optionRequired = append(optionRequired, f.Name)
		}
	})

	// destructiveHint is the broader signal (host should confirm); injecting
	// --yes only makes sense when the command actually defines that flag.
	destructive := hasConfirm || annotatedDestructive

	optionsSchema := map[string]any{
		"type":       "object",
		"properties": optionProps,
	}
	if len(optionRequired) > 0 {
		optionsSchema["required"] = optionRequired
	}

	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"args": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": positionalDescription(cmd),
			},
			"options": optionsSchema,
		},
	}

	annotations := map[string]any{}
	if cmd.Annotations[AnnotationReadOnly] == "true" {
		annotations["readOnlyHint"] = true
	}
	if destructive {
		annotations["destructiveHint"] = true
	}

	t := Tool{
		Name:          strings.Join(parts, s.opts.nameSeparator),
		Description:   desc,
		InputSchema:   input,
		path:          parts,
		injectConfirm: hasConfirm,
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
// persistent, de-duped, minus hidden/infra flags and --yes. It returns whether
// cmd defines a --yes confirm flag (so the caller can inject it on a host-
// confirmed call).
func (s *Server) visitFlags(cmd *cobra.Command, fn func(f *pflag.Flag, required bool)) bool {
	hasConfirm := false
	seen := map[string]bool{}
	visit := func(f *pflag.Flag) {
		if f.Name == "yes" {
			hasConfirm = true
			return
		}
		if f.Hidden || s.opts.hiddenFlags[f.Name] || f.Annotations[AnnotationFlagHidden] != nil {
			return
		}
		if seen[f.Name] {
			return
		}
		seen[f.Name] = true
		_, required := f.Annotations[cobra.BashCompOneRequiredFlag]
		fn(f, required)
	}
	cmd.LocalFlags().VisitAll(visit)
	cmd.InheritedFlags().VisitAll(visit)
	return hasConfirm
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
	if subs := s.subNames(group); len(subs) > 0 {
		desc = strings.TrimSpace(desc + " — subcommands: " + strings.Join(subs, ", ") +
			`. Call with args:["<subcommand>", …]; use args:["help"] for full usage and flags.`)
	}

	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"args": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": `The subcommand and its arguments/flags, e.g. ["get","ENG-123"] or ` +
					`["create","--title","X"]. Use ["help"] to list subcommands and flags.`,
			},
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
	if s.groupHasDestructive(group) {
		t.Annotations = map[string]any{"destructiveHint": true}
	}
	return t
}

// subNames lists the immediate runnable/visible subcommand names of cmd.
func (s *Server) subNames(cmd *cobra.Command) []string {
	var names []string
	for _, sub := range cmd.Commands() {
		if excluded(sub) {
			continue
		}
		names = append(names, sub.Name())
	}
	return names
}

// groupHasDestructive reports whether any reachable subcommand of group is
// destructive (defines --yes or is annotated destructive).
func (s *Server) groupHasDestructive(group *cobra.Command) bool {
	for _, sub := range group.Commands() {
		if excluded(sub) {
			continue
		}
		if sub.Annotations[AnnotationDestructive] == "true" || sub.Flags().Lookup("yes") != nil {
			return true
		}
		if s.groupHasDestructive(sub) {
			return true
		}
	}
	return false
}

// groupHelp renders the usage for an exposed group: each subcommand with its
// positional args, short description, and schema-visible flags. It is the
// payload of the "help" verb (and the fallback for an empty/unknown subcommand).
func (s *Server) groupHelp(group *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n\n", strings.Join(commandPathParts(group), " "), group.Short)
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
				s.visitFlags(sub, func(f *pflag.Flag, required bool) {
					req := ""
					if required {
						req = " (required)"
					}
					fmt.Fprintf(&b, "%s    --%s <%s>  %s%s\n", indent, f.Name, f.Value.Type(), f.Usage, req)
				})
			}
			if s.hasRunnableSub(sub) {
				render(sub, depth+1)
			}
		}
	}
	render(group, 0)
	return b.String()
}

func positionalDescription(cmd *cobra.Command) string {
	use := strings.TrimSpace(cmd.Use)
	if i := strings.IndexByte(use, ' '); i >= 0 {
		return "Positional arguments: " + strings.TrimSpace(use[i+1:])
	}
	return "Positional arguments"
}

func flagSchema(f *pflag.Flag) map[string]any {
	schema := map[string]any{"description": f.Usage}
	switch t := f.Value.Type(); t {
	case "bool":
		schema["type"] = "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "count":
		schema["type"] = "integer"
	case "float32", "float64":
		schema["type"] = "number"
	default:
		if strings.HasSuffix(t, "Slice") || strings.HasSuffix(t, "Array") {
			schema["type"] = "array"
			schema["items"] = map[string]any{"type": sliceItemType(t)}
		} else {
			schema["type"] = "string"
		}
	}
	return schema
}

func sliceItemType(flagType string) string {
	switch {
	case strings.HasPrefix(flagType, "bool"):
		return "boolean"
	case strings.HasPrefix(flagType, "int"), strings.HasPrefix(flagType, "uint"):
		return "integer"
	case strings.HasPrefix(flagType, "float"):
		return "number"
	default:
		return "string"
	}
}
