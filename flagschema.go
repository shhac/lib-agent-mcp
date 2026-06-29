package agentmcp

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// This file holds the pure mapping from cobra/pflag shapes to JSON-Schema
// fragments — no Server state, no tree walking. schema.go owns the tree walk and
// Tool assembly; these helpers describe the shape of a single command's input.

func positionalDescription(cmd *cobra.Command) string {
	use := strings.TrimSpace(cmd.Use)
	if i := strings.IndexByte(use, ' '); i >= 0 {
		return "Positional arguments: " + strings.TrimSpace(use[i+1:])
	}
	return "Positional arguments"
}

// argsArraySchema is the JSON-Schema fragment for the positional `args` string
// array, shared by leaf tools (positionals) and group tools (subcommand + args).
func argsArraySchema(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": description,
	}
}

// optionsObjectSchema is the JSON-Schema fragment for a leaf tool's typed
// `options` object, with `required` included only when non-empty.
func optionsObjectSchema(props map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// flagSchema maps a single pflag's type to its JSON-Schema property fragment.
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
