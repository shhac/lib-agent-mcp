package agentmcp

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// groupHelp renders the "help" verb payload for an exposed group tool: the
// group's common (inherited) flags once, then each subcommand with its local
// flags, so an agent can discover the dispatch surface without a schema per sub.
func (s *Server) groupHelp(group *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n\n", strings.Join(commandPathParts(group), " "), group.Short)

	// Flags every subcommand inherits (e.g. domain persistent flags) are listed
	// once here rather than repeated on each subcommand line below.
	var common []string
	group.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if s.flagSkipped(f) {
			return
		}
		common = append(common, "  "+formatFlagLine(f, false))
	})
	if len(common) > 0 {
		b.WriteString("Common flags (apply to every subcommand):\n")
		b.WriteString(strings.Join(common, "\n"))
		b.WriteString("\n\n")
	}

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
				s.visitLocalFlags(sub, func(f *pflag.Flag, required bool) {
					fmt.Fprintf(&b, "%s    %s\n", indent, formatFlagLine(f, required))
				})
			}
			if hasRunnableSub(sub) {
				render(sub, depth+1)
			}
		}
	}
	render(group, 0)
	return b.String()
}

// formatFlagLine renders one flag as "--name <type>  usage", with a trailing
// " (required)" when required. Callers prepend their own indentation.
func formatFlagLine(f *pflag.Flag, required bool) string {
	req := ""
	if required {
		req = " (required)"
	}
	return fmt.Sprintf("--%s <%s>  %s%s", f.Name, f.Value.Type(), f.Usage, req)
}
