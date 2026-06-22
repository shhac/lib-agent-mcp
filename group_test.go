package agentmcp

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func optInRoot() *cobra.Command {
	root := &cobra.Command{Use: "demo", Version: "1.0.0"}

	item := &cobra.Command{Use: "item", Short: "Manage items"}
	get := &cobra.Command{Use: "get <id>", Short: "Get an item", RunE: noop}
	list := &cobra.Command{Use: "list", Short: "List items", RunE: noop}
	list.Flags().Int("limit", 0, "Max items")
	del := &cobra.Command{Use: "delete <id>", Short: "Delete an item", RunE: noop}
	del.Flags().Bool("yes", false, "Confirm")
	item.AddCommand(get, list, del)
	Expose(item) // opt in the whole group as one coarse tool

	// A non-exposed group (config) and a non-exposed leaf (usage) must not surface.
	cfg := &cobra.Command{Use: "config", Short: "Config"}
	cfg.AddCommand(&cobra.Command{Use: "get", Short: "show", RunE: noop})
	root.AddCommand(item, cfg, &cobra.Command{Use: "usage", Short: "Usage", RunE: noop})
	return root
}

func helpText(res map[string]any) string {
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	return text
}

func TestExpose_GroupTool(t *testing.T) {
	s := newServer(optInRoot())
	by := map[string]Tool{}
	for _, tl := range s.buildTools() {
		by[tl.Name] = tl
	}

	// Exactly one tool — the exposed group. Per-leaf and un-exposed commands gone.
	if len(by) != 1 {
		t.Fatalf("want 1 tool, got %d: %v", len(by), mapKeys(by))
	}
	it, ok := by["item"]
	if !ok || !it.group {
		t.Fatalf("expected a group tool named 'item'; have %v", mapKeys(by))
	}
	for _, gone := range []string{"item_get", "item_list", "item_delete", "config", "config_get", "usage"} {
		if _, leaked := by[gone]; leaked {
			t.Errorf("tool %q must not exist in opt-in mode", gone)
		}
	}
	// A group with a --yes subcommand carries the coarse destructive hint.
	if it.Annotations["destructiveHint"] != true {
		t.Error("group containing a --yes subcommand should hint destructive")
	}
	// The input is the args array.
	props := it.InputSchema["properties"].(map[string]any)
	if _, ok := props["args"]; !ok {
		t.Errorf("group tool should take args; got %v", props)
	}
}

func TestExpose_HelpVerb(t *testing.T) {
	s := newServer(optInRoot())
	var item Tool
	for _, tl := range s.buildTools() {
		if tl.Name == "item" {
			item = tl
		}
	}
	// Empty args, "help", and an unknown subcommand all return the usage.
	for _, args := range [][]string{nil, {"help"}, {"bogus"}} {
		help := helpText(s.callGroup(context.Background(), &item, args, nil))
		for _, want := range []string{"get", "list", "delete", "--limit"} {
			if !strings.Contains(help, want) {
				t.Errorf("args %v: help missing %q:\n%s", args, want, help)
			}
		}
	}
}

// TestExpose_DestructiveAnnotationWithoutYes — a subcommand marked Destructive
// but without a --yes flag still makes its group carry destructiveHint, so the
// host confirms; the injection path stays flag-based (tested via buildArgv) so
// the subprocess never receives an unknown --yes flag.
func TestExpose_DestructiveAnnotationWithoutYes(t *testing.T) {
	root := &cobra.Command{Use: "demo", Version: "1.0.0"}
	grp := &cobra.Command{Use: "thing", Short: "Things"}
	purge := &cobra.Command{Use: "purge <id>", Short: "Purge a thing", RunE: noop}
	Destructive(purge) // destructive, but defines no --yes flag
	grp.AddCommand(purge)
	Expose(grp)
	root.AddCommand(grp)

	s := newServer(root)
	var tool Tool
	for _, tl := range s.buildTools() {
		if tl.Name == "thing" {
			tool = tl
		}
	}
	if tool.Annotations["destructiveHint"] != true {
		t.Error("a Destructive-annotated subcommand should set the group destructiveHint")
	}
	// The subcommand has no --yes flag, so a host-confirmed call must NOT inject one.
	if purge.Flags().Lookup("yes") != nil {
		t.Fatal("test setup: purge should not define --yes")
	}
}

// TestLegacyFallback — with no Expose anywhere, the server keeps reflect-all
// (one tool per runnable leaf), so un-migrated CLIs are unaffected.
func TestLegacyFallback(t *testing.T) {
	by := toolMap(t) // testRoot() has no Expose
	if _, ok := by["item_get"]; !ok {
		t.Error("legacy mode should still emit per-leaf tools like item_get")
	}
}
