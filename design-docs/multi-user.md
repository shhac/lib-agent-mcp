# Multi-user serving: principals, bindings, and the trust model

Status: implemented (per-principal pairing + identity binding). This document
is the security design for serving one CLI's tools to **several humans** over
HTTP + OAuth, each acting with their own downstream credentials.

## What changes when a server has more than one user

The single-user shape (one operator, one shared pairing code, every caller
acting with the CLI's own keychain) treats *who-may-connect* as the only
question. With two humans, a second question becomes load-bearing: **which
credentials does this specific call act with?** A bug in that second axis is
no longer a stale cache or a broken command — it is @bob reading @alice's
DMs. Everything in this design exists to make that failure loud and
structural rather than silent.

## The pieces

1. **Named principals** (`oauth/principal.go`). The operator mints a pairing
   code *per person*: `mcp pair add alice --bind workspace=alice-acme`.
   Completing the OAuth approval with that code yields tokens whose
   `principal` claim is `alice` and which carry the stored binding. The
   legacy shared code still works and maps to the anonymous operator
   (empty principal, no binding) — single-user setups are unchanged.

2. **Claims, not lookups.** The principal name and binding ride in the signed
   access token (HS256, audience-bound). The Resource Server needs no
   per-call store read, and the values are tamper-proof: a client cannot
   claim a binding it was not paired with. The cost: a binding edit or
   principal removal does not rewrite tokens already in the wild — see
   revocation.

3. **Principal propagation.** `Protect` validates the token and attaches the
   `Verified{ClientID, Principal, Binding, …}` to the request context
   (`PrincipalFrom`). It flows through dispatch into the tool runner
   untouched.

4. **Identity binding** (`WithIdentityBinding`). The embedding CLI declares
   how a principal's binding translates into subprocess argv/env — e.g.
   agent-slack: `--workspace <alias>` plus `AGENT_SLACK_REQUIRE_IDENTITY=1`.
   The lib guarantees the translation is applied to every
   principal-authenticated call; the CLI owns the vocabulary.

5. **Fail closed on the CLI side.** The binding env should always include the
   CLI's fail-closed gate (`AGENT_<X>_REQUIRE_IDENTITY=1`): if the lib-side
   plumbing ever fails to supply a selector, the subprocess errors instead of
   falling back to the operator's default identity. The two halves —
   lib injects, CLI refuses defaults — mean a single bug cannot produce a
   cross-principal call; it takes both to fail.

## Trust boundaries, stated plainly

- **The host machine is the trust root.** All principals' downstream
  credentials live in the operator's keychain/config, selected by alias.
  Principals are isolated from *each other* through the binding mechanism,
  not from the operator: whoever runs the server can read every stored
  credential. This is a credential-custodian model, not tenant isolation.
- **A pairing code is a bearer enrollment secret.** Whoever completes the
  approval with alice's code acts under alice's binding. Codes are shown
  once at mint time, stored in the keyring, never listed back.
- **Binding data is routing, not authorization.** The binding selects among
  credentials the operator already stored (e.g. `agent-slack auth add
  --alias alice …`). Enrolling the credentials themselves stays an
  operator-side action.
- **Anonymous-operator calls are unbound.** stdio, plain HTTP (already
  documented as unauthenticated), and legacy-code pairings run with the
  CLI's own defaults, exactly as before this design.

## Revocation and its window

`mcp pair remove alice` deletes the pairing code and every refresh token
issued under `alice`. Outstanding **access** tokens are stateless JWTs and
remain valid until expiry (default TTL, typically minutes-to-an-hour): the
revocation window is the access-token TTL. `mcp pair reset` remains the
break-glass option — rotating the signing key invalidates every token
immediately, for all principals.

Re-running `mcp pair add alice` rotates alice's code and updates her stored
binding; tokens minted before the change carry the old binding until they
expire (same window as revocation).

## Out of scope (deliberately)

- **Per-principal ACLs on tools or scopes** — every principal gets the full
  tool surface, acting under their own credentials. Fine-grained scopes are
  future work (tracked in oauth.md's roadmap).
- **Enrollment of downstream credentials over HTTP** — the web-form
  credential enrollment (descriptor-driven `auth add`) is a separate design;
  today credentials enter via the CLI's own auth commands.
- **Multiple tools behind one origin / host binary** — the host-mode design
  builds on `mcp schema` (see design.md) and this principal model, but is
  not part of this change.
