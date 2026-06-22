package agentmcp

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// widgetSession is a running `widget mcp` subprocess with framed send/recv.
type widgetSession struct {
	send func(any)
	recv func() map[string]any
}

// startWidget builds the widget example and starts `widget mcp` with the given
// extra environment, returning a framed JSON-RPC session. Cleanup is registered
// on t. It is the shared harness for both the legacy and exposed e2e tests.
func startWidget(t *testing.T, env ...string) *widgetSession {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping e2e build in -short")
	}

	bin := filepath.Join(t.TempDir(), "widget")
	if out, err := exec.Command("go", "build", "-o", bin, "./examples/widget").CombinedOutput(); err != nil {
		t.Fatalf("build widget: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "mcp")
	cmd.Env = append(os.Environ(), env...)
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
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

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
	return &widgetSession{send: send, recv: recv}
}

// initialize drives the opening handshake and returns the initialize result.
func (s *widgetSession) initialize(t *testing.T) map[string]any {
	t.Helper()
	s.send(rpc(1, "initialize", map[string]any{"protocolVersion": "2025-06-18"}))
	res := result(t, s.recv())
	s.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return res
}

// TestEndToEnd builds the widget example, runs `widget mcp`, and drives a real
// MCP stdio handshake against it — proving schema generation, the protocol
// loop, subprocess self-exec, and NDJSON translation together (legacy mode:
// one tool per leaf command).
func TestEndToEnd(t *testing.T) {
	s := startWidget(t)

	si := s.initialize(t)["serverInfo"].(map[string]any)
	if si["name"] != "widget" {
		t.Errorf("serverInfo.name = %v, want widget", si["name"])
	}

	s.send(rpc(2, "tools/list", nil))
	tools := toolNames(result(t, s.recv()))
	for _, want := range []string{"item_list", "item_get", "item_search", "item_delete", "config_get", "config_set", "config_reset"} {
		if !tools[want] {
			t.Errorf("tools/list missing %q; have %v", want, tools)
		}
	}
	for _, gone := range []string{"item", "config", "admin", "admin_secret"} {
		if tools[gone] {
			t.Errorf("tools/list should not contain %q", gone)
		}
	}

	s.send(call(3, "item_list", nil, nil))
	if got := len(records(t, s.recv())); got != 4 {
		t.Errorf("item_list returned %d records, want 4", got)
	}

	s.send(call(4, "item_get", []any{"w-2"}, nil))
	recs := records(t, s.recv())
	if len(recs) != 1 || recs[0].(map[string]any)["id"] != "w-2" {
		t.Errorf("item_get w-2 = %v", recs)
	}

	s.send(call(5, "item_get", []any{"nope"}, nil))
	missing := result(t, s.recv())
	if missing["isError"] != true {
		t.Errorf("missing item_get should be isError, got %v", missing["isError"])
	}
	if fb := fixableBy(missing); fb != "agent" {
		t.Errorf("missing item_get fixable_by = %q, want agent", fb)
	}

	// --yes flag present: the bridge injects --yes, so the delete succeeds
	// rather than dead-ending on the confirmation gate.
	s.send(call(6, "item_delete", []any{"w-1"}, nil))
	del := records(t, s.recv())
	if len(del) != 1 || del[0].(map[string]any)["deleted"] != "w-1" {
		t.Errorf("item_delete result = %v", del)
	}

	// mcp.destructive WITHOUT a --yes flag: the bridge must NOT inject --yes
	// (cobra would reject an unknown flag). The call should still succeed.
	s.send(call(7, "config_set", []any{"theme", "light"}, nil))
	set := records(t, s.recv())
	if len(set) != 1 || set[0].(map[string]any)["set"] != "theme" {
		t.Errorf("config_set result = %v", set)
	}

	// --yes flag present: injected, so reset runs.
	s.send(call(8, "config_reset", nil, nil))
	reset := records(t, s.recv())
	if len(reset) != 1 || reset[0].(map[string]any)["reset"] != true {
		t.Errorf("config_reset result = %v", reset)
	}
}

// TestEndToEndExposed drives the opt-in group-tool path end-to-end (WIDGET_EXPOSE
// makes item/config coarse group tools): the help verb, the empty/unknown
// fallback to help, real subcommand dispatch through cobra Find, and — the part
// the legacy test can't reach — per-subcommand --yes injection decided inside
// callGroup against a real subprocess.
func TestEndToEndExposed(t *testing.T) {
	s := startWidget(t, "WIDGET_EXPOSE=1")
	s.initialize(t)

	// Only the two coarse group tools surface — no leaves, no skip'd admin.
	s.send(rpc(2, "tools/list", nil))
	listRes := result(t, s.recv())
	tools := toolNames(listRes)
	for _, want := range []string{"item", "config"} {
		if !tools[want] {
			t.Errorf("tools/list missing group tool %q; have %v", want, tools)
		}
	}
	for _, gone := range []string{"item_list", "item_get", "item_delete", "config_get", "config_set", "admin", "admin_secret"} {
		if tools[gone] {
			t.Errorf("tools/list should not contain %q in exposed mode", gone)
		}
	}
	// Both groups carry destructiveHint: item via delete's --yes, config via
	// set's mcp.destructive annotation.
	if !hasDestructiveHint(listRes, "item") {
		t.Error("item group tool should carry destructiveHint (delete defines --yes)")
	}
	if !hasDestructiveHint(listRes, "config") {
		t.Error("config group tool should carry destructiveHint (set is mcp.destructive)")
	}

	// help verb: lists subcommands, not an error.
	s.send(call(3, "item", []any{"help"}, nil))
	help := result(t, s.recv())
	if help["isError"] == true {
		t.Error("help verb should not be an error")
	}
	for _, want := range []string{"list", "get", "delete"} {
		if ht := resultText(t, help); !strings.Contains(ht, want) {
			t.Errorf("item help missing %q:\n%s", want, ht)
		}
	}

	// Empty args fall back to help, not a subprocess error.
	s.send(call(4, "item", nil, nil))
	if result(t, s.recv())["isError"] == true {
		t.Error("empty args should fall back to help, not error")
	}

	// Unknown subcommand falls back to help and must never exec.
	s.send(call(5, "item", []any{"bogus"}, nil))
	bogus := result(t, s.recv())
	if bogus["isError"] == true {
		t.Error("unknown subcommand should fall back to help, not error")
	}
	if bt := resultText(t, bogus); !strings.Contains(bt, "unknown subcommand") {
		t.Errorf("expected unknown-subcommand notice, got:\n%s", bt)
	}

	// Real read dispatch through the group: `item get w-2`.
	s.send(call(6, "item", []any{"get", "w-2"}, nil))
	recs := records(t, s.recv())
	if len(recs) != 1 || recs[0].(map[string]any)["id"] != "w-2" {
		t.Errorf("item get w-2 via group = %v", recs)
	}

	// `item list` through the group → all 4 records.
	s.send(call(7, "item", []any{"list"}, nil))
	if got := len(records(t, s.recv())); got != 4 {
		t.Errorf("item list via group returned %d records, want 4", got)
	}

	// Destructive subcommand WITH --yes: callGroup injects --yes for this
	// specific subcommand, so the delete runs instead of hitting the gate.
	s.send(call(8, "item", []any{"delete", "w-1"}, nil))
	del := records(t, s.recv())
	if len(del) != 1 || del[0].(map[string]any)["deleted"] != "w-1" {
		t.Errorf("item delete via group = %v", del)
	}

	// Destructive subcommand WITHOUT a --yes flag (config set is mcp.destructive
	// only): callGroup must NOT inject --yes, or cobra would reject it. Succeeds.
	s.send(call(9, "config", []any{"set", "theme", "light"}, nil))
	set := records(t, s.recv())
	if len(set) != 1 || set[0].(map[string]any)["set"] != "theme" {
		t.Errorf("config set via group = %v", set)
	}

	// config reset defines --yes → injected → runs.
	s.send(call(10, "config", []any{"reset"}, nil))
	reset := records(t, s.recv())
	if len(reset) != 1 || reset[0].(map[string]any)["reset"] != true {
		t.Errorf("config reset via group = %v", reset)
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

// hasDestructiveHint reports whether the named tool in a tools/list result
// carries annotations.destructiveHint == true.
func hasDestructiveHint(listRes map[string]any, name string) bool {
	tools, _ := listRes["tools"].([]any)
	for _, tl := range tools {
		m, _ := tl.(map[string]any)
		if m["name"] != name {
			continue
		}
		ann, _ := m["annotations"].(map[string]any)
		return ann["destructiveHint"] == true
	}
	return false
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

// textContent returns the first text content block of a tools/call result.
func resultText(t *testing.T, res map[string]any) string {
	t.Helper()
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content blocks in result: %v", res)
	}
	block, _ := content[0].(map[string]any)
	s, _ := block["text"].(string)
	return s
}

func fixableBy(res map[string]any) string {
	sc, _ := res["structuredContent"].(map[string]any)
	e, _ := sc["error"].(map[string]any)
	s, _ := e["fixable_by"].(string)
	return s
}
