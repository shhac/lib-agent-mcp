# AGENTS.md — lib-agent-mcp

Guidance for an agent (or human) working in this repo. `CLAUDE.md` is a symlink
to this file.

## What this is

`lib-agent-mcp` (Go package `agentmcp`, module `github.com/shhac/lib-agent-mcp`)
turns a `spf13/cobra` command tree into an MCP server. It generates tools by
reflecting over the tree and runs tool calls by executing the same binary as a
subprocess, translating output via the `lib-agent-output` NDJSON contract.

Read [`design-docs/design.md`](design-docs/design.md) first — it has the full
rationale and the decisions below in context.

## Layout

| File | Responsibility |
|---|---|
| `server.go` | `Command(root, ...Option)`, options, annotation keys, `newServer` |
| `schema.go` | tree walk → `[]Tool`; flag → JSON-Schema type mapping |
| `jsonrpc.go` | stdio JSON-RPC loop: `initialize`, `tools/list`, `tools/call`, `ping` |
| `runner.go` | subprocess self-exec; flag/arg reconstruction; `--yes` injection |
| `translate.go` | subprocess result → MCP `tools/call` result |
| `examples/widget/` | demo CLI + `fixtures.json` used by the e2e test |
| `*_test.go` | schema, translate (unit) and `e2e_test.go` (real handshake) |

## Build, test, verify

```sh
go build ./...
go vet ./...
go test ./...          # includes the e2e test (builds widget, runs `widget mcp`)
go test -short ./...   # skips the e2e build
```

Always run `go vet` and `go test ./...` before considering a change done.

## Dependency on lib-agent-output

The NDJSON contract types live in the sibling module
`github.com/shhac/lib-agent-output`, pinned to a published tag in `go.mod`
(`require github.com/shhac/lib-agent-output v0.1.0`). Do **not** re-implement
the contract types here — import them.

For local cross-repo development (editing both at once without re-tagging), use
a gitignored `go.work` at the repo root:

```sh
go work init . ../lib-agent-output
```

`go.work`/`go.work.sum` are gitignored, so they never affect the published
dependency. To cut a new release, tag `lib-agent-output` first, then bump the
`require` here and re-tag.

## Conventions / how to infer things

- **Code style**: early returns over nested conditionals; self-documenting
  code; comments explain *why*, not *what*. Match the surrounding file.
- **Adding a capability**: most changes are one of three kinds — a new
  *schema* rule (`schema.go`), a new *protocol* method (`jsonrpc.go`), or a new
  *output* mapping (`translate.go`). Find the matching file; they're small and
  single-purpose.
- **Schema/translation are pure and unit-testable** without a subprocess. Add a
  unit test there; reserve `e2e_test.go` for changes that cross the
  process/protocol boundary.
- **The bridge is generic over cobra but opinionated about output.** It assumes
  the `lib-agent-output` contract (NDJSON on stdout, `{error,fixable_by,hint}`
  on stderr) but degrades gracefully (non-JSON stdout → text). Keep that
  property: never hard-fail on output that doesn't parse.
- **Safety**: a tool call can run a real, possibly destructive command. The
  `--yes`/`destructiveHint` handling is deliberate (see design doc); don't
  weaken it without thinking through the host-confirmation model.
- **Annotation keys** (`mcp.skip`, `mcp.readonly`, `mcp.destructive`,
  `mcp.hidden`) are the public contract for CLI authors — treat them as API.

## Naming convention (family-wide)

- `lib-agent-*` = shared libraries (this repo, `lib-agent-output`).
- `agent-*` = the CLIs that consume them.

## Design docs

`design-docs/` holds durable rationale and learnings. When you make a decision
worth remembering (a tradeoff, a rejected alternative, a protocol nuance),
record it there rather than only in a commit message.
