package agentmcp

import (
	"testing"

	"github.com/spf13/cobra"
)

func noop(*cobra.Command, []string) error { return nil }

func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "widget", Version: "1.0.0"}
	root.PersistentFlags().String("format", "jsonl", "Output format")

	item := &cobra.Command{Use: "item", Short: "Manage widgets"}

	list := &cobra.Command{
		Use:         "list",
		Short:       "List widgets",
		Annotations: map[string]string{AnnotationReadOnly: "true"},
		RunE:        noop,
	}
	list.Flags().Int("limit", 0, "Maximum widgets to return")
	list.Flags().String("status", "", "Filter by status")

	get := &cobra.Command{
		Use:         "get [id]",
		Short:       "Get a widget",
		Annotations: map[string]string{AnnotationReadOnly: "true"},
		RunE:        noop,
	}

	del := &cobra.Command{Use: "delete [id]", Short: "Delete a widget", RunE: noop}
	del.Flags().Bool("yes", false, "Confirm deletion")

	item.AddCommand(list, get, del)
	root.AddCommand(item)
	return root
}

func optionProps(t *testing.T, tl Tool) map[string]any {
	t.Helper()
	props, ok := tl.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q has no properties: %v", tl.Name, tl.InputSchema)
	}
	options, ok := props["options"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q has no options object", tl.Name)
	}
	return options["properties"].(map[string]any)
}

func TestBuildToolsTreeWalk(t *testing.T) {
	s := newServer(testRoot())
	by := map[string]Tool{}
	for _, tl := range s.buildTools() {
		by[tl.Name] = tl
	}

	for _, want := range []string{"item_list", "item_get", "item_delete"} {
		if _, ok := by[want]; !ok {
			t.Fatalf("missing tool %q; have %v", want, mapKeys(by))
		}
	}
	// The `item` group command is not runnable and must not be a tool.
	if _, leaked := by["item"]; leaked {
		t.Error("non-runnable group command leaked as a tool")
	}
}

func TestToolAnnotations(t *testing.T) {
	s := newServer(testRoot())
	by := map[string]Tool{}
	for _, tl := range s.buildTools() {
		by[tl.Name] = tl
	}

	if by["item_get"].Annotations["readOnlyHint"] != true {
		t.Error("item_get should carry readOnlyHint")
	}
	if by["item_delete"].Annotations["destructiveHint"] != true {
		t.Error("item_delete (has --yes) should carry destructiveHint")
	}
	if !by["item_delete"].gated {
		t.Error("item_delete should be gated for --yes injection")
	}
}

func TestSchemaFiltersInfraAndYesFlags(t *testing.T) {
	s := newServer(testRoot())
	by := map[string]Tool{}
	for _, tl := range s.buildTools() {
		by[tl.Name] = tl
	}

	if _, leaked := optionProps(t, by["item_delete"])["yes"]; leaked {
		t.Error("--yes must not appear in the tool schema")
	}
	listOpts := optionProps(t, by["item_list"])
	if _, leaked := listOpts["format"]; leaked {
		t.Error("--format infra flag must be filtered out")
	}
	limit, ok := listOpts["limit"].(map[string]any)
	if !ok || limit["type"] != "integer" {
		t.Errorf("limit should be typed integer, got %v", listOpts["limit"])
	}
	status, ok := listOpts["status"].(map[string]any)
	if !ok || status["type"] != "string" {
		t.Errorf("status should be typed string, got %v", listOpts["status"])
	}
}

func mapKeys(m map[string]Tool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
