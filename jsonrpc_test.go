package agentmcp

import (
	"context"
	"encoding/json"
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
