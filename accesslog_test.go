package agentmcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccessLoggerWritesNDJSONAndRedacts(t *testing.T) {
	var buf bytes.Buffer
	al := &accessLogger{w: &buf}
	h := al.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hi"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Origin", "https://claude.ai")
	req.Header.Set("Authorization", "Bearer super-secret-token")
	req.Header.Set("Cookie", "session=super-secret-cookie")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	h.ServeHTTP(httptest.NewRecorder(), req)

	line := strings.TrimSpace(buf.String())
	for _, secret := range []string{"super-secret-token", "Authorization", "super-secret-cookie", "Cookie"} {
		if strings.Contains(line, secret) {
			t.Fatalf("access log leaked %q: %s", secret, line)
		}
	}

	var e map[string]any
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		t.Fatalf("not NDJSON: %v (%q)", err, line)
	}
	if e["method"] != "POST" || e["path"] != "/mcp" {
		t.Errorf("method/path = %v/%v", e["method"], e["path"])
	}
	if e["status"].(float64) != http.StatusCreated {
		t.Errorf("status = %v, want 201", e["status"])
	}
	if e["bytes"].(float64) != 2 {
		t.Errorf("bytes = %v, want 2", e["bytes"])
	}
	if e["origin"] != "https://claude.ai" || e["mcp_protocol"] != "2025-06-18" {
		t.Errorf("headers not captured: %v", e)
	}
}

func TestAccessLoggerOmitsEmptyHeaders(t *testing.T) {
	var buf bytes.Buffer
	al := &accessLogger{w: &buf}
	al.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if strings.Contains(buf.String(), "origin") {
		t.Errorf("empty Origin should be omitted: %q", buf.String())
	}
}

func TestNewAccessLoggerFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.ndjson")
	al, err := newAccessLogger(p)
	if err != nil {
		t.Fatalf("newAccessLogger: %v", err)
	}
	al.write(httptest.NewRequest(http.MethodGet, "/x", nil), 200, 5, time.Now())
	if err := al.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"path":"/x"`) {
		t.Errorf("file missing entry: %s", data)
	}
}
