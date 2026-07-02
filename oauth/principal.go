package oauth

import "context"

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

// principalsStoreKey holds the JSON map of named principals in the
// SecretStore: name → {pairing code, binding}.
const principalsStoreKey = "principals"

// principalRecord is a stored named principal. The code is a secret exactly
// like the shared pairing code; the binding is non-secret routing data.
type principalRecord struct {
	Code    string            `json:"code"`
	Binding map[string]string `json:"binding,omitempty"`
}

func (p *Pairing) principals() *jsonMapStore[principalRecord] {
	return &jsonMapStore[principalRecord]{store: p.store, key: principalsStoreKey}
}

// AddPrincipal mints (or rotates) the pairing code for a named principal and
// records its binding. Completing the OAuth approval with this code yields
// tokens whose subject principal is name and which carry binding.
func (p *Pairing) AddPrincipal(name string, binding map[string]string) (string, error) {
	code, err := generatePairingCode()
	if err != nil {
		return "", err
	}
	if err := p.principals().mutate(func(m map[string]principalRecord) bool {
		m[name] = principalRecord{Code: code, Binding: binding}
		return true
	}); err != nil {
		return "", err
	}
	return code, nil
}

// RemovePrincipal deletes a named principal's pairing code, reporting whether
// it existed. Callers wanting the full revocation (refresh tokens included)
// use the package-level RemovePrincipal.
func (p *Pairing) RemovePrincipal(name string) (bool, error) {
	removed := false
	err := p.principals().mutate(func(m map[string]principalRecord) bool {
		if _, ok := m[name]; ok {
			delete(m, name)
			removed = true
		}
		return removed
	})
	return removed, err
}

// Principals lists the named principals and their bindings (never codes).
func (p *Pairing) Principals() (map[string]map[string]string, error) {
	records, err := p.principals().load()
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string, len(records))
	for name, rec := range records {
		out[name] = rec.Binding
	}
	return out, nil
}

// VerifyPrincipal matches input against the shared pairing code (the
// anonymous operator) and every named principal's code, constant-time each.
// It returns the matched identity.
func (p *Pairing) VerifyPrincipal(input string) (PrincipalGrant, bool, error) {
	ok, err := p.Verify(input)
	if err != nil {
		return PrincipalGrant{}, false, err
	}
	if ok {
		return PrincipalGrant{}, true, nil
	}
	records, err := p.principals().load()
	if err != nil {
		return PrincipalGrant{}, false, err
	}
	got := normalizePairing(input)
	// Compare against every record (no early exit) so timing doesn't reveal
	// which principal matched.
	var matched PrincipalGrant
	found := false
	for name, rec := range records {
		if constantTimeEqualPairing(got, rec.Code) && !found {
			matched = PrincipalGrant{Name: name, Binding: rec.Binding}
			found = true
		}
	}
	return matched, found, nil
}

// RemovePrincipal fully revokes a named principal against store: its pairing
// code stops verifying and its outstanding refresh tokens are deleted.
// Already-minted access tokens live out their (short) TTL — document the
// window, don't pretend it away.
func RemovePrincipal(store SecretStore, name string) (bool, error) {
	pairing := NewPairing(store)
	removed, err := pairing.RemovePrincipal(name)
	if err != nil {
		return false, err
	}
	if err := newRefreshStore(store).removeForPrincipal(name); err != nil {
		return removed, err
	}
	return removed, nil
}
