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
| `FileRef` atom, `FileRoot{Name,Path}`, `SafeResolve`, `FileRefFor` (reverse-map), `SniffMimeType` | **lib-agent-output** | The wire contract's shared home; both producers (CLIs) and the consumer (bridge) already import it. Pure, zero-dep, no secrets. |
| Ergonomic root construction (`xdg.Root(name, dir)` → `output.FileRoot`) | **lib-agent-cli** | cli owns the app's XDG dirs (`xdg`), so it's where an app *declares* the roots it has. |
| Live root registry, the `fs` tool, content-block types, the output-rewrite pass (`rewriteFileRefs`) | **lib-agent-mcp** | Per-server runtime + protocol surface. |
| Opt-in wiring (`WithFileRoots(xdg.Root("cache", appCacheDir()))`) | **the CLI (e.g. agent-slack)** | One line: declare which roots to expose. The path rewrite then needs no further CLI change. |

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
    Type     string            `json:"type"`
    Text     string            `json:"text,omitempty"`
    Data     string            `json:"data,omitempty"`     // base64 for image/audio
    MimeType string            `json:"mimeType,omitempty"` // for image/audio
    Resource *embeddedResource `json:"resource,omitempty"` // type:resource
}
```

Constructors: `imageBlock`, `audioBlock`, `resourceBlock` (embedded blob).
Existing `textBlock` is untouched. (No `resource_link` block: a `get` over the
inline limit returns a structured error rather than a URI pointer — over-cap
handling is out of scope, so there is nothing for a link block to carry.)

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
    agentmcp.WithFileRoots(xdg.Root("cache", appCacheDir())), // xdg from lib-agent-cli
))
```

## Phasing (as built)

**Phase 1 — the tool.** `output`: `FileRef`, `FileRoot`, `SafeResolve`,
`SniffMimeType`. `mcp`: native-tool seam, `fs` (find/ls/get), content blocks,
the three options, inline-cap error. `cli`: `xdg.Root(name, dir)` constructor.
`agent-slack`: the one-line binding. Fully usable: the agent calls
`fs find cache -e png`, then `fs get cache downloads/F0BD….png`.

**Phase 2 — automatic path hints.** So the model doesn't have to `find` then
`get` after another tool already named a file, the bridge rewrites file paths
in a tool's output into fetchable `FileRef` atoms — with **zero per-CLI work**:

- `output`: `FileRefFor(roots, abs)` reverse-maps an absolute path to a
  `FileRef` under the deepest containing root (pure string mapping).
- `mcp`: after translating a tool's NDJSON, `rewriteFileRefs` walks each record
  (recursing through nested objects/arrays) and replaces any string value that
  is an absolute path **under a configured root** with a `FileRef` atom. The
  text block is rebuilt with paths scrubbed only when a root path appears in
  stdout, so every other tool's output is preserved byte-for-byte.

This is an **exact** match (a string equal to or prefixed by a configured
root's real path), not heuristic path-shape scanning — so it only fires on
genuine in-root files, needs no marker convention, and an unexposed root's
paths are simply left untouched (no `FileRef`, nothing to fetch). The CLI keeps
printing its normal absolute path, which stays useful in plain-CLI mode (where
the agent has its own filesystem access); only under MCP does the bridge rewrite
it. The trade-off vs. a CLI-emitted typed marker: the bridge owns all the logic
and CLIs need no change, at the cost of the rewrite being a bridge-side
convention rather than a producer-declared one.

The same `FileRef` shape is produced by `find`/`ls` and by the rewrite, so a
file looks identical whether listed by the tool or surfaced from another
command's output.

## Testing (as built)

- `output`: `SafeResolve` traversal + symlink-escape + symlinked-root +
  not-found + unavailable-root; `FileRef` normalization; `FileRefFor`
  deepest-root/non-match; `SniffMimeType`.
- `mcp`: file-tool present/absent + name override + read-only hint; `find` by
  ext/glob/multi-ext/no-match/truncation; `ls` dir + nested + dir marker; `get`
  image/text/binary-resource/over-limit/directory/escape; unknown root/verb;
  rewrite of paths under a root incl. nested objects/arrays, meta preservation,
  multi-record, and no-roots no-op.
- `agent-slack`: verified end-to-end via the MCP stdio protocol — `fs` appears
  in `tools/list` and `fs get cache downloads/<f>` returns the file.

## Release coordination

Bottom-up, each tagged after its dep is published: `output` → `cli` (bump
output) → `mcp` (bump output) → `agent-slack` (bump output+cli+mcp). Local
cross-module development uses a `go.work` in the parent dir (uncommitted); the
`go.mod` requires are bumped to the freshly released versions at release time.
