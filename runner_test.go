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
