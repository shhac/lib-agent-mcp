package agentmcp

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestEndToEnd builds the widget example, runs `widget mcp`, and drives a real
// MCP stdio handshake against it — proving schema generation, the protocol
// loop, subprocess self-exec, and NDJSON translation together.
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e build in -short")
	}

	bin := filepath.Join(t.TempDir(), "widget")
	if out, err := exec.Command("go", "build", "-o", bin, "./examples/widget").CombinedOutput(); err != nil {
		t.Fatalf("build widget: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "mcp")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	enc := json.NewEncoder(stdin)
	rd := bufio.NewReader(stdout)
	send := func(v any) {
		if err := enc.Encode(v); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	recv := func() map[string]any {
		line, err := rd.ReadBytes('\n')
		if err != nil && err != io.EOF {
			t.Fatalf("recv: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("recv parse %q: %v", line, err)
		}
		return m
	}

	send(rpc(1, "initialize", map[string]any{"protocolVersion": "2025-06-18"}))
	si := result(t, recv())["serverInfo"].(map[string]any)
	if si["name"] != "widget" {
		t.Errorf("serverInfo.name = %v, want widget", si["name"])
	}

	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	send(rpc(2, "tools/list", nil))
	tools := toolNames(result(t, recv()))
	for _, want := range []string{"item_list", "item_get", "item_delete"} {
		if !tools[want] {
			t.Errorf("tools/list missing %q; have %v", want, tools)
		}
	}

	send(call(3, "item_list", nil, nil))
	if got := len(records(t, recv())); got != 3 {
		t.Errorf("item_list returned %d records, want 3", got)
	}

	send(call(4, "item_get", []any{"w-2"}, nil))
	recs := records(t, recv())
	if len(recs) != 1 || recs[0].(map[string]any)["id"] != "w-2" {
		t.Errorf("item_get w-2 = %v", recs)
	}

	send(call(5, "item_get", []any{"nope"}, nil))
	missing := result(t, recv())
	if missing["isError"] != true {
		t.Errorf("missing item_get should be isError, got %v", missing["isError"])
	}
	if fb := fixableBy(missing); fb != "agent" {
		t.Errorf("missing item_get fixable_by = %q, want agent", fb)
	}

	// Gated: the bridge injects --yes, so the delete succeeds rather than
	// dead-ending on the confirmation gate.
	send(call(6, "item_delete", []any{"w-1"}, nil))
	del := records(t, recv())
	if len(del) != 1 || del[0].(map[string]any)["deleted"] != "w-1" {
		t.Errorf("item_delete result = %v", del)
	}
}

func rpc(id int, method string, params any) map[string]any {
	m := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		m["params"] = params
	}
	return m
}

func call(id int, name string, args []any, options map[string]any) map[string]any {
	if args == nil {
		args = []any{}
	}
	if options == nil {
		options = map[string]any{}
	}
	return rpc(id, "tools/call", map[string]any{
		"name":      name,
		"arguments": map[string]any{"args": args, "options": options},
	})
}

func result(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in response: %v", resp)
	}
	return res
}

func toolNames(res map[string]any) map[string]bool {
	out := map[string]bool{}
	tools, _ := res["tools"].([]any)
	for _, tl := range tools {
		if m, ok := tl.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				out[n] = true
			}
		}
	}
	return out
}

func records(t *testing.T, resp map[string]any) []any {
	t.Helper()
	res := result(t, resp)
	sc, ok := res["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("no structuredContent: %v", res)
	}
	recs, _ := sc["records"].([]any)
	return recs
}

func fixableBy(res map[string]any) string {
	sc, _ := res["structuredContent"].(map[string]any)
	e, _ := sc["error"].(map[string]any)
	s, _ := e["fixable_by"].(string)
	return s
}
