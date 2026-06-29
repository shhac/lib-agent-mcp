# lib-agent-mcp: local OAuth (Phase 2 of the HTTP transport)

> Status: **implemented** (in the `oauth/` package, wired via `mcp --http --oauth
> local --public-url <url>`). This document captures the full design and every
> decision; the code follows it. Phase 1 (the `--http` transport) is in
> [http-transport.md](http-transport.md); this is the additive authorization
> layer on top of it. The package layout: `oauth/store.go` (SecretStore seam +
> keyring/mem impls), `token.go` (JWT issuer), `pairing.go` (pairing code),
> `pkce.go`/`authcode.go`/`clients.go`/`refresh.go` (AS state), and
> `server.go`/`metadata.go`/`register.go`/`authorize.go`/`grant.go` (the HTTP
> endpoints + the `Protect` RS gate).

## Why

A remote MCP client (a Claude Custom Connector) only adds a server that performs
the **MCP OAuth handshake** — an unauthenticated HTTP endpoint isn't accepted by
the UI. We want that handshake **without a third party**: the user is not
expected to stand up or use an external Authorization Server. So the lib-agent-mcp
server is **both** the OAuth 2.1 **Authorization Server (AS)** and **Resource
Server (RS)** for a single user — it mints its own tokens and validates them.

This is deliberately a *single-user, self-issued* gate: there are no accounts.
"Your identity" is "whoever can complete the approval step on your server." OAuth
here is the standards-compliant shape the UI requires, with you as the sole
authority.

## Decisions (the load-bearing ones)

1. **Flag: `--oauth local`.** The argument is the *mode*. `local` = the built-in
   self-contained AS+RS (this document). A future `--oauth <https-issuer-url>`
   (a URL value) selects **delegate** mode (RS-only, validating a third party's
   tokens) — reserved, not built now. `--oauth local` **requires** `--http`
   (you cannot OAuth a stdio pipe); `--oauth` without `--http` is a hard error.
2. **`--public-url <https-url>` is required with `--oauth local`.** Behind a
   tunnel the server can't infer its externally-reachable URL. Pass it as the
   **root** (no path): it is the **issuer** and the base from which the
   `.well-known` + `/oauth/*` documents are served. The **protected resource**
   (and token **audience**, RFC 8707) is *not* the root — it is the **`/mcp`
   endpoint** (`<public-url>/mcp`), because a client binds the audience to the
   exact URL it calls (Claude's connector POSTs MCP to the registered path and
   rejects a resource that doesn't match it). The library derives the resource by
   appending the MCP path; the operator still passes only the root.
3. **Endpoint layout.** The two discovery documents live at their **RFC-mandated
   well-known paths** (RFC 8615) and **cannot be moved**; everything else we own
   is namespaced under `/oauth/` to avoid polluting the server:
   ```
   /mcp                                       resource = audience (now gated)
   /.well-known/oauth-protected-resource      RFC 9728 PRM (static JSON, mandated path)
   /.well-known/oauth-protected-resource/mcp  same PRM, RFC 9728 path-suffixed for the /mcp resource
   /.well-known/oauth-authorization-server    RFC 8414 AS metadata (static JSON, mandated path)
   /oauth/register                            RFC 7591 Dynamic Client Registration
   /oauth/authorize                           authorization endpoint (human approval)
   /oauth/token                               token endpoint (code+PKCE → token)
   ```
   The well-known JSON just *advertises* the `/oauth/*` paths, so namespacing is
   free. RFC 9728 locates a path-bearing resource's metadata at the **suffixed**
   path; some clients construct that URL instead of following the 401 challenge,
   so the PRM is served at both. The `/oauth/*` endpoints are **not** aliased at
   the root — a spec-compliant client (the Claude connector included, verified by
   capture) finds them through the AS metadata's `*_endpoint` URLs.
4. **Pairing code** is our local user-auth factor at `/authorize` (see below).
   It is **not** an OAuth `client_secret` (per-client) and **not** an RFC 8628
   device `user_code` — it's our term for a reusable bootstrap secret.
5. **Access tokens are stateless JWTs** signed by a server key, audience-bound,
   short-TTL — so the RS validates per-token with no shared session and **N
   clients are valid concurrently** by construction.
6. **Secrets live in a keyring via a `SecretStore` seam**, in their **own
   namespace**, separate from the CLI's API credentials. The default
   implementation is backed by **`lib-agent-keyring`** (a shared sibling lib);
   the app passes only a namespace, never an implementation. lib-agent-mcp keeps
   its no-knowledge-of-CLI-creds boundary.

## Roles & the handshake

The server is AS + RS. Claude automates everything except the one-time human
approval (step 6):

1. User pastes `https://<public-url>/mcp` into the Connector (the **`/mcp` path is
   required** — it is the resource identifier; the bare host would post MCP to `/`).
2. Client POSTs `/mcp` with no token → **401** + `WWW-Authenticate: Bearer
   resource_metadata="https://<public-url>/.well-known/oauth-protected-resource/mcp"`.
3. Client GETs the PRM → learns the AS is this same server.
4. Client GETs `/.well-known/oauth-authorization-server` → endpoint URLs.
5. Client **self-registers** at `/oauth/register` (DCR) → gets a `client_id`
   (public client; PKCE, no client secret).
6. Client opens a browser to `/oauth/authorize?response_type=code&client_id=…&
   redirect_uri=…&code_challenge=…&code_challenge_method=S256&state=…&
   resource=https://<public-url>` → **the human approves by entering the pairing
   code** → server redirects to the client's `redirect_uri` with `?code=…&state=…`.
7. Client POSTs `/oauth/token` (`grant_type=authorization_code`, `code`,
   `redirect_uri`, `client_id`, `code_verifier`) → server verifies the code
   (unexpired, unused, PKCE `S256(verifier)==challenge`, redirect/client match)
   → mints an **audience-bound** access token (+ refresh token).
8. Client re-POSTs `/mcp` with `Authorization: Bearer …` → RS validates
   (signature, `aud == <public-url>/mcp`, not expired, scope) → tools flow.
9. On expiry the client refreshes (or re-auths); the human does **not** repeat
   step 6.

## The pairing code

- **Purpose.** The user-authentication factor at `/oauth/authorize`. There are
  no accounts; proving "it's you" = presenting the pairing code. It gates token
  *issuance*, not each tool call.
- **Format.** Crockford Base32 (drops I/L/O/U, case-insensitive — typo/paste
  resistant), **~128 bits** from `crypto/rand`, hyphen-grouped 5-char blocks,
  with an identifying prefix:
  ```
  mcp-K7Q29-F3MXR-8WZ4T-…   (prefix + Crockford base32, 5-char groups)
  ```
  Generous entropy because it guards tools acting with the user's real creds.
- **Lifecycle.** **Generated if absent, then persisted** (stable across
  restarts — you don't re-pair every launch). **Regenerable** via the operator
  commands:
  - `mcp pair rotate` — issue a fresh code if it leaks. Existing connections keep
    their tokens; only *new* pairings need the new code.
  - `mcp pair reset --yes` — the bigger hammer for a suspected token compromise:
    wipes the whole MCP keyring namespace (signing key → every issued access/
    refresh token is invalidated, registered clients, and the pairing code). A
    fresh signing key + code regenerate on next boot; every client must
    re-register and re-pair. Rotating the PIN alone does **not** revoke tokens an
    attacker already holds — that's what `reset` is for.
- **Storage.** In the keyring through `SecretStore`, under the MCP namespace
  (separate from CLI creds — the host app supplies its reverse-DNS service plus a
  `.mcp` suffix, e.g. `app.example.agent-foo.mcp`).
- **Reusable, not single-use.** Every harness (Claude, Codex, …) pairs with the
  **same** code; the single-use artifact is the OAuth *authorization code* at
  step 6/7. This is what makes multiple concurrent connections work.
- **Surfaced on launch.** Printed to **stdout** at boot in `--http --oauth local`
  mode (stdout is *not* the protocol channel in HTTP mode, so it's safe — unlike
  stdio mode, where banners go to stderr), alongside the public URL, the `/mcp`
  endpoint, and the well-known URLs. Document the caveat: **treat it like a
  password** — if stdout is redirected to a file the code lands there.

## Token model

- **Access token = JWT**, signed by the server **signing key** (HS256 or
  EdDSA — decide at build; EdDSA/asymmetric is cleaner if we ever expose a JWKS,
  HS256 is simplest for self-contained). Claims: `iss`=public-url (root),
  `aud`=`<public-url>/mcp` (the resource — RFC 8707 audience binding), `exp` short
  (e.g. 1h), `sub`/`cid`
  = client_id, `scope`. **Stateless validation** — the RS needs only the signing
  key, no token store.
- **Refresh tokens** are stored (opaque, in `SecretStore`) so they can be rotated
  and revoked; access tokens are not stored.
- **DCR client registrations** are stored (client_id → redirect_uris, name).
- **Authorization codes** are short-lived (≈60s), single-use, bound to
  `code_challenge` + `client_id` + `redirect_uri` + `resource`. Held in memory
  (they live seconds) or the store.
- **PKCE `S256` is required** for all clients (OAuth 2.1).

## Secrets & the `SecretStore` seam

```go
// In lib-agent-mcp — the seam, so the lib never depends on a specific backend:
type SecretStore interface {
    Get(key string) (string, bool, error)
    Set(key, value string) error
    Delete(key string) error
}
```

- **Default impl** is backed by **`lib-agent-keyring`** under an MCP-specific
  service namespace (e.g. `<reverse-domain-or-app>.mcp`), distinct from the CLI's
  API-credential service. The app supplies just the namespace (or lib-agent-mcp
  derives `<root-name>.mcp`); it never supplies an implementation. A `SecretStore`
  can still be injected for tests / future backends.
- **What's stored:** signing key, pairing code, refresh tokens, DCR
  registrations.
- **What's NOT touched:** the CLI's API credentials (Slack/Linear tokens) stay in
  their own keyring service; the OAuth layer never reads them. Two trust axes —
  *who-may-connect* vs *what-creds-the-tools-use* — rotate independently.

Dependency direction after the keyring extraction (no cycles):
```
lib-agent-output (base) ; lib-agent-keyring (base, ~zero-dep)
lib-agent-cli  → output, keyring (re-exports keychain for back-compat)
lib-agent-mcp  → output, keyring (default SecretStore)
```

## Naming: what's fixed vs ours

- **Fixed by RFC — do not rename:** the `.well-known/oauth-protected-resource`
  and `.well-known/oauth-authorization-server` paths; the metadata field names
  `authorization_endpoint`/`token_endpoint`/`registration_endpoint`; PKCE
  `code_challenge`/`code_challenge_method`/`S256`; `Bearer`/`WWW-Authenticate`.
- **Ours (named deliberately):** "pairing code" (not `client_secret`, not
  `device_code`); `--oauth local`; `--public-url`; the `/oauth/*` path prefix
  (allowed because endpoints are metadata-advertised, not fixed).

## Security posture

Single-user, self-issued. A valid token grants **full tool access acting with
the CLI's real credentials**, so: short access-token TTL; audience-bound;
issuance gated by the pairing code; the listener bound to loopback with TLS
terminated by the tunnel/edge (the spec requires HTTPS for auth endpoints —
`localhost` is the only plaintext exception). OAuth is the gate the UI requires;
it does not make exposing credentialed tools casual.

## Build milestones (for step 6 of the plan; structure pass + apply at each)

1. **SecretStore seam + keyring default + namespace wiring** (depends on
   `lib-agent-keyring` existing).
2. **Token layer** — signing-key management (generate/persist via SecretStore),
   JWT mint/verify, audience/exp/scope.
3. **RS gate on `/mcp`** — 401 challenge, the PRM well-known doc, bearer
   validation. (Composes over the Phase 1 handler.)
4. **AS endpoints** — AS metadata well-known doc, `/oauth/register` (DCR),
   `/oauth/authorize` (pairing-code approval + auth code + PKCE challenge),
   `/oauth/token` (code+PKCE → JWT + refresh).
5. **CLI wiring** — `--oauth local` + `--public-url` on the `mcp` command, the
   stdout boot info, and the requires-`--http` guard.

## Testing strategy

- Unit: pairing-code format/entropy + generate-if-absent + rotate; JWT
  mint/verify (good/expired/wrong-aud/bad-sig); PKCE verify (S256 match/mismatch);
  auth-code single-use + expiry + binding; DCR register/lookup; SecretStore fake.
- Handler: `/mcp` 401-without-token + serves-with-valid-token; PRM + AS metadata
  JSON shape; `/oauth/register` round-trip; `/oauth/authorize` rejects wrong
  pairing code, issues code on right one; `/oauth/token` happy + each failure.
- End-to-end: drive the full discovery→register→authorize→token→/mcp flow against
  the `examples/widget` binary over HTTP (scripted), asserting a tool call
  succeeds only with a freshly-minted token.

## Out of scope (now)

Delegate mode (`--oauth <issuer-url>`), a token **revocation** endpoint
(RFC 7009), fine-grained per-tool scopes, JWKS exposure (only needed if a
separate RS ever validates our tokens). Each is an additive follow-on.
