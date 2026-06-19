# lib-agent-mcp

Expose any [`spf13/cobra`](https://github.com/spf13/cobra) CLI as a
[Model Context Protocol](https://modelcontextprotocol.io) (MCP) server — with
**no hand-maintained schemas**.

A cobra command tree already knows its subcommands, flag types, required-ness,
help text, and completions. That is almost a complete MCP tool schema sitting
unused. `lib-agent-mcp` reflects over the tree to generate the tools, and runs
a tool call by executing the same binary as a subprocess, interpreting its
output with the shared [`lib-agent-output`](https://github.com/shhac/lib-agent-output)
NDJSON contract.

## Quick start

Add one line to your root command:

```go
import agentmcp "github.com/shhac/lib-agent-mcp"

func main() {
    root := newRootCmd()
    root.AddCommand(agentmcp.Command(root)) // adds `mycli mcp`
    _ = root.Execute()
}
```

Now `mycli mcp` is an MCP stdio server. Point a client at it:

```jsonc
// claude_desktop_config.json
{
  "mcpServers": {
    "mycli": { "command": "mycli", "args": ["mcp"] }
  }
}
```

## How it works

- **Tools** — one per runnable leaf command (`item get` → `item_get`). Group
  commands, hidden commands, and `help`/`completion`/`mcp` are skipped.
- **Input schema** — `{ args, options }`: `args` is the positional string
  array; `options` is a typed object, one property per flag, types inferred
  from pflag. Required flags become `options.required`.
- **Annotations** — `mcp.readonly` → `readOnlyHint`; a `--yes` flag or
  `mcp.destructive` → `destructiveHint`.
- **Execution** — the tool call runs `mycli <path> [flags] [args] --format
  jsonl` as a subprocess (clean stdout/stderr capture and process isolation;
  in-process execution would corrupt the JSON-RPC stream on stdout).
- **Output translation** — stdout NDJSON records become `structuredContent`,
  `@`-prefixed lines become metadata, and a non-zero exit with a structured
  `{error, fixable_by, hint}` on stderr is surfaced as an MCP error. A CLI that
  does not follow the contract still works: raw stdout passes through as text.

See [`design-docs/design.md`](design-docs/design.md) for the full rationale,
including the Claude Cowork transport constraints.

## Annotations and options

| Command annotation | Effect |
|---|---|
| `mcp.skip: "true"` | Hide this command from the tool list |
| `mcp.readonly: "true"` | `readOnlyHint` |
| `mcp.destructive: "true"` | `destructiveHint` + inject `--yes` on call |

| Flag annotation | Effect |
|---|---|
| `mcp.hidden` (present) | Hide this flag from the schema |

`Command(root, opts...)` options: `WithName`, `WithVersion`,
`WithNameSeparator`, `WithHiddenFlags`, `WithExecutable`. Infra flags
(`format`, `debug`, `timeout`, `help`) and `--yes` are hidden by default.

## Example

[`examples/widget`](examples/widget) is a kitchen-sink cobra CLI built on
`lib-agent-output` that exercises every flag type, every annotation, and every
output helper — it doubles as the bridge's end-to-end test fixture. Try it:

```sh
go build -o /tmp/widget ./examples/widget
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"item_list","arguments":{"args":[],"options":{"status":"active"}}}}' \
  | /tmp/widget mcp
```

## Develop

```sh
go test ./...   # unit + end-to-end (builds widget, drives a real MCP handshake)
go vet ./...
```

This module depends on the published
[`github.com/shhac/lib-agent-output`](https://github.com/shhac/lib-agent-output)
`v0.1.0`. For local cross-repo development against a checkout at
`../lib-agent-output`, use a (gitignored) `go.work`:

```sh
go work init . ../lib-agent-output
```

See [`AGENTS.md`](AGENTS.md).

## License

[PolyForm Noncommercial License 1.0.0](LICENSE) — © 2026 Paul Somers.
