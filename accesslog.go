package agentmcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// accessLogger writes one NDJSON line per HTTP request to a file (or stderr),
// for debugging what a remote MCP client actually sent — the job a hand-rolled
// proxy used to do. Secrets (Authorization, Cookie) are never logged.
type accessLogger struct {
	mu     sync.Mutex
	w      io.Writer
	closer io.Closer // non-nil when we opened a file to close on shutdown
}

// newAccessLogger opens the destination: "-" means stderr, anything else is a
// file opened for append (created if absent).
func newAccessLogger(dest string) (*accessLogger, error) {
	if dest == "-" {
		return &accessLogger{w: os.Stderr}, nil
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("--access-log: %w", err)
	}
	return &accessLogger{w: f, closer: f}, nil
}

// Close releases the log file (no-op for stderr).
func (a *accessLogger) Close() error {
	if a.closer != nil {
		return a.closer.Close()
	}
	return nil
}

// middleware wraps next, logging each request once it completes.
func (a *accessLogger) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		a.write(r, sw.status, sw.bytes, time.Since(start))
	})
}

// write emits one NDJSON entry. Empty header fields are omitted to keep lines
// lean; Authorization/Cookie are deliberately never included.
func (a *accessLogger) write(r *http.Request, status, bytes int, dur time.Duration) {
	entry := map[string]any{
		"time":   start(dur),
		"method": r.Method,
		"path":   r.URL.Path,
		"status": status,
		"bytes":  bytes,
		"dur_ms": dur.Milliseconds(),
	}
	for field, header := range map[string]string{
		"origin":        "Origin",
		"mcp_protocol":  "MCP-Protocol-Version",
		"user_agent":    "User-Agent",
		"forwarded_for": "X-Forwarded-For",
	} {
		if v := r.Header.Get(header); v != "" {
			entry[field] = v
		}
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.w.Write(append(b, '\n'))
}

// start renders the request's wall-clock start time as RFC3339 (now minus the
// elapsed duration), so the entry timestamps when the request arrived.
func start(elapsed time.Duration) string {
	return time.Now().Add(-elapsed).Format(time.RFC3339Nano)
}

// statusRecorder captures the status code and byte count of a response so the
// access log can report them.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}
