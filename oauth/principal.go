package oauth

import (
	"context"
	"maps"
	"slices"
)

// principalKey keys the Verified identity Protect attaches to the request
// context.
type principalKey struct{}

// WithPrincipal returns ctx carrying the validated token identity. Protect
// calls it on every authorized request; it is exported so embedders and tests
// can construct principal-bearing contexts directly.
func WithPrincipal(ctx context.Context, v Verified) context.Context {
	return context.WithValue(ctx, principalKey{}, v)
}

// PrincipalFrom returns the identity Protect attached to ctx, if any. Tool
// dispatch reads it to bind a caller to per-principal credentials; absence
// means the transport had no OAuth gate (stdio, or plain HTTP).
func PrincipalFrom(ctx context.Context) (Verified, bool) {
	v, ok := ctx.Value(principalKey{}).(Verified)
	return v, ok
}

// PrincipalGrant is the identity a pairing established: which named principal
// approved the authorization, and the binding data (e.g. a credential-set
// selector) its tool calls carry. The zero value is the anonymous operator —
// the legacy shared pairing code, with no binding.
type PrincipalGrant struct {
	Name    string            `json:"name,omitempty"`
	Binding map[string]string `json:"binding,omitempty"`
}

// IsAnonymous reports whether this is the zero grant — the anonymous
// operator established by the legacy shared pairing code. This is THE
// definition of the operator/named-principal boundary: every fail-closed
// gate (identity injection, file-root scoping) routes through it, so the two
// can never drift.
func (g PrincipalGrant) IsAnonymous() bool {
	return g.Name == "" && len(g.Binding) == 0
}

// principalsStoreKey holds the JSON map of named principals in the
// SecretStore: name → {pairing code, binding}.
const principalsStoreKey = "principals"

// principalRecord is a stored named principal. The code is a secret exactly
// like the shared pairing code; the binding is non-secret routing data.
type principalRecord struct {
	Code    string            `json:"code"`
	Binding map[string]string `json:"binding,omitempty"`
}

// AddPrincipal mints (or rotates) the pairing code for a named principal and
// records its binding. Completing the OAuth approval with this code yields
// tokens whose subject principal is name and which carry binding.
func (p *Pairing) AddPrincipal(name string, binding map[string]string) (string, error) {
	code, err := generatePairingCode()
	if err != nil {
		return "", err
	}
	if err := p.principals.mutate(func(m map[string]principalRecord) bool {
		m[name] = principalRecord{Code: code, Binding: binding}
		return true
	}); err != nil {
		return "", err
	}
	return code, nil
}

// RemovePrincipal fully revokes a named principal: its pairing code stops
// verifying and its outstanding refresh tokens are deleted. It reports
// whether the principal existed. Already-minted access tokens live out their
// (short) TTL — that window is documented, not pretended away.
func (p *Pairing) RemovePrincipal(name string) (bool, error) {
	removed := false
	err := p.principals.mutate(func(m map[string]principalRecord) bool {
		if _, ok := m[name]; ok {
			delete(m, name)
			removed = true
		}
		return removed
	})
	if err != nil {
		return false, err
	}
	if err := newRefreshStore(p.store).removeForPrincipal(name); err != nil {
		return removed, err
	}
	return removed, nil
}

// Principals lists the named principals and their bindings (never codes).
func (p *Pairing) Principals() (map[string]map[string]string, error) {
	records, err := p.principals.load()
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string, len(records))
	for name, rec := range records {
		out[name] = rec.Binding
	}
	return out, nil
}

// VerifyPrincipal matches input against every acceptable pairing code — the
// shared operator code (as the anonymous, name-less grant) and each named
// principal's — and returns the matched identity.
func (p *Pairing) VerifyPrincipal(input string) (PrincipalGrant, bool, error) {
	candidates, err := p.candidates()
	if err != nil {
		return PrincipalGrant{}, false, err
	}
	got := normalizePairing(input)
	// Every candidate is compared, no early exit, so response timing reveals
	// neither whether nor which entry matched. The !found guard keeps
	// first-match-wins without a break — it is load-bearing for the
	// constant-time property, not an optimization.
	var matched PrincipalGrant
	found := false
	for _, c := range candidates {
		if constantTimeEqualPairing(got, c.code) && !found {
			matched = c.grant
			found = true
		}
	}
	return matched, found, nil
}

// pairingCandidate pairs one acceptable code with the grant it establishes.
type pairingCandidate struct {
	code  string
	grant PrincipalGrant
}

// candidates lists every acceptable pairing code in stable order: the shared
// operator code first (the zero grant), then the named principals sorted by
// name.
func (p *Pairing) candidates() ([]pairingCandidate, error) {
	shared, err := p.Code()
	if err != nil {
		return nil, err
	}
	records, err := p.principals.load()
	if err != nil {
		return nil, err
	}
	out := make([]pairingCandidate, 0, 1+len(records))
	out = append(out, pairingCandidate{code: shared})
	for _, name := range slices.Sorted(maps.Keys(records)) {
		rec := records[name]
		out = append(out, pairingCandidate{code: rec.Code, grant: PrincipalGrant{Name: name, Binding: rec.Binding}})
	}
	return out, nil
}
