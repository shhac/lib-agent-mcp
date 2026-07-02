package oauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestPrincipalPairingCodes(t *testing.T) {
	p := NewPairing(NewMemStore())

	code, err := p.AddPrincipal("alice", map[string]string{"workspace": "alice-acme"})
	if err != nil {
		t.Fatal(err)
	}
	if code == "" {
		t.Fatal("empty principal code")
	}

	grant, ok, err := p.VerifyPrincipal(code)
	if err != nil || !ok {
		t.Fatalf("verify alice: ok=%v err=%v", ok, err)
	}
	if grant.Name != "alice" || grant.Binding["workspace"] != "alice-acme" {
		t.Errorf("grant = %+v", grant)
	}

	// The legacy shared code still verifies — as the anonymous operator.
	legacy, err := p.Code()
	if err != nil {
		t.Fatal(err)
	}
	anon, ok, err := p.VerifyPrincipal(legacy)
	if err != nil || !ok {
		t.Fatalf("verify legacy: ok=%v err=%v", ok, err)
	}
	if anon.Name != "" {
		t.Errorf("legacy code should carry no principal, got %+v", anon)
	}

	if _, ok, _ := p.VerifyPrincipal("mcp-00000-00000-00000-00000-00000"); ok {
		t.Error("garbage code verified")
	}
}

func TestAddPrincipalRotatesExistingCode(t *testing.T) {
	p := NewPairing(NewMemStore())
	first, _ := p.AddPrincipal("alice", nil)
	second, err := p.AddPrincipal("alice", map[string]string{"workspace": "ws"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := p.VerifyPrincipal(first); ok {
		t.Error("re-adding a principal must rotate out its old code")
	}
	grant, ok, _ := p.VerifyPrincipal(second)
	if !ok || grant.Binding["workspace"] != "ws" {
		t.Errorf("rotated code grant = %+v ok=%v", grant, ok)
	}
}

func TestFullFlowCarriesPrincipalIntoTokens(t *testing.T) {
	h := newHarness(t)
	code, err := h.srv.pairing.AddPrincipal("alice", map[string]string{"workspace": "alice-acme"})
	if err != nil {
		t.Fatal(err)
	}

	clientID := h.registerClient(t)
	authCode := h.authorize(t, clientID, code).Query().Get("code")
	if authCode == "" {
		t.Fatal("no authorization code")
	}
	tokens := h.exchange(t, clientID, authCode)
	access, _ := tokens["access_token"].(string)
	refresh, _ := tokens["refresh_token"].(string)

	v, err := h.srv.issuer.Validate(access)
	if err != nil {
		t.Fatal(err)
	}
	if v.Principal != "alice" || v.Binding["workspace"] != "alice-acme" {
		t.Errorf("verified = %+v", v)
	}

	// Refresh preserves the principal and binding.
	rResp := h.postForm(t, TokenPath, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	defer rResp.Body.Close()
	if rResp.StatusCode != http.StatusOK {
		t.Fatalf("refresh = %d", rResp.StatusCode)
	}
	var refreshed map[string]any
	if err := json.NewDecoder(rResp.Body).Decode(&refreshed); err != nil {
		t.Fatal(err)
	}
	v2, err := h.srv.issuer.Validate(refreshed["access_token"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if v2.Principal != "alice" || v2.Binding["workspace"] != "alice-acme" {
		t.Errorf("refreshed verified = %+v", v2)
	}
}

func TestRemovePrincipalRevokesCodeAndRefreshTokens(t *testing.T) {
	store := NewMemStore()
	h := newHarnessWithStore(t, store)
	code, _ := h.srv.pairing.AddPrincipal("bob", nil)

	clientID := h.registerClient(t)
	authCode := h.authorize(t, clientID, code).Query().Get("code")
	tokens := h.exchange(t, clientID, authCode)
	refresh, _ := tokens["refresh_token"].(string)

	removed, err := RemovePrincipal(store, "bob")
	if err != nil || !removed {
		t.Fatalf("remove: %v removed=%v", err, removed)
	}

	if _, ok, _ := h.srv.pairing.VerifyPrincipal(code); ok {
		t.Error("removed principal's code still verifies")
	}
	rResp := h.postForm(t, TokenPath, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	defer rResp.Body.Close()
	if rResp.StatusCode == http.StatusOK {
		t.Error("removed principal's refresh token still exchanges")
	}
}
