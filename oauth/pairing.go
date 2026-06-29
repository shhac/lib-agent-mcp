package oauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"strings"
)

// pairingCodeStoreKey is where the pairing code lives in the SecretStore.
const pairingCodeStoreKey = "pairing-code"

// pairingPrefix identifies the code in logs and paste boxes (the modern secret
// convention, like sk- / ghp_).
const pairingPrefix = "mcp-"

// crockford is Crockford's base32 alphabet — no I/L/O/U, so the code is
// legible and resistant to transcription mistakes. The encoder is padding-free.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var crockEnc = base32.NewEncoding(crockford).WithPadding(base32.NoPadding)

// Pairing manages the local pairing code — the single reusable secret a human
// enters at the authorize endpoint to prove "it's me" (there are no accounts).
// Because it's reusable, every client (Claude, Codex, …) pairs with the same
// code, which is what allows many concurrent connections.
type Pairing struct{ store SecretStore }

// NewPairing returns a Pairing backed by store.
func NewPairing(store SecretStore) *Pairing { return &Pairing{store: store} }

// Code returns the pairing code, generating and persisting one on first use so
// it is stable across restarts.
func (p *Pairing) Code() (string, error) {
	if v, ok, err := p.store.Get(pairingCodeStoreKey); err != nil {
		return "", err
	} else if ok {
		return v, nil
	}
	return p.Rotate()
}

// Rotate generates a fresh pairing code, stores it, and returns it. Any code a
// client paired with before is invalidated.
func (p *Pairing) Rotate() (string, error) {
	code, err := generatePairingCode()
	if err != nil {
		return "", err
	}
	if err := p.store.Set(pairingCodeStoreKey, code); err != nil {
		return "", err
	}
	return code, nil
}

// Verify reports whether input matches the pairing code, tolerant of case,
// hyphens, spaces, and the Crockford-confusable characters a human might mistype
// (O→0, I/L→1). The comparison is constant-time.
func (p *Pairing) Verify(input string) (bool, error) {
	code, err := p.Code()
	if err != nil {
		return false, err
	}
	got := normalizePairing(input)
	want := normalizePairing(code)
	// Always go through the constant-time compare — an empty or short input is
	// rejected by the length mismatch, not by a faster path.
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1, nil
}

// generatePairingCode returns a prefixed, hyphen-grouped, ~125-bit Crockford
// base32 code, e.g. "mcp-K7Q29-F3MXR-8WZ4T-...".
func generatePairingCode() (string, error) {
	b := make([]byte, 16) // 128 bits
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth: generating pairing code: %w", err)
	}
	s := crockEnc.EncodeToString(b)[:25] // 25 chars → five 5-char groups
	groups := make([]string, 0, 5)
	for i := 0; i < len(s); i += 5 {
		groups = append(groups, s[i:i+5])
	}
	return pairingPrefix + strings.Join(groups, "-"), nil
}

// normalizePairing canonicalizes a code for comparison: lowercase, strip
// hyphens/spaces, drop the prefix, and fold the Crockford-confusable characters.
// Order matters — separators are removed before the prefix, so a de-hyphenated
// "mcpXXXXX…" still has its prefix recognised.
func normalizePairing(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("-", "", " ", "").Replace(s)
	s = strings.TrimPrefix(s, strings.ReplaceAll(pairingPrefix, "-", ""))
	s = strings.NewReplacer("o", "0", "i", "1", "l", "1").Replace(s)
	return strings.ToUpper(s)
}
