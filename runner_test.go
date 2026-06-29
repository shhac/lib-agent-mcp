package agentmcp

import (
	"context"
	"reflect"
	"testing"
)

func TestRenderFlag(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want []string
	}{
		{"verbose", true, []string{"--verbose=true"}},
		{"verbose", false, []string{"--verbose=false"}},
		{"limit", float64(5), []string{"--limit=5"}}, // JSON numbers decode to float64
		{"score", float64(7.5), []string{"--score=7.5"}},
		{"status", "active", []string{"--status=active"}},
		{"tag", []any{"a", "b"}, []string{"--tag=a", "--tag=b"}}, // slices repeat
		{"missing", nil, nil},
	}
	for _, c := range cases {
		got := renderFlag(c.name, c.val)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("renderFlag(%q, %v) = %v, want %v", c.name, c.val, got, c.want)
		}
	}
}

func TestBuildArgv(t *testing.T) {
	tool := &Tool{path: []string{"item", "delete"}}
	got := buildArgv(tool, []string{"w-1"}, map[string]any{"force": true, "limit": float64(5)}, true)
	want := []string{"item", "delete", "--force=true", "--limit=5", "w-1", "--yes", "--format", "jsonl"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildArgv = %v, want %v", got, want)
	}
}

func TestBuildArgvNoOptionsNoConfirm(t *testing.T) {
	tool := &Tool{path: []string{"item", "list"}}
	got := buildArgv(tool, nil, nil, false)
	want := []string{"item", "list", "--format", "jsonl"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildArgv = %v, want %v", got, want)
	}
}

// TestBuildArgvGroupTool — a group tool's path is the group only; the subcommand
// name and its args ride in args, so the subcommand must land immediately after
// the group path (before any injected --yes and the forced --format).
func TestBuildArgvGroupTool(t *testing.T) {
	tool := &Tool{path: []string{"item"}, group: true}
	got := buildArgv(tool, []string{"delete", "w-1"}, nil, true)
	want := []string{"item", "delete", "w-1", "--yes", "--format", "jsonl"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildArgv group = %v, want %v", got, want)
	}
}

// TestRunStartFailureDegrades — when the executable can't start, run reports
// exitCode -1 with the error on stderr, and translate degrades that (non-JSON
// stderr) into an isError result with a text block rather than panicking.
func TestRunStartFailureDegrades(t *testing.T) {
	s := newServer(testRoot(), WithExecutable("/no/such/binary-xyz"))
	res := s.run(context.Background(), &Tool{path: []string{"item", "list"}}, nil, nil, false)
	if res.exitCode != -1 {
		t.Errorf("start-failure exitCode = %d, want -1", res.exitCode)
	}
	if len(res.stderr) == 0 {
		t.Error("start failure should surface an error on stderr")
	}
	out := translate(res, nil)
	if !out.IsError {
		t.Error("translate of a failed run should be isError")
	}
	if len(out.Content) == 0 {
		t.Error("translate should still emit a text content block on start failure")
	}
}
