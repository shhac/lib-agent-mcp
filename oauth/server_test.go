package oauth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	testPublicURL  = "https://mcp.example.com"
	testRedirect   = "https://client.example/callback"
	testVerifier   = "a-sufficiently-long-pkce-code-verifier-0123456789"
	testToolOKBody = "tool-ok"
)

// oauthHarness builds a Server (MemStore) and an httptest.Server that mounts the
// OAuth routes and a Protect-gated /mcp.
type oauthHarness struct {
	srv    *Server
	http   *httptest.Server
	client *http.Client
}

func newHarness(t *testing.T) *oauthHarness {
	t.Helper()
	srv, err := New(Config{Store: NewMemStore(), PublicURL: testPublicURL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	mux.Handle("/mcp", srv.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(testToolOKBody))
	})))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	// Don't auto-follow redirects: we inspect the Location off the authorize POST.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &oauthHarness{srv: srv, http: ts, client: client}
}

func (h *oauthHarness) url(path string) string { return h.http.URL + path }

func (h *oauthHarness) get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := h.client.Get(h.url(path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (h *oauthHarness) postForm(t *testing.T, path string, form url.Values) *http.Response {
	t.Helper()
	resp, err := h.client.PostForm(h.url(path), form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (h *oauthHarness) getJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	resp, err := h.client.Get(h.url(path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return m
}

func TestMetadataDocuments(t *testing.T) {
	h := newHarness(t)

	prm := h.getJSON(t, ProtectedResourceMetadataPath)
	if prm["resource"] != testPublicURL {
		t.Errorf("PRM resource = %v, want %s", prm["resource"], testPublicURL)
	}
	if as, _ := prm["authorization_servers"].([]any); len(as) != 1 || as[0] != testPublicURL {
		t.Errorf("PRM authorization_servers = %v", prm["authorization_servers"])
	}

	md := h.getJSON(t, AuthServerMetadataPath)
	if md["issuer"] != testPublicURL {
		t.Errorf("AS issuer = %v", md["issuer"])
	}
	if md["authorization_endpoint"] != testPublicURL+AuthorizePath ||
		md["token_endpoint"] != testPublicURL+TokenPath ||
		md["registration_endpoint"] != testPublicURL+RegisterPath {
		t.Errorf("AS endpoints wrong: %v", md)
	}
	if m, _ := md["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != "S256" {
		t.Errorf("AS pkce methods = %v, want [S256]", md["code_challenge_methods_supported"])
	}
}

func TestMCPRequiresToken(t *testing.T) {
	h := newHarness(t)
	resp, err := h.client.Get(h.url("/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	wa := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(wa, "Bearer") || !strings.Contains(wa, ProtectedResourceMetadataPath) {
		t.Errorf("WWW-Authenticate = %q, want a Bearer resource_metadata challenge", wa)
	}
}

// registerClient runs DCR and returns the client_id.
func (h *oauthHarness) registerClient(t *testing.T) string {
	t.Helper()
	body := `{"redirect_uris":["` + testRedirect + `"],"client_name":"Test Client"}`
	resp, err := h.client.Post(h.url(RegisterPath), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", resp.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	id, _ := out["client_id"].(string)
	if id == "" {
		t.Fatal("register returned empty client_id")
	}
	return id
}

// authorize POSTs the approval form and returns the redirect Location.
func (h *oauthHarness) authorize(t *testing.T, clientID, pairingCode string) *url.URL {
	t.Helper()
	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {testRedirect},
		"response_type":         {"code"},
		"code_challenge":        {challengeFor(testVerifier)},
		"code_challenge_method": {"S256"},
		"state":                 {"xyz"},
		"scope":                 {"mcp"},
		"pairing_code":          {pairingCode},
	}
	resp, err := h.client.PostForm(h.url(AuthorizePath), form)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302 (got body? check pairing code)", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("bad redirect Location: %v", err)
	}
	if loc.Query().Get("state") != "xyz" {
		t.Errorf("redirect missing state: %s", loc)
	}
	return loc
}

// exchange runs the token endpoint for an authorization code.
func (h *oauthHarness) exchange(t *testing.T, clientID, code string) map[string]any {
	t.Helper()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {testRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	}
	resp, err := h.client.PostForm(h.url(TokenPath), form)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d, want 200", resp.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func (h *oauthHarness) callMCP(t *testing.T, accessToken string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.url("/mcp"), nil)
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("call /mcp: %v", err)
	}
	return resp
}

func TestFullAuthorizationCodeFlow(t *testing.T) {
	h := newHarness(t)
	pairing, _ := h.srv.PairingCode()

	clientID := h.registerClient(t)
	loc := h.authorize(t, clientID, pairing)
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("no authorization code in redirect")
	}

	tokens := h.exchange(t, clientID, code)
	access, _ := tokens["access_token"].(string)
	refresh, _ := tokens["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("missing tokens: %v", tokens)
	}
	if tokens["token_type"] != "Bearer" {
		t.Errorf("token_type = %v, want Bearer", tokens["token_type"])
	}

	// The access token unlocks /mcp.
	resp := h.callMCP(t, access)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/mcp with token = %d, want 200", resp.StatusCode)
	}

	// Refresh yields a new access token; the old refresh token is rotated out.
	rForm := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}
	rResp := h.postForm(t, TokenPath, rForm)
	defer rResp.Body.Close()
	if rResp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200", rResp.StatusCode)
	}
	var refreshed map[string]any
	_ = json.NewDecoder(rResp.Body).Decode(&refreshed)
	if refreshed["access_token"] == "" {
		t.Error("refresh produced no access token")
	}
	// Reusing the rotated refresh token must fail.
	again := h.postForm(t, TokenPath, rForm)
	defer again.Body.Close()
	if again.StatusCode == http.StatusOK {
		t.Error("rotated refresh token was accepted twice")
	}
}

func TestAuthorizeWrongPairingCodeRerendersForm(t *testing.T) {
	h := newHarness(t)
	clientID := h.registerClient(t)
	form := url.Values{
		"client_id": {clientID}, "redirect_uri": {testRedirect}, "response_type": {"code"},
		"code_challenge": {challengeFor(testVerifier)}, "code_challenge_method": {"S256"},
		"pairing_code": {"mcp-00000-00000-00000-00000-00000"},
	}
	resp := h.postForm(t, AuthorizePath, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { // form re-rendered, not a redirect
		t.Fatalf("status = %d, want 200 (form re-render)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Incorrect pairing code") {
		t.Error("re-rendered form missing the error message")
	}
}

func TestTokenRejectsBadPKCE(t *testing.T) {
	h := newHarness(t)
	pairing, _ := h.srv.PairingCode()
	clientID := h.registerClient(t)
	code := h.authorize(t, clientID, pairing).Query().Get("code")

	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {testRedirect},
		"client_id": {clientID}, "code_verifier": {"the-WRONG-verifier"},
	}
	resp := h.postForm(t, TokenPath, form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad-PKCE token status = %d, want 400", resp.StatusCode)
	}
}

func TestAuthorizeUnknownClientIsFatal(t *testing.T) {
	h := newHarness(t)
	resp := h.get(t, AuthorizePath+"?client_id=nope&redirect_uri="+url.QueryEscape(testRedirect)+"&response_type=code")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown client status = %d, want 400", resp.StatusCode)
	}
}

func TestConsumedCodeCannotBeReused(t *testing.T) {
	h := newHarness(t)
	pairing, _ := h.srv.PairingCode()
	clientID := h.registerClient(t)
	code := h.authorize(t, clientID, pairing).Query().Get("code")

	_ = h.exchange(t, clientID, code) // first exchange succeeds (asserts 200 within)

	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {testRedirect},
		"client_id": {clientID}, "code_verifier": {testVerifier},
	}
	resp := h.postForm(t, TokenPath, form)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("authorization code was accepted a second time (must be single-use)")
	}
}

func TestValidRedirectURI(t *testing.T) {
	good := []string{
		"https://app.example/cb", "https://app.example/cb?x=1",
		"http://localhost:8080/cb", "http://127.0.0.1/cb", "http://[::1]/cb",
	}
	bad := []string{
		"http://evil.example/cb", // non-loopback http
		"ftp://app.example/cb",   // wrong scheme
		"/relative", "https://", "not a url", "",
	}
	for _, u := range good {
		if !validRedirectURI(u) {
			t.Errorf("validRedirectURI(%q) = false, want true", u)
		}
	}
	for _, u := range bad {
		if validRedirectURI(u) {
			t.Errorf("validRedirectURI(%q) = true, want false", u)
		}
	}
}

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(Config{PublicURL: testPublicURL}); err == nil {
		t.Error("New without Store should error")
	}
	if _, err := New(Config{Store: NewMemStore()}); err == nil {
		t.Error("New without PublicURL should error")
	}
}
