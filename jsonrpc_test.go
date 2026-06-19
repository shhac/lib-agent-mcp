package agentmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func rpcReq(id, method, params string) rpcRequest {
	r := rpcRequest{JSONRPC: "2.0", Method: method}
	if id != "" {
		r.ID = json.RawMessage(id)
	}
	if params != "" {
		r.Params = json.RawMessage(params)
	}
	return r
}

func TestDispatchInitializeEchoesProtocolVersion(t *testing.T) {
	s := newServer(testRoot())
	resp := s.dispatch(context.Background(), rpcReq("1", "initialize", `{"protocolVersion":"2025-03-26"}`))
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	res := resp.Result.(map[string]any)
	if res["protocolVersion"] != "2025-03-26" {
		t.Errorf("protocolVersion = %v, want echo of client", res["protocolVersion"])
	}
	if si := res["serverInfo"].(map[string]any); si["name"] != "widget" {
		t.Errorf("serverInfo.name = %v", si["name"])
	}
}

func TestDispatchInitializeDefaultsProtocolVersion(t *testing.T) {
	s := newServer(testRoot())
	resp := s.dispatch(context.Background(), rpcReq("1", "initialize", `{}`))
	res := resp.Result.(map[string]any)
	if res["protocolVersion"] != defaultProtocolVersion {
		t.Errorf("protocolVersion = %v, want default %v", res["protocolVersion"], defaultProtocolVersion)
	}
}

func TestDispatchPing(t *testing.T) {
	s := newServer(testRoot())
	resp := s.dispatch(context.Background(), rpcReq("2", "ping", ""))
	if resp.Error != nil {
		t.Fatalf("ping errored: %v", resp.Error)
	}
	if m, ok := resp.Result.(map[string]any); !ok || len(m) != 0 {
		t.Errorf("ping result = %v, want empty object", resp.Result)
	}
}

func TestDispatchUnknownMethod(t *testing.T) {
	s := newServer(testRoot())
	resp := s.dispatch(context.Background(), rpcReq("3", "frobnicate", ""))
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Errorf("unknown method should be -32601, got %v", resp.Error)
	}
}

func TestDispatchUnknownTool(t *testing.T) {
	s := newServer(testRoot())
	resp := s.dispatch(context.Background(), rpcReq("4", "tools/call", `{"name":"nope","arguments":{"args":[],"options":{}}}`))
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Errorf("unknown tool should be -32602, got %v", resp.Error)
	}
}

func TestDispatchToolsList(t *testing.T) {
	s := newServer(testRoot())
	resp := s.dispatch(context.Background(), rpcReq("5", "tools/list", ""))
	res := resp.Result.(map[string]any)
	tools, ok := res["tools"].([]Tool)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools/list returned no tools: %v", res["tools"])
	}
}

func TestServeSkipsNotificationsAndMalformedLines(t *testing.T) {
	s := newServer(testRoot())
	in := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // notification → no response
		`not json at all`, // malformed → silently skipped
		``,                // blank line → skipped
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	}, "\n") + "\n")

	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 responses (only the two pings), got %d: %q", len(lines), out.String())
	}
	for _, line := range lines {
		var resp map[string]any
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("response not valid JSON: %q", line)
		}
		if resp["id"] == nil {
			t.Errorf("response missing id (a notification leaked a response?): %q", line)
		}
	}
}
