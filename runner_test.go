package agentmcp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shhac/lib-agent-mcp/oauth"
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

// echoFixture writes a script that prints its argv and the given env var, so
// run()-level tests can observe exactly what the subprocess received.
func echoFixture(t *testing.T, envVar string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "echo.sh")
	body := "#!/bin/sh\necho \"$@\"\necho \"ENV=$" + envVar + "\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// A configured identity binding must shape every tool subprocess for the
// caller's principal: its argv (selector flags) and env (fail-closed gate).
func TestRunAppliesIdentityBindingForPrincipal(t *testing.T) {
	s := newServer(testRoot(),
		WithExecutable(echoFixture(t, "AGENT_TEST_REQUIRE")),
		WithIdentityBinding(func(p oauth.Verified) (argv, env []string) {
			return []string{"--workspace", "ws-" + p.ClientID},
				[]string{"AGENT_TEST_REQUIRE=1"}
		}))
	tool := &Tool{path: []string{"item", "list"}}

	ctx := oauth.WithPrincipal(context.Background(), oauth.Verified{ClientID: "alice"})
	res := s.run(ctx, tool, nil, nil, false)
	out := string(res.stdout)
	if !strings.Contains(out, "--workspace ws-alice") {
		t.Errorf("binding argv not applied:\n%s", out)
	}
	if !strings.Contains(out, "ENV=1") {
		t.Errorf("binding env not applied:\n%s", out)
	}
}

// Without a principal on the context (stdio, plain HTTP) the binding must not
// fire — the subprocess runs exactly as the operator's own invocation would.
func TestRunSkipsIdentityBindingWithoutPrincipal(t *testing.T) {
	called := false
	s := newServer(testRoot(),
		WithExecutable(echoFixture(t, "AGENT_TEST_REQUIRE")),
		WithIdentityBinding(func(p oauth.Verified) (argv, env []string) {
			called = true
			return []string{"--workspace", "nope"}, nil
		}))
	tool := &Tool{path: []string{"item", "list"}}

	res := s.run(context.Background(), tool, nil, nil, false)
	out := string(res.stdout)
	if called {
		t.Error("binding invoked without a principal")
	}
	if strings.Contains(out, "--workspace") {
		t.Errorf("binding argv leaked into an unbound call:\n%s", out)
	}
}
