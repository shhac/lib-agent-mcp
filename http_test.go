package agentmcp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// postMCP sends one JSON-RPC body to a test server's /mcp and returns the
// response. baseURL is an httptest server URL.
func postMCP(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	resp, err := client.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	return resp
}

func httpTestServer(t *testing.T) (*http.Client, string) {
	t.Helper()
	s := newServer(testRoot())
	srv := httptest.NewServer(s.httpHandler())
	t.Cleanup(srv.Close)
	return srv.Client(), srv.URL + mcpHTTPPath
}

func TestHTTPInitialize(t *testing.T) {
	client, url := httpTestServer(t)
	resp := postMCP(t, client, url, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var out rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	res, _ := out.Result.(map[string]any)
	if res["protocolVersion"] == nil {
		t.Errorf("initialize result missing protocolVersion: %+v", out)
	}
}

func TestHTTPToolsList(t *testing.T) {
	client, url := httpTestServer(t)
	resp := postMCP(t, client, url, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	defer resp.Body.Close()

	var out rpcResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	res, _ := out.Result.(map[string]any)
	if _, ok := res["tools"]; !ok {
		t.Errorf("tools/list missing tools: %+v", out)
	}
}

func TestHTTPNotificationGets202(t *testing.T) {
	client, url := httpTestServer(t)
	resp := postMCP(t, client, url, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Errorf("notification body = %q, want empty", body)
	}
}

func TestHTTPParseError(t *testing.T) {
	client, url := httpTestServer(t)
	resp := postMCP(t, client, url, `{not json`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var out rpcResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Error == nil || out.Error.Code != -32700 {
		t.Errorf("want parse error -32700, got %+v", out.Error)
	}
}

func TestHTTPGetNotAllowed(t *testing.T) {
	client, url := httpTestServer(t)
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
	if a := resp.Header.Get("Allow"); a != http.MethodPost {
		t.Errorf("Allow = %q, want POST", a)
	}
}

func TestHTTPNonPostMethodsRejected(t *testing.T) {
	client, url := httpTestServer(t)
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, _ := http.NewRequest(method, url, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, resp.StatusCode)
		}
		if a := resp.Header.Get("Allow"); a != http.MethodPost {
			t.Errorf("%s Allow = %q, want POST", method, a)
		}
	}
}

func TestHTTPUnknownMethod(t *testing.T) {
	client, url := httpTestServer(t)
	resp := postMCP(t, client, url, `{"jsonrpc":"2.0","id":7,"method":"does/not/exist"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (JSON-RPC error rides a 200)", resp.StatusCode)
	}
	var out rpcResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Error == nil || out.Error.Code != -32601 {
		t.Errorf("want method-not-found -32601, got %+v", out.Error)
	}
}

func TestHTTPURL(t *testing.T) {
	cases := map[string]string{
		":8000":          "http://localhost:8000/mcp",
		"127.0.0.1:8000": "http://127.0.0.1:8000/mcp",
		"0.0.0.0:9000":   "http://0.0.0.0:9000/mcp",
	}
	for addr, want := range cases {
		if got := httpURL(addr); got != want {
			t.Errorf("httpURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestCORSPreflight(t *testing.T) {
	client, url := httpTestServer(t)
	req, _ := http.NewRequest(http.MethodOptions, url, nil)
	req.Header.Set("Origin", "https://claude.ai")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://claude.ai" {
		t.Errorf("Allow-Origin = %q, want the request origin", got)
	}
	if !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("Allow-Methods = %q, want POST", resp.Header.Get("Access-Control-Allow-Methods"))
	}
}

func TestCORSHeaderOnResponse(t *testing.T) {
	client, url := httpTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Origin", "https://claude.ai")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://claude.ai" {
		t.Errorf("Allow-Origin on response = %q, want the request origin", got)
	}
}

func TestServeHTTPListensAndShutsDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := newServer(testRoot())
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.serveHTTP(ctx, ln) }()

	url := "http://" + ln.Addr().String() + mcpHTTPPath
	// Poll until the server answers (listener is already open, so this is quick).
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Post(url, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("server never answered: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("ping status = %d, want 200", resp.StatusCode)
	}

	cancel() // trigger graceful shutdown
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serveHTTP returned %v, want nil after shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveHTTP did not return after context cancel")
	}
}
