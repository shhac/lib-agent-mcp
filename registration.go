package agentmcp

import "fmt"

// The stdio-registration recipe (the mcpServers config + the `claude mcp add`
// line) is shown in two places — the run-by-hand setup hint and `mcp usage`.
// These helpers single-source it so the "mcp" subcommand token and argument
// shape can't drift between the two surfaces (or from the skill docs).

// claudeMcpAddLine is the `claude mcp add` invocation that registers this server
// with the Claude Code CLI.
func claudeMcpAddLine(name, exec string) string {
	return fmt.Sprintf("claude mcp add %s -- %s mcp", name, exec)
}

// mcpServersConfig renders the desktop-client mcpServers JSON entry. pretty gives
// the indented multi-line form (the setup hint embeds it after a 4-space indent);
// otherwise a compact single line (the usage card).
func mcpServersConfig(name, exec string, pretty bool) string {
	if pretty {
		return fmt.Sprintf(`{
      "mcpServers": {
        %q: {
          "command": %q,
          "args": ["mcp"]
        }
      }
    }`, name, exec)
	}
	return fmt.Sprintf(`{"mcpServers": {%q: {"command": %q, "args": ["mcp"]}}}`, name, exec)
}
