// Command widget is the kitchen-sink demo for lib-agent-mcp + lib-agent-output.
//
// It deliberately exercises every path that matters:
//
//   - schema generation: all pflag types (string, int, float, bool, slices,
//     duration, count), a required flag, a cobra-hidden flag, an mcp.hidden
//     flag, read-only / destructive annotations, an mcp.destructive command
//     with no --yes flag, and an mcp.skip subtree.
//   - lib-agent-output producers: NDJSONWriter records, WritePagination,
//     WriteMetaLine (custom @counts), WriteNotice, all three fixable_by error
//     classes, PruneEmpty, Print/PrintJSON, WriteList, and Format routing.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	agentmcp "github.com/shhac/lib-agent-mcp"
	output "github.com/shhac/lib-agent-output"
	"github.com/spf13/cobra"
)

//go:embed fixtures.json
var fixturesJSON []byte

type widget struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Status string   `json:"status"`
	Score  float64  `json:"score"`
	Tags   []string `json:"tags,omitempty"`
	Note   string   `json:"note,omitempty"`
}

var (
	widgets    []widget
	configData = map[string]string{"theme": "dark", "page_size": "25"}
)

func find(id string) (widget, bool) {
	for _, w := range widgets {
		if w.ID == id {
			return w, true
		}
	}
	return widget{}, false
}

// listFormat / singleFormat apply the family default (lists → NDJSON, single
// resources → pretty JSON) to the persistent --format flag. Under MCP the
// bridge passes --format jsonl, so both resolve to NDJSON.
func listFormat(cmd *cobra.Command) output.Format {
	f, _ := output.ResolveFormat(cmd.Flag("format").Value.String(), output.FormatNDJSON)
	return f
}

func singleFormat(cmd *cobra.Command) output.Format {
	f, _ := output.ResolveFormat(cmd.Flag("format").Value.String(), output.FormatJSON)
	return f
}

func main() {
	if err := json.Unmarshal(fixturesJSON, &widgets); err != nil {
		panic(err)
	}

	root := &cobra.Command{
		Use:           "widget",
		Short:         "Kitchen-sink cobra CLI exercising lib-agent-mcp",
		Version:       "1.0.0",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringP("format", "f", "", "Output format: json, jsonl")
	root.PersistentFlags().Bool("debug", false, "Enable debug logging") // infra flag; hidden by the bridge

	item, config := itemCommand(), configCommand()
	root.AddCommand(item, config, adminCommand())

	// WIDGET_EXPOSE flips the demo to the opt-in surface so one binary covers
	// both bridge modes: unset → legacy reflect-all (one tool per leaf); set →
	// item and config become coarse group tools. The e2e tests drive each mode.
	if os.Getenv("WIDGET_EXPOSE") != "" {
		agentmcp.Expose(item)
		agentmcp.Expose(config)
	}

	root.AddCommand(agentmcp.Command(root))

	if err := root.Execute(); err != nil {
		output.WriteError(os.Stderr, err)
		os.Exit(1)
	}
}

func itemCommand() *cobra.Command {
	item := &cobra.Command{Use: "item", Short: "Manage widgets"}

	list := &cobra.Command{
		Use:         "list",
		Short:       "List widgets",
		Annotations: map[string]string{agentmcp.AnnotationReadOnly: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			status, _ := cmd.Flags().GetString("status")
			tags, _ := cmd.Flags().GetStringSlice("tag")

			var items []any
			matched := 0
			for _, w := range widgets {
				if status != "" && w.Status != status {
					continue
				}
				if len(tags) > 0 && !hasAnyTag(w, tags) {
					continue
				}
				matched++
				items = append(items, w)
			}
			hasMore := false
			if limit > 0 && len(items) > limit {
				items, hasMore = items[:limit], true
			}
			// Demonstrate a custom @-meta line and a structured stderr notice
			// alongside the standard @pagination trailer.
			meta := map[string]any{
				output.MetaKeyPagination: output.Pagination{HasMore: hasMore, TotalItems: matched},
				"@counts":                map[string]any{"returned": len(items), "matched": matched},
			}
			output.WriteNotice(os.Stderr, "compact projection; pass --full for raw payloads", "")
			return output.WriteList(os.Stdout, listFormat(cmd), items, meta, output.PruneEmpty)
		},
	}
	list.Flags().Int("limit", 0, "Maximum widgets to return")
	list.Flags().String("status", "", "Filter by status: active or archived")
	list.Flags().StringSlice("tag", nil, "Filter by tag (repeatable)")

	get := &cobra.Command{
		Use:         "get [id]",
		Short:       "Get a widget by id",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{agentmcp.AnnotationReadOnly: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			w, ok := find(args[0])
			if !ok {
				return output.New(fmt.Sprintf("widget %q not found", args[0]), output.FixableByAgent).
					WithHint("list ids with 'widget item list'")
			}
			return output.Print(os.Stdout, w, singleFormat(cmd), output.PruneEmpty)
		},
	}

	// search exercises the full pflag type surface plus hidden-flag handling.
	search := &cobra.Command{
		Use:         "search",
		Short:       "Search widgets",
		Annotations: map[string]string{agentmcp.AnnotationReadOnly: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			query, _ := cmd.Flags().GetString("query")
			limit, _ := cmd.Flags().GetInt("limit")
			minScore, _ := cmd.Flags().GetFloat64("min-score")

			// Demonstrate the retry class: a sentinel query simulates a
			// transient backend failure.
			if query == "upstream-down" {
				return output.New("search backend temporarily unavailable", output.FixableByRetry).
					WithHint("retry in a few seconds")
			}

			var items []any
			for _, w := range widgets {
				if minScore > 0 && w.Score < minScore {
					continue
				}
				if query != "" && !strings.Contains(strings.ToLower(w.Title), strings.ToLower(query)) {
					continue
				}
				items = append(items, w)
				if limit > 0 && len(items) >= limit {
					break
				}
			}
			return output.WriteList(os.Stdout, listFormat(cmd), items, nil, output.PruneEmpty)
		},
	}
	search.Flags().String("query", "", "Search query (matches title)")
	_ = search.MarkFlagRequired("query")
	search.Flags().Int("limit", 10, "Maximum results")
	search.Flags().Float64("min-score", 0, "Minimum score")
	search.Flags().Bool("fuzzy", false, "Enable fuzzy matching")
	search.Flags().StringSlice("tag", nil, "Restrict to tags (repeatable)")
	search.Flags().Duration("since", 0, "Only items newer than this duration")
	search.Flags().CountP("verbose", "v", "Increase verbosity (repeatable)")
	search.Flags().String("internal-token", "", "Internal use only")
	_ = search.Flags().SetAnnotation("internal-token", agentmcp.AnnotationFlagHidden, []string{"true"})
	search.Flags().Bool("legacy", false, "Deprecated; do not use")
	_ = search.Flags().MarkHidden("legacy")

	del := &cobra.Command{
		Use:   "delete [id]",
		Short: "Delete a widget by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, ok := find(args[0])
			if !ok {
				return output.New(fmt.Sprintf("widget %q not found", args[0]), output.FixableByAgent)
			}
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				return output.New(fmt.Sprintf("refusing to delete widget %q without confirmation", w.ID), output.FixableByHuman).
					WithHint("rerun with --yes")
			}
			return output.NewNDJSONWriter(os.Stdout).WriteItem(map[string]any{"deleted": w.ID})
		},
	}
	del.Flags().Bool("yes", false, "Confirm deletion")

	item.AddCommand(list, get, search, del)
	return item
}

func configCommand() *cobra.Command {
	config := &cobra.Command{Use: "config", Short: "Manage configuration"}

	get := &cobra.Command{
		Use:         "get [key]",
		Short:       "Get configuration (all keys, or one)",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{agentmcp.AnnotationReadOnly: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				v, ok := configData[args[0]]
				if !ok {
					return output.New(fmt.Sprintf("unknown config key %q", args[0]), output.FixableByAgent).
						WithHint("list keys with 'widget config get'")
				}
				return output.Print(os.Stdout, map[string]any{args[0]: v}, singleFormat(cmd), output.PruneEmpty)
			}
			return output.Print(os.Stdout, configData, singleFormat(cmd), output.PruneEmpty)
		},
	}

	// mcp.destructive WITHOUT a --yes flag: destructiveHint is set, but the
	// bridge must NOT inject --yes (the command has no such flag).
	set := &cobra.Command{
		Use:         "set [key] [value]",
		Short:       "Set a configuration value",
		Args:        cobra.ExactArgs(2),
		Annotations: map[string]string{agentmcp.AnnotationDestructive: "true"},
		RunE: func(_ *cobra.Command, args []string) error {
			configData[args[0]] = args[1]
			output.WriteNotice(os.Stderr, "configuration updated", "")
			return output.NewNDJSONWriter(os.Stdout).WriteItem(map[string]any{"set": args[0], "value": args[1]})
		},
	}

	// --yes flag AND mcp.destructive: destructiveHint set and --yes injected.
	reset := &cobra.Command{
		Use:         "reset",
		Short:       "Reset configuration to defaults",
		Annotations: map[string]string{agentmcp.AnnotationDestructive: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				return output.New("refusing to reset configuration without confirmation", output.FixableByHuman).
					WithHint("rerun with --yes")
			}
			configData = map[string]string{"theme": "dark", "page_size": "25"}
			return output.NewNDJSONWriter(os.Stdout).WriteItem(map[string]any{"reset": true})
		},
	}
	reset.Flags().Bool("yes", false, "Confirm reset")

	config.AddCommand(get, set, reset)
	return config
}

// adminCommand is annotated mcp.skip: neither it nor its subcommands should
// appear as MCP tools.
func adminCommand() *cobra.Command {
	admin := &cobra.Command{
		Use:         "admin",
		Short:       "Administrative commands (hidden from MCP)",
		Annotations: map[string]string{agentmcp.AnnotationSkip: "true"},
	}
	admin.AddCommand(&cobra.Command{
		Use:   "secret",
		Short: "Reveal a secret",
		RunE: func(_ *cobra.Command, _ []string) error {
			return output.NewNDJSONWriter(os.Stdout).WriteItem(map[string]any{"secret": "hunter2"})
		},
	})
	return admin
}

func hasAnyTag(w widget, tags []string) bool {
	for _, want := range tags {
		for _, have := range w.Tags {
			if have == want {
				return true
			}
		}
	}
	return false
}
