package agentmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
)

// defaultProtocolVersion is echoed when the client does not request one.
const defaultProtocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve runs the MCP stdio transport: newline-delimited JSON-RPC 2.0 messages
// in, responses out, until in is exhausted.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	var mu sync.Mutex
	write := func(resp rpcResponse) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(resp)
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if len(req.ID) == 0 {
			// Notification (e.g. notifications/initialized): no response.
			continue
		}
		write(s.dispatch(ctx, req))
	}
	return scanner.Err()
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = s.handleInitialize(req.Params)
	case "tools/list":
		resp.Result = map[string]any{"tools": s.buildTools()}
	case "tools/call":
		result, rerr := s.handleToolCall(ctx, req.Params)
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
	case "ping":
		resp.Result = map[string]any{}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

func (s *Server) handleInitialize(params json.RawMessage) map[string]any {
	version := defaultProtocolVersion
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(params, &p) == nil && p.ProtocolVersion != "" {
		version = p.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": s.opts.name, "version": s.opts.version},
	}
}

func (s *Server) handleToolCall(ctx context.Context, params json.RawMessage) (map[string]any, *rpcError) {
	var p struct {
		Name      string `json:"name"`
		Arguments struct {
			Args    []any          `json:"args"`
			Options map[string]any `json:"options"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}

	tools := s.buildTools()
	var tool *Tool
	for i := range tools {
		if tools[i].Name == p.Name {
			tool = &tools[i]
			break
		}
	}
	if tool == nil {
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}

	args := make([]string, 0, len(p.Arguments.Args))
	for _, a := range p.Arguments.Args {
		args = append(args, toArg(a))
	}
	return translate(s.run(ctx, tool, args, p.Arguments.Options)), nil
}
