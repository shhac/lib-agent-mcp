# Decisions needed

Open questions that need Paul's input. Unblocked work continues around these.

## 1. Release / versioning of `lib-agent-output` and `lib-agent-mcp` — DEFERRED (by Paul)

**Status:** Deliberately deferred — "we will worry about actually releasing this
tomorrow. Right now the only users should be tests + the kitchen sink."

**Current state:** `lib-agent-mcp/go.mod` uses a local
`replace github.com/shhac/lib-agent-output => ../lib-agent-output`. This means
`lib-agent-mcp` is **not** `go get` / `go install`-able standalone; it only
builds with the sibling repo checked out at `../lib-agent-output`.

**When ready to release (tomorrow+):**
- Tag `lib-agent-output` (e.g. `v0.1.0`) and push the tag.
- In `lib-agent-mcp`, replace the local `replace` with a real
  `require github.com/shhac/lib-agent-output v0.1.0`.
- Optionally add a gitignored `go.work` for local cross-repo development.
- Then tag `lib-agent-mcp` `v0.1.0`.

No action taken in this loop beyond keeping the `replace` in place.

---

_(New blockers discovered during the autonomous readiness loop are appended below.)_
