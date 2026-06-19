// Command widget is a tiny demo cobra CLI that follows the lib-agent-output
// NDJSON contract and exposes itself over MCP via lib-agent-mcp. It exists to
// prove the bridge end to end: `widget mcp` serves the tree, and a tool call
// re-runs `widget item ...` as a subprocess.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

	agentmcp "github.com/shhac/lib-agent-mcp"
	output "github.com/shhac/lib-agent-output"
	"github.com/spf13/cobra"
)

//go:embed fixtures.json
var fixturesJSON []byte

type widget struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

var widgets []widget

func find(id string) (widget, bool) {
	for _, w := range widgets {
		if w.ID == id {
			return w, true
		}
	}
	return widget{}, false
}

func main() {
	if err := json.Unmarshal(fixturesJSON, &widgets); err != nil {
		panic(err)
	}

	var format string
	root := &cobra.Command{
		Use:           "widget",
		Short:         "Demo cobra CLI exercising lib-agent-mcp",
		Version:       "1.0.0",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Persistent infra flag the bridge owns; it must NOT leak into tool schemas.
	root.PersistentFlags().StringVarP(&format, "format", "f", "jsonl", "Output format: json, jsonl")

	item := &cobra.Command{Use: "item", Short: "Manage widgets"}

	list := &cobra.Command{
		Use:         "list",
		Short:       "List widgets",
		Annotations: map[string]string{agentmcp.AnnotationReadOnly: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			status, _ := cmd.Flags().GetString("status")
			w := output.NewNDJSONWriter(os.Stdout)
			n := 0
			for _, it := range widgets {
				if status != "" && it.Status != status {
					continue
				}
				if limit > 0 && n >= limit {
					return w.WritePagination(output.Pagination{HasMore: true})
				}
				if err := w.WriteItem(it); err != nil {
					return err
				}
				n++
			}
			return nil
		},
	}
	list.Flags().Int("limit", 0, "Maximum widgets to return")
	list.Flags().String("status", "", "Filter by status: active or archived")

	get := &cobra.Command{
		Use:         "get [id]",
		Short:       "Get a widget by id",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{agentmcp.AnnotationReadOnly: "true"},
		RunE: func(_ *cobra.Command, args []string) error {
			it, ok := find(args[0])
			if !ok {
				return output.New(fmt.Sprintf("widget %q not found", args[0]), output.FixableByAgent).
					WithHint("list ids with 'widget item list'")
			}
			return output.NewNDJSONWriter(os.Stdout).WriteItem(it)
		},
	}

	var yes bool
	del := &cobra.Command{
		Use:   "delete [id]",
		Short: "Delete a widget by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			it, ok := find(args[0])
			if !ok {
				return output.New(fmt.Sprintf("widget %q not found", args[0]), output.FixableByAgent)
			}
			if !yes {
				return output.New(fmt.Sprintf("refusing to delete widget %q without confirmation", it.ID), output.FixableByHuman).
					WithHint("rerun with --yes")
			}
			return output.NewNDJSONWriter(os.Stdout).WriteItem(map[string]any{"deleted": it.ID})
		},
	}
	del.Flags().BoolVar(&yes, "yes", false, "Confirm deletion")

	item.AddCommand(list, get, del)
	root.AddCommand(item)
	root.AddCommand(agentmcp.Command(root))

	if err := root.Execute(); err != nil {
		output.WriteError(os.Stderr, err)
		os.Exit(1)
	}
}
