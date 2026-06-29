package agentmcp

import (
	"strings"

	"github.com/spf13/cobra"
)

// This file holds the pure cobra command-tree predicates and traversal shared
// across the bridge — schema.go (Tool assembly), jsonrpc.go (call dispatch), and
// grouphelp.go (group help) all read these, so the exclusion filter, the
// "confirms"/"destructive" signals, and the tree walk each have one definition.

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

// commandPathParts is cmd's command path with the root binary name dropped — the
// args a caller passes to reach cmd through a group tool.
func commandPathParts(cmd *cobra.Command) []string {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) > 0 {
		parts = parts[1:]
	}
	return parts
}

// subNames lists the names of cmd's visible subcommands.
func subNames(cmd *cobra.Command) []string {
	subs := visibleSubs(cmd)
	names := make([]string, 0, len(subs))
	for _, sub := range subs {
		names = append(names, sub.Name())
	}
	return names
}

// groupHasDestructive reports whether any command under group is destructive.
func groupHasDestructive(group *cobra.Command) bool {
	return anyCommand(group, commandDestructive)
}
