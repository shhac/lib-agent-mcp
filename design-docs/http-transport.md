# lib-agent-mcp: HTTP transport

Serve the same reflected tool surface over a URL, so a **remote** MCP client (a
Claude Custom Connector) can reach a CLI that only spoke stdio — without a
`supergateway` shim.

## Why

stdio is a subprocess pipe: invisible to a remote connector, which wants an
HTTPS URL speaking MCP's **Streamable HTTP** transport. The bridge already has a
transport-agnostic core (`dispatch(req) → response`); only the framing differs.
So HTTP is a second framing in front of the same dispatch, not a new server.

## Phasing

Transport and authorization are orthogonal, so they ship as independent,
additive layers:

- **Phase 1 — transport (shipped).** `mcp --http <addr>` serves Streamable
  HTTP. **No authorization.** Useful on loopback, on a trusted network, behind
  an MCP-aware authenticating edge, or fronted by a tunnel that does auth.
- **Phase 2 — authorization (planned).** An additive `--oauth <issuer>` layer
  makes the server an OAuth 2.1 **Resource Server**: it serves Protected
  Resource Metadata (RFC 9728), challenges with `401 + WWW-Authenticate`, and
  validates audience-bound bearer tokens (RFC 8707) issued by the named
  Authorization Server. `--oauth` requires `--http`; its absence leaves Phase 1
  untouched. (A self-contained minimal AS, if ever wanted, would be a separate
  mode — it needs more than a URL: a signing key, a store, an approval policy.)

The secret-storage boundary for Phase 2: MCP authorization secrets (signing
key, client registrations, issued tokens) live in their **own** store owned by
this lib — never mixed with the CLI's API credentials (Keychain), so the bridge
keeps its zero-knowledge-of-creds boundary and the two trust axes
(who-may-connect vs what-creds-to-use) rotate independently.

## Phase 1 design (as built)

One endpoint, `/mcp`:

- **POST** carries exactly one JSON-RPC message (2025-06-18 dropped batching).
  - a **request** (has `id`) → `200` with `Content-Type: application/json` and
    the JSON-RPC response (from the shared `dispatch`).
  - a **notification** (no `id`) → `202 Accepted`, empty body.
  - an unparseable body → `400` with a JSON-RPC `-32700` parse error.
- **GET** → `405` with `Allow: POST`. The server never initiates messages, so it
  needs no server-to-client SSE stream; per the spec a 405 is a valid answer.
  Returning JSON (not SSE) for responses is also spec-compliant.

`ServeHTTP(ctx, addr)` opens the listener; `serveHTTP(ctx, listener)` is the
testable core (tests listen on `:0`). Shutdown is graceful on `ctx` cancel.
Request bodies are size-capped; header reads are timeout-bounded.

**Statelessness.** `dispatch` holds no per-connection state (the tool list is
built once at construction), so Phase 1 issues no `Mcp-Session-Id` and keeps no
sessions — each POST is self-contained. Sessions can be added later if a client
requires them.

**Security posture.** Phase 1 is deliberately unauthenticated and does not
enforce `Origin`/`Host`. The boot banner says so in as many words. It is the
caller's job to bind to loopback or front it with auth until Phase 2 lands. This
is called out because the tools act with the CLI's real credentials.

## API

```go
// stdio (default) is unchanged:
root.AddCommand(agentmcp.Command(root))

// the mcp command gains a --http flag:
//   mycli mcp                 → stdio
//   mycli mcp --http :8000    → Streamable HTTP at http://localhost:8000/mcp
```

No new `Option` is needed for Phase 1 — `--http` is a flag on the `mcp` command.
Phase 2 will add `WithOAuth(...)` / `--oauth`.

## Testing

- Handler: initialize/tools-list happy paths, notification → 202, parse error →
  400/-32700, GET → 405+Allow.
- Listener: `serveHTTP` on `:0` answers a real `ping` POST and returns nil after
  context cancel (graceful shutdown).
- End-to-end: the `examples/widget` binary under `mcp --http` answers
  initialize/tools-list/notification/GET over real HTTP.
