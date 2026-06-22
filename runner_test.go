package agentmcp

import (
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
	tool := &Tool{path: []string{"item", "delete"}, injectConfirm: true}
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

func TestToArg(t *testing.T) {
	cases := map[any]string{
		"x":         "x",
		float64(42): "42",
		true:        "true",
	}
	for in, want := range cases {
		if got := toArg(in); got != want {
			t.Errorf("toArg(%v) = %q, want %q", in, got, want)
		}
	}
}
