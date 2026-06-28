# lib-agent-mcp: scoped file access (`fs` tool)

Give an MCP client a way to **read files an agent-* CLI produced on the local
machine** — Slack attachments, exported reports, downloaded artifacts — without
exposing host filesystem paths and without the client needing its own
filesystem access.

## Why

MCP rides on JSON-RPC, so a tool result is JSON. The bridge today only emits
**text** content blocks (`translate.go`), and the CLIs only ever hand back a
host path string, e.g.

```json
{ "id": "F0BD…", "path": "/Users/paul/.cache/agent-slack/downloads/F0BD….png" }
```

That works for the **write** direction (the CLI reads bytes off disk itself when
you pass `--attach <path>`), but it breaks the **read** direction over pure MCP:

- The path is meaningless to a client that has no filesystem capability.
- Even when the client *does* have file access, leaking the absolute host path
  is poor hygiene (privacy, portability, and it tells the model where on disk it
  is, which it should not need to know).

The MCP spec already defines the content blocks we need —
[`image`](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#image-content)
(base64), `audio`, and embedded
[`resource`](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#embedded-resources)
(base64 `blob`). Binary is **always base64** over the wire; there is no raw-byte
frame and no result-content pagination. So the job is: a scoped, read-only file
tool that lists what's there and returns the bytes as the right content block.

## Scope decisions (settled with the maintainer)

- **Read-only.** `find` / `ls` / `get` only. No write, move, or delete.
- **Named roots.** A server exposes one or more named roots (e.g. `cache`);
  every verb names the root it operates in. Paths are always **relative to the
  root** — the host parent is never shown or accepted.
- **Opt-in, per CLI.** The tool exists only if the CLI registers ≥1 root via
  `WithFileRoots`. CLIs that don't want to offer file access get nothing.
- **MCP-only.** `fs` is a *native* bridge tool, not a cobra command — it never
  appears in normal CLI usage.
- **Overridable name.** Defaults to `fs`; a CLI may rename it (`WithFileToolName`).
- **No downscaling / no binary pagination (out of scope for now).** A `get`
  above the inline limit returns a structured error. Re-encoding large images
  and byte-range reads can come later if a real need shows up.
- **One canonical file atom.** "A file the agent can fetch" has a single shape
  (`FileRef`), used identically whether it appears in a `find`/`ls` listing or
  embedded in some other tool's output (a Slack message with 5 attachments).
  The surrounding data differs; the file bit does not.

## Where each piece lives (and why)

Module DAG today (no cycles): `lib-agent-output` is zero-dep; `lib-agent-cli →
output`; `lib-agent-mcp → output`. **mcp does not depend on cli**, and we keep
it that way — `cli` is heavyweight (creds, keychain, dialog, graphics) and the
bridge stays lean.

| Piece | Home | Rationale |
|---|---|---|
| `FileRef` atom, `FileRoot{Name,Path}`, `SafeResolve`, marker encode/decode, mimetype sniff | **lib-agent-output** | The wire contract's shared home; both producers (CLIs) and the consumer (bridge) already import it. Pure, zero-dep, no secrets. |
| Ergonomic root construction (`app.CacheRoot()` → `output.FileRoot`) | **lib-agent-cli** | cli owns the app's XDG dirs (`xdg.App`), so it's where an app *declares* the roots it has. |
| Live root registry, the `fs` tool, content-block types, the output-rewrite pass | **lib-agent-mcp** | Per-server runtime + protocol surface. |
| Opt-in wiring (`WithFileRoots(app.CacheRoot())`) + migrating path emission to the helper | **the CLI (e.g. agent-slack)** | One-time, tiny: declare roots, emit files via the helper. |

"cli owns the named roots" is honored: the app obtains its roots through cli
(which knows its dirs); the underlying *type* sits in output so the bridge can
consume it without taking a cli dependency.

## The canonical atom: `output.FileRef`

```go
// FileRef is one fetchable local file, expressed relative to a named root.
// It is the single shape for "a file the agent can read", whether listed by the
// fs tool or embedded in another tool's record.
type FileRef struct {
    Type     string `json:"@type"`              // discriminator, always "file"
    Root     string `json:"root"`               // named root, e.g. "cache"
    Path     string `json:"path"`               // forward-slash path relative to the root
    Name     string `json:"name,omitempty"`     // basename
    MimeType string `json:"mimetype,omitempty"`
    Size     int64  `json:"size,omitempty"`
}
```

- `@type: "file"` is the discriminator that lets the bridge recognise a FileRef
  embedded anywhere inside an arbitrary record (phase 2), reusing the family's
  existing `@`-key convention without colliding with the single-key `@meta`
  lines (a FileRef is a multi-field object, not a one-key meta line).
- Directory entries in `ls` reuse the shape with a `dir` mimetype sentinel (or a
  sibling `kind` field) — TBD in implementation, kept minimal.

### `SafeResolve` — the one containment chokepoint

Every verb resolves user input through exactly one function; this is the
security boundary, not a detail.

```go
// SafeResolve joins rel under root and guarantees the result stays inside it.
func SafeResolve(root FileRoot, rel string) (abs string, err error)
```

Algorithm: reject absolute `rel`; `filepath.Clean`; reject any `..` segment;
join to `root.Path`; `EvalSymlinks` on the result; confirm the resolved path is
still prefixed by the resolved root (`resolved == r || strings.HasPrefix(resolved, r+sep)`).
On any violation, return a structured error (`fixable_by: agent`, hint naming
the root). Symlinks that escape the root are rejected by the post-resolve prefix
check. This has dedicated traversal/symlink-escape tests.

## The `fs` tool

A native, group-style tool: `args[0]` is the verb, the root is the next
positional, then verb-specific arguments. Present only when roots are
registered; named per `WithFileToolName` (default `fs`).

### `find` — discover files

```
args: ["find", "<root>", "-e", "png", "-e", "jpg", "<glob?>"]
```

- `-e <ext>` (repeatable) filter by extension; bare positional is a glob; later:
  `-t f|d`, `--max-depth N`. Implemented natively with `filepath.WalkDir` over a
  small, owned filter — **we do not shell out to the real `fd`** (portability,
  injection surface, and it would honor `.gitignore`/follow symlinks out of the
  sandbox).
- Returns `FileRef` records (relative paths) as `structuredContent`, the same
  atom `ls` and embedded hints use.

### `ls` — list one directory level

```
args: ["ls", "<root>", "<relpath?>"]
```

Shallow listing (files + dirs) as `FileRef` records.

### `get` — return the bytes

```
args: ["get", "<root>", "<relpath>"]
```

Sniffs content type, then returns the spec-idiomatic content block:

| Detected type | Content block |
|---|---|
| `image/*` | `{ "type": "image", "data": "<base64>", "mimeType": … }` |
| `audio/*` | `{ "type": "audio", "data": "<base64>", "mimeType": … }` |
| text-ish | `{ "type": "text", "text": … }` |
| other binary | `{ "type": "resource", "resource": { "uri", "mimeType", "blob": "<base64>" } }` |

The `uri` is a non-host-revealing scheme over `{root, path}` (e.g.
`agent-file://cache/downloads/F0BD….png`), never the absolute path. Result also
carries the `FileRef` as metadata so the model knows what it received.

**Inline limit.** `get` enforces a byte cap (`WithFileInlineLimit`, default a
few MiB — *much* smaller than the 100 MB upload ceiling, because base64 inlining
costs context tokens). Over the cap → structured error
(`fixable_by: human`, hint). No downscaling, no chunking (out of scope).

### `help` / empty / unknown verb

Usage text listing the registered roots and the three verbs, mirroring the
existing group-help affordance.

## Content blocks (`translate.go`)

Extend the text-only `contentBlock` to carry the spec's other shapes:

```go
type contentBlock struct {
    Type     string           `json:"type"`
    Text     string           `json:"text,omitempty"`
    Data     string           `json:"data,omitempty"`     // base64 for image/audio
    MimeType string           `json:"mimeType,omitempty"`
    URI      string           `json:"uri,omitempty"`      // resource_link
    Name     string           `json:"name,omitempty"`     // resource_link
    Resource *embeddedResource `json:"resource,omitempty"` // type:resource
}
```

Constructors: `imageBlock`, `audioBlock`, `resourceBlock` (embedded blob),
`resourceLinkBlock` (URI pointer). Existing `textBlock` and the whole
cobra-exec translate path are untouched — these are only produced by the native
`fs` handler.

## Native tools in the bridge

Today every `Tool` is cobra-derived and run by re-exec (`runner.go`). `fs` is
the first **native** tool — handled in-process, no subprocess. Minimal change:

- `Tool` gains an optional `handler func(ctx, args, opts) toolResult` (nil for
  cobra tools).
- `buildTools` appends registered native tools after the reflected ones.
- `callTool` short-circuits: `if tool.handler != nil { return tool.handler(...) }`
  before the cobra group/leaf dispatch.

This keeps the reflect-and-exec core intact and gives a clean seam for any
future native tool.

## New options (`server.go`)

```go
func WithFileRoots(roots ...output.FileRoot) Option   // opt-in; tool absent if none
func WithFileToolName(name string) Option             // default "fs"
func WithFileInlineLimit(bytes int64) Option          // default ~5 MiB
```

agent-slack wiring (the entire CLI-side change for phase 1):

```go
root.AddCommand(agentmcp.Command(root,
    agentmcp.WithHiddenFlags("color", "expose", "images", "hyperlinks"),
    agentmcp.WithFileRoots(app.CacheRoot()), // app from lib-agent-cli
))
```

## Phasing

**Phase 1 — the tool.** `output`: `FileRef`, `FileRoot`, `SafeResolve`,
mimetype sniff. `mcp`: native-tool seam, `fs` (find/ls/get), content blocks,
the three options, inline-cap error. `cli`: `App` root constructor. `agent-slack`:
the one-line binding + docs/skill. Fully usable: the agent calls
`fs find cache -e png`, then `fs get cache downloads/F0BD….png`.

**Phase 2 — automatic path hints.** So the model doesn't have to `find` then
`get` after another tool already named a file:

- `output`: an emit helper that tags a value (or list of values — a message may
  carry many attachments) as `FileRef` atoms instead of raw path strings. The
  helper owns the marker format; the CLI never hand-rolls it. The CLI's only
  change is to emit files through the helper.
- `mcp`: a post-translate pass that scans tool-output records for `@type:"file"`
  atoms, strips the host parent, and rewrites to an `fs`-fetchable `{root, path}`
  reference — **only** for roots actually exposed on this server (an unexposed
  root's file is left as-is / stripped, never pointed at a tool that isn't there).

We **do not** scan free text for path-shaped strings — that's heuristic, lossy,
and turns the bridge from a translator into a content editor. The typed atom is
the contract; recognition is exact.

Same atom flows through both phases, so "this is a Slack message with a file"
and "this is a find result" present the identical file object to the model.

## Testing

- `output`: `SafeResolve` traversal + symlink-escape rejection; FileRef
  round-trip; mimetype sniff.
- `mcp`: native-tool dispatch; each `get` content-block type; inline-cap error;
  `find`/`ls` listing shape; root-not-found and bad-path errors; (phase 2)
  rewrite of embedded atoms for exposed vs. unexposed roots.
- `agent-slack`: binding present only under `mcp`; contract test that a
  downloaded file is `get`-able by its listed relative path.

## Release coordination

Bottom-up, each tagged after its dep is published: `output` → `cli` (bump
output) → `mcp` (bump output) → `agent-slack` (bump output+cli+mcp, wire it).
Phase 2 repeats the chain for the helper + rewrite.
