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
- **Phase 2 — authorization (planned, designed).** An additive `--oauth` layer.
  The chosen first mode is **`--oauth local`**: the server is its own OAuth 2.1
  Authorization Server *and* Resource Server (no third party), gating `/mcp`
  behind audience-bound JWTs it mints itself, with a local **pairing code** as
  the approval factor. `--oauth requires --http`; its absence leaves Phase 1
  untouched. A future `--oauth <issuer-url>` selects delegate mode (RS-only). The
  full plan — endpoints, pairing code, token model, secret storage, milestones —
  is in **[oauth.md](oauth.md)**.

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

## Tailscale auto-wiring

`--tailscale funnel|serve` brings up a Tailscale tunnel in front of the `--http`
listener and tears it down on exit, so one command yields a public (funnel) or
tailnet-private (serve) HTTPS URL with no separate `tailscale` step.

- **`--tailscale funnel`** exposes the server on the public internet (ports 443,
  8443, 10000 only) — what a cloud MCP connector needs. **`serve`** is tailnet-only.
- **`--tailscale-port 443|8443|10000`** picks the public HTTPS port (default 443).
- **`--public-url` becomes optional**: when unset it is derived from the node's
  MagicDNS name (`tailscale status --json` → `Self.DNSName`), carrying the port
  when it isn't 443. The OAuth resource stays `<public-url>/mcp`.
- **Lifecycle**: started with `tailscale <mode> --bg --yes --https=<port>
  <local-port>`, removed with `tailscale <mode> --https=<port> off`. The `mcp`
  command installs its own SIGINT/SIGTERM handler (the host runs it with a
  background context), so Ctrl-C drains the HTTP server and runs teardown on a
  fresh context — the cancelled serve context can't be used for the off command.
- The `tailscale` calls sit behind a `cmdRunner` seam, so validation, URL
  derivation, and start/teardown argv are unit-tested without the binary or
  network; only the thin `exec`/`LookPath` glue is uncovered.

Requires the `tailscale` CLI on PATH and Funnel enabled on the tailnet.

## Access log

`--access-log <path>` writes one NDJSON line per HTTP request (`"-"` = stderr) —
method, path, status, duration, byte count, and the headers that matter for
connector debugging (`Origin`, `MCP-Protocol-Version`, `User-Agent`,
`X-Forwarded-For`). `Authorization`/`Cookie` are never written. It promotes the
throwaway logging proxy used to debug the Claude connector into the tool, and is
a `statusRecorder` + middleware wrapped outside CORS so it sees the final status
of every request including preflights and 401s. Only applies with `--http`.

## Testing

- Handler: initialize/tools-list happy paths, notification → 202, parse error →
  400/-32700, GET → 405+Allow.
- Listener: `serveHTTP` on `:0` answers a real `ping` POST and returns nil after
  context cancel (graceful shutdown).
- End-to-end: the `examples/widget` binary under `mcp --http` answers
  initialize/tools-list/notification/GET over real HTTP.
