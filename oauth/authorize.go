package oauth

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
)

// authParams are the authorization-request parameters, shared by the GET form
// and the POST submission (the form round-trips them as hidden fields).
type authParams struct {
	clientID            string
	redirectURI         string
	state               string
	scope               string
	codeChallenge       string
	codeChallengeMethod string
	responseType        string
}

func parseAuthParams(v url.Values) authParams {
	return authParams{
		clientID:            v.Get("client_id"),
		redirectURI:         v.Get("redirect_uri"),
		state:               v.Get("state"),
		scope:               v.Get("scope"),
		codeChallenge:       v.Get("code_challenge"),
		codeChallengeMethod: v.Get("code_challenge_method"),
		responseType:        v.Get("response_type"),
	}
}

// handleAuthorize renders the approval form (GET) and processes it (POST).
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.authorizeForm(w, r, parseAuthParams(r.URL.Query()))
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			s.authorizeErrorPage(w, "could not parse the form")
			return
		}
		s.authorizeSubmit(w, r, parseAuthParams(r.PostForm))
	default:
		w.Header().Set("Allow", "GET, POST")
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "method not allowed")
	}
}

// validatedRequest runs the shared authorization-request safety gates: it
// resolves and checks the client/redirect (fatal → error page) and the
// redirectable request params (→ error redirect). Both the GET form and the
// POST submission go through it, so the gates can't drift between them. A false
// second return means a response was already written.
func (s *Server) validatedRequest(w http.ResponseWriter, r *http.Request, p authParams) (Client, bool) {
	client, fatal := s.validateClientRedirect(p)
	if fatal != "" {
		s.authorizeErrorPage(w, fatal)
		return Client{}, false
	}
	if errCode := validateAuthParams(p); errCode != "" {
		s.redirectError(w, r, p, errCode)
		return Client{}, false
	}
	return client, true
}

// authorizeForm validates the request, then shows the pairing-code form.
func (s *Server) authorizeForm(w http.ResponseWriter, r *http.Request, p authParams) {
	client, ok := s.validatedRequest(w, r, p)
	if !ok {
		return
	}
	s.renderForm(w, client, p, "")
}

// authorizeSubmit re-validates, checks the pairing code, then issues an
// authorization code and redirects back to the client.
func (s *Server) authorizeSubmit(w http.ResponseWriter, r *http.Request, p authParams) {
	client, valid := s.validatedRequest(w, r, p)
	if !valid {
		return
	}

	principal, ok, err := s.pairing.VerifyPrincipal(r.PostForm.Get("pairing_code"))
	if err != nil {
		s.authorizeErrorPage(w, "internal error verifying the pairing code")
		return
	}
	if !ok {
		s.renderForm(w, client, p, "Incorrect pairing code — try again.")
		return
	}

	code, err := s.codes.issue(authGrant{
		ClientID:            client.ID,
		RedirectURI:         p.redirectURI,
		CodeChallenge:       p.codeChallenge,
		CodeChallengeMethod: p.codeChallengeMethod,
		Scope:               s.grantedScope(p.scope),
		Principal:           principal,
	})
	if err != nil {
		s.authorizeErrorPage(w, "internal error issuing the authorization code")
		return
	}
	s.redirectWith(w, r, p, url.Values{"code": {code}})
}

// validateClientRedirect resolves the client and checks the redirect URI is one
// it registered. A non-empty string is a fatal error (no safe redirect target).
func (s *Server) validateClientRedirect(p authParams) (Client, string) {
	if p.clientID == "" {
		return Client{}, "missing client_id"
	}
	c, ok, err := s.clients.Get(p.clientID)
	if err != nil {
		return Client{}, "internal error looking up the client"
	}
	if !ok {
		return Client{}, "unknown client_id"
	}
	if p.redirectURI == "" || !c.allowsRedirect(p.redirectURI) {
		return Client{}, "redirect_uri is not registered for this client"
	}
	return c, ""
}

// validateAuthParams checks the redirectable request parameters; a non-empty
// return is an OAuth error code to send back to the client's redirect URI.
func validateAuthParams(p authParams) string {
	switch {
	case p.responseType != "code":
		return "unsupported_response_type"
	case p.codeChallenge == "" || p.codeChallengeMethod != pkceMethodS256:
		return "invalid_request"
	default:
		return ""
	}
}

// grantedScope returns the scope to bind to the code: the requested one if any,
// else the server's default.
func (s *Server) grantedScope(requested string) string {
	if requested != "" {
		return requested
	}
	return s.scopes[0]
}

// redirectError redirects to the client with an OAuth error and the state.
func (s *Server) redirectError(w http.ResponseWriter, r *http.Request, p authParams, errCode string) {
	s.redirectWith(w, r, p, url.Values{"error": {errCode}})
}

// redirectWith redirects to p.redirectURI with extra query params plus state.
func (s *Server) redirectWith(w http.ResponseWriter, r *http.Request, p authParams, extra url.Values) {
	u, err := url.Parse(p.redirectURI)
	if err != nil {
		s.authorizeErrorPage(w, "invalid redirect_uri")
		return
	}
	q := u.Query()
	for k, vs := range extra {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	if p.state != "" {
		q.Set("state", p.state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

var authorizeTmpl = template.Must(template.New("authorize").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Authorize</title>
<style>
body{font-family:system-ui,sans-serif;max-width:30rem;margin:4rem auto;padding:0 1.25rem;color:#111}
h1{font-size:1.4rem} input{font-size:1rem;padding:.55rem;width:100%;box-sizing:border-box;margin-top:.25rem}
button{font-size:1rem;padding:.6rem 1.2rem;margin-top:1rem;cursor:pointer}
.err{color:#b00020} .muted{color:#555;font-size:.9rem}
</style></head><body>
<h1>Connect {{if .ClientName}}“{{.ClientName}}”{{else}}this client{{end}}?</h1>
<p class="muted">Enter the pairing code printed in the server's terminal to let this
client call your tools. It then acts with your credentials.</p>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form method="post" action="{{.Action}}">
  <label>Pairing code
    <input type="password" name="pairing_code" placeholder="mcp-XXXXX-XXXXX-…" autofocus autocomplete="off">
  </label>
  {{range $k, $v := .Hidden}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}
  <button type="submit">Authorize</button>
</form>
</body></html>`))

// renderForm shows the pairing-code form, echoing the request as hidden fields.
func (s *Server) renderForm(w http.ResponseWriter, client Client, p authParams, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = authorizeTmpl.Execute(w, map[string]any{
		"Action":     AuthorizePath,
		"ClientName": client.Name,
		"Error":      errMsg,
		"Hidden": map[string]string{
			"client_id":             p.clientID,
			"redirect_uri":          p.redirectURI,
			"response_type":         p.responseType,
			"code_challenge":        p.codeChallenge,
			"code_challenge_method": p.codeChallengeMethod,
			"state":                 p.state,
			"scope":                 p.scope,
		},
	})
}

// authorizeErrorPage renders a fatal authorization error (no safe redirect).
func (s *Server) authorizeErrorPage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = fmt.Fprintf(w, "<!doctype html><html lang=en><meta charset=utf-8><title>Cannot authorize</title>"+
		"<body style=\"font-family:system-ui;max-width:30rem;margin:4rem auto;padding:0 1.25rem\">"+
		"<h1>Cannot authorize</h1><p>%s</p></body></html>", template.HTMLEscapeString(msg))
}
