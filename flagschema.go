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
