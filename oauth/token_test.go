package oauth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer = "https://mcp.example.com"
)

func newTestIssuer(t *testing.T, store SecretStore, ttl time.Duration) *Issuer {
	t.Helper()
	iss, err := NewIssuer(store, testIssuer, testIssuer, ttl)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	return iss
}

func TestMintAndValidate(t *testing.T) {
	iss := newTestIssuer(t, NewMemStore(), time.Hour)

	token, ttl, err := iss.Mint("client-1", "mcp")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if ttl != time.Hour {
		t.Errorf("ttl = %v, want 1h", ttl)
	}
	v, err := iss.Validate(token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if v.ClientID != "client-1" || v.Scope != "mcp" {
		t.Errorf("verified = %+v, want client-1/mcp", v)
	}
}

func TestValidateRejectsTampered(t *testing.T) {
	iss := newTestIssuer(t, NewMemStore(), time.Hour)
	token, _, _ := iss.Mint("c", "")
	if _, err := iss.Validate(token + "x"); err == nil {
		t.Error("tampered token accepted")
	}
}

func TestValidateRejectsExpired(t *testing.T) {
	iss := newTestIssuer(t, NewMemStore(), -time.Minute) // already expired
	token, _, _ := iss.Mint("c", "")
	if _, err := iss.Validate(token); err == nil {
		t.Error("expired token accepted")
	}
}

func TestValidateRejectsWrongAudience(t *testing.T) {
	store := NewMemStore()
	minter, _ := NewIssuer(store, testIssuer, "https://other.example.com", time.Hour)
	checker, _ := NewIssuer(store, testIssuer, testIssuer, time.Hour) // same key, expects our audience
	token, _, _ := minter.Mint("c", "")
	if _, err := checker.Validate(token); err == nil {
		t.Error("token for a different audience accepted")
	}
}

func TestValidateRejectsWrongIssuer(t *testing.T) {
	store := NewMemStore()
	minter, _ := NewIssuer(store, "https://other-issuer.example.com", testIssuer, time.Hour)
	checker, _ := NewIssuer(store, testIssuer, testIssuer, time.Hour) // same key, expects our issuer
	token, _, _ := minter.Mint("c", "")
	if _, err := checker.Validate(token); err == nil {
		t.Error("token with a different issuer accepted")
	}
}

func TestValidateRejectsWrongKey(t *testing.T) {
	token, _, _ := newTestIssuer(t, NewMemStore(), time.Hour).Mint("c", "")
	other := newTestIssuer(t, NewMemStore(), time.Hour) // different store → different key
	if _, err := other.Validate(token); err == nil {
		t.Error("token validated under the wrong signing key")
	}
}

func TestValidateRejectsNoneAlg(t *testing.T) {
	// A token with alg=none must be refused — the classic JWT downgrade attack.
	iss := newTestIssuer(t, NewMemStore(), time.Hour)
	claims := tokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer:    testIssuer,
		Audience:  jwt.ClaimStrings{testIssuer},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}}
	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("crafting none token: %v", err)
	}
	if _, err := iss.Validate(none); err == nil {
		t.Error("alg=none token accepted")
	}
}

func TestSigningKeyPersistsAcrossIssuers(t *testing.T) {
	store := NewMemStore()
	token, _, _ := newTestIssuer(t, store, time.Hour).Mint("c", "scope-x")
	// A fresh issuer over the same store loads the same key → can validate.
	v, err := newTestIssuer(t, store, time.Hour).Validate(token)
	if err != nil {
		t.Fatalf("second issuer failed to validate: %v", err)
	}
	if v.Scope != "scope-x" {
		t.Errorf("scope = %q, want scope-x", v.Scope)
	}
}
