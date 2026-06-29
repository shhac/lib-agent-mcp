package agentmcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	// mcpHTTPPath is the single endpoint of the Streamable HTTP transport.
	mcpHTTPPath = "/mcp"
	// maxHTTPBody caps a request body — one JSON-RPC message, not a payload.
	maxHTTPBody = 4 << 20 // 4 MiB
	// httpReadHeaderTimeout bounds slow-loris header reads.
	httpReadHeaderTimeout = 10 * time.Second
	// httpShutdownGrace bounds the graceful drain on context cancellation.
	httpShutdownGrace = 5 * time.Second
)

// ServeHTTP runs the MCP Streamable HTTP transport, listening on addr (e.g.
// ":8000" or "127.0.0.1:8000") until ctx is cancelled, then draining gracefully.
//
// This transport performs NO authorization: any caller that can reach addr can
// invoke every exposed tool. Bind it to loopback, or front it with an
// authenticating proxy/tunnel, before exposing it beyond the local machine.
func (s *Server) ServeHTTP(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.serveHTTP(ctx, ln)
}

// serveHTTP serves the transport on an existing listener — the testable core of
// ServeHTTP (a test can listen on :0 and learn the chosen port).
func (s *Server) serveHTTP(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.httpHandler(), ReadHeaderTimeout: httpReadHeaderTimeout}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), httpShutdownGrace)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// httpHandler maps the single MCP endpoint.
func (s *Server) httpHandler() http.Handler {
	mux := http.NewServeMux()
	if s.oauth != nil {
		// Local OAuth: serve the discovery + authorization endpoints and gate
		// /mcp behind a valid bearer token.
		s.oauth.RegisterRoutes(mux)
		mux.Handle(mcpHTTPPath, s.oauth.Protect(http.HandlerFunc(s.handleHTTP)))
		return mux
	}
	mux.HandleFunc(mcpHTTPPath, s.handleHTTP)
	return mux
}

// handleHTTP serves /mcp. POST carries one JSON-RPC message; GET (server-
// initiated SSE) is unsupported because this server never initiates messages,
// so it answers 405 — a valid response per the Streamable HTTP spec.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody))
	if err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, -32700, "could not read request body")
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, -32700, "parse error")
		return
	}

	// A notification (no id) expects no response — acknowledge and return.
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	writeJSON(w, http.StatusOK, s.dispatch(r.Context(), req))
}

// writeJSON encodes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeRPCError writes a JSON-RPC error response (used for transport-level
// failures like an unparseable body, where there may be no request id).
func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string) {
	writeJSON(w, status, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}
