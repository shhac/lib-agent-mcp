package agentmcp

import (
	"testing"

	"github.com/spf13/cobra"
)

func noop(*cobra.Command, []string) error { return nil }

// testRoot mirrors the kitchen-sink example's structure so schema generation is
// exercised against every flag type and annotation without building a binary.
func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "widget", Version: "1.0.0"}
	root.PersistentFlags().String("format", "", "Output format")
	root.PersistentFlags().Bool("debug", false, "Debug logging")
	root.PersistentFlags().String("workspace", "", "Workspace to operate in") // a non-hidden domain-level persistent flag

	item := &cobra.Command{Use: "item", Short: "Manage widgets"}

	list := &cobra.Command{Use: "list", Short: "List widgets",
		Annotations: map[string]string{AnnotationReadOnly: "true"}, RunE: noop}
	list.Flags().Int("limit", 0, "Maximum widgets")
	list.Flags().String("status", "", "Filter by status")
	list.Flags().StringSlice("tag", nil, "Filter by tag")

	get := &cobra.Command{Use: "get [id]", Short: "Get a widget",
		Annotations: map[string]string{AnnotationReadOnly: "true"}, RunE: noop}

	search := &cobra.Command{Use: "search", Short: "Search widgets", RunE: noop}
	search.Flags().String("query", "", "Search query")
	_ = search.MarkFlagRequired("query")
	search.Flags().Int("limit", 10, "Maximum results")
	search.Flags().Float64("min-score", 0, "Minimum score")
	search.Flags().Bool("fuzzy", false, "Fuzzy matching")
	search.Flags().StringSlice("tag", nil, "Restrict to tags")
	search.Flags().Duration("since", 0, "Newer than")
	search.Flags().Count("verbose", "Verbosity")
	search.Flags().String("internal-token", "", "Internal")
	_ = search.Flags().SetAnnotation("internal-token", AnnotationFlagHidden, []string{"true"})
	search.Flags().Bool("legacy", false, "Deprecated")
	_ = search.Flags().MarkHidden("legacy")

	del := &cobra.Command{Use: "delete [id]", Short: "Delete a widget", RunE: noop}
	del.Flags().Bool("yes", false, "Confirm deletion")

	item.AddCommand(list, get, search, del)

	config := &cobra.Command{Use: "config", Short: "Configuration"}
	cfgSet := &cobra.Command{Use: "set [k] [v]", Short: "Set a value",
		Annotations: map[string]string{AnnotationDestructive: "true"}, RunE: noop}
	config.AddCommand(cfgSet)

	admin := &cobra.Command{Use: "admin", Short: "Admin",
		Annotations: map[string]string{AnnotationSkip: "true"}}
	admin.AddCommand(&cobra.Command{Use: "secret", Short: "Secret", RunE: noop})

	root.AddCommand(item, config, admin)
	return root
}

func toolMap(t *testing.T) map[string]Tool {
	t.Helper()
	by := map[string]Tool{}
	for _, tl := range newServer(testRoot()).buildTools() {
		by[tl.Name] = tl
	}
	return by
}

func optionProps(t *testing.T, tl Tool) map[string]any {
	t.Helper()
	props := tl.InputSchema["properties"].(map[string]any)
	options := props["options"].(map[string]any)
	return options["properties"].(map[string]any)
}

func TestInheritedPersistentFlagsAreSurfaced(t *testing.T) {
	by := toolMap(t)
	opts := optionProps(t, by["item_get"])
	// A non-hidden root persistent flag is a usable tool input on every command.
	if _, ok := opts["workspace"]; !ok {
		keys := make([]string, 0, len(opts))
		for k := range opts {
			keys = append(keys, k)
		}
		t.Errorf("inherited persistent --workspace should appear in item_get options; have %v", keys)
	}
	// The infra globals stay hidden even though they are inherited.
	for _, hidden := range []string{"format", "debug"} {
		if _, leaked := opts[hidden]; leaked {
			t.Errorf("hidden inherited flag %q must not appear in the schema", hidden)
		}
	}
}

func TestBuildToolsTreeWalk(t *testing.T) {
	by := toolMap(t)
	for _, want := range []string{"item_list", "item_get", "item_search", "item_delete", "config_set"} {
		if _, ok := by[want]; !ok {
			t.Errorf("missing tool %q; have %v", want, mapKeys(by))
		}
	}
	// Non-runnable groups are not tools; mcp.skip hides a whole subtree.
	for _, gone := range []string{"item", "config", "admin", "admin_secret"} {
		if _, leaked := by[gone]; leaked {
			t.Errorf("tool %q should not exist", gone)
		}
	}
}

func TestToolAnnotations(t *testing.T) {
	by := toolMap(t)

	if by["item_get"].Annotations["readOnlyHint"] != true {
		t.Error("item_get should carry readOnlyHint")
	}
	// --yes flag → destructiveHint AND --yes injected on a confirmed call.
	if by["item_delete"].Annotations["destructiveHint"] != true || !commandConfirms(by["item_delete"].cmd) {
		t.Error("item_delete should be destructiveHint + confirm-injectable")
	}
	// mcp.destructive annotation, no --yes flag → destructiveHint but NO inject.
	if by["config_set"].Annotations["destructiveHint"] != true {
		t.Error("config_set should carry destructiveHint")
	}
	if commandConfirms(by["config_set"].cmd) {
		t.Error("config_set has no --yes flag; must NOT inject --yes")
	}
}

func TestSchemaFlagTypesAndFiltering(t *testing.T) {
	by := toolMap(t)

	if _, leaked := optionProps(t, by["item_delete"])["yes"]; leaked {
		t.Error("--yes must not appear in the schema")
	}
	listOpts := optionProps(t, by["item_list"])
	for _, infra := range []string{"format", "debug"} {
		if _, leaked := listOpts[infra]; leaked {
			t.Errorf("infra flag %q must be filtered", infra)
		}
	}

	s := optionProps(t, by["item_search"])
	if _, leaked := s["internal-token"]; leaked {
		t.Error("mcp.hidden flag must be filtered")
	}
	if _, leaked := s["legacy"]; leaked {
		t.Error("cobra-hidden flag must be filtered")
	}
	wantType := map[string]string{
		"limit":     "integer",
		"min-score": "number",
		"fuzzy":     "boolean",
		"tag":       "array",
		"since":     "string",  // duration renders as string
		"verbose":   "integer", // count renders as integer
		"query":     "string",
	}
	for flag, typ := range wantType {
		p, ok := s[flag].(map[string]any)
		if !ok {
			t.Errorf("missing flag %q in schema", flag)
			continue
		}
		if p["type"] != typ {
			t.Errorf("flag %q type = %v, want %v", flag, p["type"], typ)
		}
	}
	if items, ok := s["tag"].(map[string]any)["items"].(map[string]any); !ok || items["type"] != "string" {
		t.Errorf("tag should be array of string, got %v", s["tag"])
	}
}

func TestRequiredFlag(t *testing.T) {
	by := toolMap(t)
	props := by["item_search"].InputSchema["properties"].(map[string]any)
	options := props["options"].(map[string]any)
	req, ok := options["required"].([]string)
	if !ok {
		t.Fatalf("item_search options.required missing or wrong type: %v", options["required"])
	}
	found := false
	for _, r := range req {
		if r == "query" {
			found = true
		}
	}
	if !found {
		t.Errorf("query should be required, got %v", req)
	}
}

func mapKeys(m map[string]Tool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
