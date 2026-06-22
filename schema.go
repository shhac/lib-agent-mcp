package agentmcp

import (
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

	path          []string // command path relative to root, e.g. ["item", "get"]
	injectConfirm bool     // command has a --yes flag → inject it when called
}

var skipCommands = map[string]bool{
	"help":             true,
	"completion":       true,
	"__complete":       true,
	"__completeNoDesc": true,
	"mcp":              true,
}

// buildTools walks the command tree and emits one tool per runnable leaf
// command, skipping hidden, plumbing, and explicitly-skipped commands.
func (s *Server) buildTools() []Tool {
	var tools []Tool
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, sub := range cmd.Commands() {
			if sub.Hidden || skipCommands[sub.Name()] || sub.Annotations[AnnotationSkip] == "true" {
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

func (s *Server) toolFor(cmd *cobra.Command) Tool {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) > 0 {
		parts = parts[1:] // drop the root command's own name
	}

	desc := cmd.Short
	if cmd.Long != "" {
		desc = cmd.Long
	}

	optionProps := map[string]any{}
	var optionRequired []string
	hasConfirm := false
	annotatedDestructive := cmd.Annotations[AnnotationDestructive] == "true"

	visit := func(f *pflag.Flag) {
		if f.Name == "yes" {
			hasConfirm = true // bridge injects --yes on call; keep it out of the schema
			return
		}
		if f.Hidden || s.opts.hiddenFlags[f.Name] || f.Annotations[AnnotationFlagHidden] != nil {
			return
		}
		if _, seen := optionProps[f.Name]; seen {
			return // a local flag shadows an inherited one of the same name
		}
		optionProps[f.Name] = flagSchema(f)
		if _, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok {
			optionRequired = append(optionRequired, f.Name)
		}
	}
	// Local flags plus inherited persistent flags, so a domain-level persistent
	// flag (e.g. a root --project / --workspace / --profile) is a usable tool
	// input; the noisy infra globals (format/debug/timeout/help and any
	// WithHiddenFlags) are dropped by the hidden-flag filter above.
	cmd.LocalFlags().VisitAll(visit)
	cmd.InheritedFlags().VisitAll(visit)

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
	}
	if len(annotations) > 0 {
		t.Annotations = annotations
	}
	return t
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
