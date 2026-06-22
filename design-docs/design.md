# lib-agent-mcp: design

Expose any `spf13/cobra` CLI as an MCP server, deriving tools and schemas from
the command tree and interpreting output via the shared NDJSON contract — so
agent-first CLIs gain an MCP interface with **no hand-maintained schemas**.

## Why

We have a family of cobra CLIs (`agent-sql`, `agent-slack`, `agent-vercel`,
`lin`, …) already built for agent consumption: structured NDJSON output, typed
flags, help text, completions. Cobra already knows the command tree, flag
types, required-ness, and descriptions. That is 90% of an MCP tool schema,
sitting unused. Rather than write and maintain a bespoke MCP server per CLI
(as `ldt-data` does for Snowflake), we reflect the tree once and translate the
existing output contract.

## Target environment: Claude Cowork

Cowork is a desktop app running a sandboxed VM; the user's CLIs live on that
machine. Its in-app **Custom Connectors are remote-only (HTTP/SSE)** — a local
stdio server is invisible to them. Local stdio servers reach Cowork through the
**Claude Desktop bridge** (registered in `claude_desktop_config.json`, proxied
into the VM). Implication for us: **support both transports.** stdio is the
default (Desktop bridge, Claude Code); an HTTP mode (future) serves the in-app
connector path directly, with no `supergateway` shim.

## Architecture

Two repos, one dependency edge:

```
   agent-* CLIs ─────►  lib-agent-output  ◄───── lib-agent-mcp
   (produce NDJSON)     (zero-dep contract)       (consume + serve MCP)
```

- **`lib-agent-output`** (package `output`) — the canonical, zero-dependency
  home for the wire contract: `NDJSONWriter`, `Pagination`, `Error` +
  `FixableBy`, `WriteError`, `WriteNotice`. Both producers (CLIs) and consumers
  (this bridge) import it, so the format is defined once. Replaces the four
  copy-pasted `internal/output/` packages.
- **`lib-agent-mcp`** (package `agentmcp`) — the bridge. Depends on
  `lib-agent-output` + cobra. Local dev uses a `replace` directive until
  published.

Naming convention: `lib-agent-*` for shared libraries, `agent-*` for the CLIs.

### Key decision: schema in-process, execution out-of-process

- **Schema** is derived in-process by walking the live `*cobra.Command` tree —
  free, accurately typed.
- **Execution** runs the command as a **subprocess of the same binary**
  (`os.Executable()` + reconstructed argv). Running in-process via
  `Execute()` would write command output to `os.Stdout`, which *is* the
  JSON-RPC channel. Subprocess gives clean stdout/stderr capture, process
  isolation, and matches the model CLIs are already built for. `WithExecutable`
  overrides the target binary (tests).

## Integration surface

```go
root.AddCommand(agentmcp.Command(root)) // adds `mycli mcp`
```

`mycli mcp` serves the tree over stdio. Options: `WithName`, `WithVersion`,
`WithNameSeparator`, `WithHiddenFlags`, `WithExecutable`.

On boot it writes a one-line banner (name, version, transport, tool count,
protocol) to **stderr**, so an operator watching the process sees it came up.
Deliberately stderr, never stdout: stdout *is* the JSON-RPC stream and any
non-protocol byte there would corrupt the client's parser. There is no port to
report — the transport is stdin/stdout pipes; a future network transport would
name its address in the banner instead.

When stdin is a **TTY** (a human ran `mycli mcp` by hand rather than a host
spawning it over a pipe), the banner is followed by a registration hint: a
ready-to-paste `mcpServers` config using the binary's own absolute path plus the
`claude mcp add` one-liner. Running the server directly otherwise just hangs
waiting for JSON-RPC, so the self-describing output is what a confused human — or
an LLM they paste it into — needs to actually wire it up. Gated on the TTY check
so host-spawned runs (piped stdin) stay quiet.

## Tool generation (cobra tree → MCP tools)

Two modes, chosen automatically by whether anything in the tree is `Expose`d:

### Opt-in surface (preferred): `Expose` / group tools

Reflecting *every* leaf produces too many tools — a real CLI yields 35–145,
well past the ~30-tool point where model tool-selection starts to degrade and
the ~40-tool cap some hosts (Cursor) enforce. So the surface is **opt-in**:

- `Expose(cmd)` marks a command as a **tool boundary**. Only exposed boundaries
  become tools; their subtree is reached *through* them, not as more tools. You
  place `Expose` at exactly the granularity you want and never have to reason
  about "what should the tool granularity be" — the tree placement *is* the
  answer. Credential/config/usage commands simply never get exposed.
- An exposed **group** (has runnable subcommands) becomes **one coarse tool**
  that dispatches its subcommands via `args[0]`, with a built-in **`help` verb**
  (`args:["help"]`, also the fallback for empty/unknown subcommands) that lists
  the subcommands and their flags — an MCP-native equivalent of `usage`. So
  `issue` + `args:["get","ENG-123"]` runs `lin issue get ENG-123`. The group
  help lists each subcommand's own flags inline and the flags every subcommand
  inherits once, under "Common flags".
- An exposed **leaf** (no runnable subcommands) becomes its own tool.
- The runner needs no special case: a group tool's path is the group and
  `args[0]` is the subcommand, so the reconstructed argv is already correct.
- `--yes` is resolved per-subcommand at call time (via cobra `Find`), and the
  group's `destructiveHint` is set if *any* reachable subcommand is destructive.

### Legacy fallback: one tool per leaf

When **nothing** is exposed, the bridge falls back to reflecting one tool per
runnable leaf, so un-migrated CLIs keep working unchanged. Non-runnable groups
are not tools. Skipped in both modes: hidden commands,
`help`/`completion`/`__complete`/`mcp`, and anything annotated `mcp.skip`.

### Common to both

- **Name** = command path joined by `_` (e.g. `item get` → `item_get`). Dots
  are avoided — many MCP clients reject them in tool names. (A client already
  namespaces by server, so the per-tool name is unprefixed, e.g. `issue`.)
- **Description** = `Short` (or `Long` when present); a group tool also lists
  its subcommands.
- **Input schema** mirrors the CLI shape: `{ args, options }`. `args` is a
  string array (positional); `options` is a typed object, one property per flag
  with type from pflag (`bool`→boolean, ints→integer, floats→number,
  `*Slice`/`*Array`→array, else string). Required flags
  (`MarkFlagRequired`) become `options.required`. (A group tool takes just
  `args` — the subcommand and its arguments/flags.)
- **Annotations**: `mcp.readonly` → `readOnlyHint`; a `--yes` flag or
  `mcp.destructive` → `destructiveHint`. Helpers: `Expose`, `Skip`,
  `Destructive`, `ReadOnly`. All annotations are MCP-only — cobra ignores them
  for CLI help and execution, so they never leak into CLI-land.

### Filtered flags

Infra/plumbing flags are hidden from schemas: `format`, `debug`, `timeout`,
`help` by default (extend via `WithHiddenFlags`), plus any flag marked
`mcp.hidden`. The bridge owns `--format` (always forces `jsonl`).

### Destructive hint vs. `--yes` injection

Two related but distinct signals, deliberately decoupled:

- **`destructiveHint: true`** is set when a command has a `--yes` flag *or* the
  `mcp.destructive` annotation. It tells the MCP host to confirm with the user
  before calling the tool.
- **`--yes` injection** happens *only* when the command actually defines a
  `--yes` flag. By the time a `tools/call` arrives, host confirmation has
  already happened, so the bridge satisfies the CLI's own gate by injecting
  `--yes` (kept hidden from the schema so the model can't self-confirm).

A command annotated `mcp.destructive` with **no** `--yes` flag is still hinted
as destructive but nothing is injected — injecting an undefined flag would make
cobra error `unknown flag`. (A future `WithConfirmField` could instead expose an
explicit `confirm: true` parameter.) The `examples/widget` `config set` (hint,
no inject) and `config reset` (hint + inject) cover both paths in the e2e test.

## Output translation (NDJSON contract → MCP result)

The contract every family CLI already emits:

- **stdout**: bare JSON records, one per line; metadata on `@`-prefixed lines
  (`@pagination`, `@unresolved`, …).
- **stderr**: structured JSON only — `{error, fixable_by, hint?}` on failure,
  `{notice, hint?}` for non-fatal diagnostics.
- **`fixable_by`**: `agent` (fix input + retry) | `human` (auth/confirm) |
  `retry` (transient).
- **exit code**: non-zero ⇒ failure.

The bridge forces `--format jsonl` and maps:

| Source | → MCP `tools/call` result |
|---|---|
| stdout bare record | `structuredContent.records[]` |
| stdout single `@key` line | `structuredContent.meta[key]` |
| all stdout | one `text` content block (fallback for clients ignoring structuredContent) |
| exit 0 | `isError: false` |
| exit ≠ 0 + stderr `{error,…}` | `isError: true`; `structuredContent.error`; error+hint as text |
| non-JSON stdout | passes through as text only (graceful degradation) |

`fixable_by` flows straight to the agent: `agent` is a self-correctable tool
error, `human` needs user action, `retry` is transient.

This makes the bridge **generic over cobra structure** but **opinionated about
output**: a CLI not following the contract still works (raw text), while the
family CLIs get rich `structuredContent` + `fixable_by`.

## Verification

- `lib-agent-output`: unit tests for error shape, NDJSON framing, pagination,
  notices, HTML non-escaping.
- `lib-agent-mcp`: schema tests (tree walk, annotations, infra/`--yes`
  filtering, flag typing), translate tests (records/meta, error/fixable_by,
  non-JSON degradation), and an **end-to-end test** that builds the `widget`
  example, runs `widget mcp`, and drives a real initialize → tools/list →
  tools/call handshake over stdio.
- `examples/widget`: a minimal cobra CLI using `lib-agent-output`, with
  `fixtures.json`, exercising read-only, typed-flag, error, and gated-delete
  paths.

## Open / future

- **HTTP / streamable-HTTP transport** for the Cowork Custom Connector path.
- **Completions → enums**: feed static `ValidArgs` and (opt-in, timeout-guarded)
  `RegisterFlagCompletionFunc` results into JSON-Schema `enum`s.
- **Named positionals** from the `Use` line instead of a flat `args` array.
- **`WithConfirmField`** alternative to `--yes` auto-injection.
- **Migration**: point the family CLIs at `lib-agent-output`, deleting their
  copied `internal/output/`.
