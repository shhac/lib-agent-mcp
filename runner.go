package agentmcp

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"

	"github.com/shhac/lib-agent-mcp/oauth"
)

type runResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

// run executes the tool's command as a subprocess of the same binary, forcing
// NDJSON output and injecting --yes for gated (destructive) commands, since
// confirmation has already happened at the MCP host layer.
func (s *Server) run(ctx context.Context, tool *Tool, args []string, opts map[string]any, injectConfirm bool) runResult {
	argv := buildArgv(tool, args, opts, injectConfirm)
	extraArgv, extraEnv := s.identityInjection(ctx)
	argv = append(argv, extraArgv...)
	cmd := exec.CommandContext(ctx, s.executable(), argv...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb

	code := 0
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			// Failed to start / context cancelled: surface as a structured error.
			code = -1
			if errb.Len() == 0 {
				errb.WriteString(err.Error())
			}
		}
	}
	return runResult{stdout: out.Bytes(), stderr: errb.Bytes(), exitCode: code}
}

// boundPrincipal returns the named principal a call acts for, or false for
// operator calls — no principal on the context, or the anonymous zero grant
// (stdio, plain HTTP, the legacy shared pairing code). Every per-principal
// behavior (identity injection, file-root scoping) gates on this one helper.
func boundPrincipal(ctx context.Context) (oauth.Verified, bool) {
	p, ok := oauth.PrincipalFrom(ctx)
	if !ok || p.IsAnonymous() {
		return oauth.Verified{}, false
	}
	return p, true
}

// identityInjection resolves the configured identity binding against the
// caller's bound principal. No binding or an operator call means no
// injection: those run with the operator's own defaults, exactly as
// multi-user.md promises.
func (s *Server) identityInjection(ctx context.Context) (argv, env []string) {
	if s.opts.identityBinding == nil {
		return nil, nil
	}
	p, ok := boundPrincipal(ctx)
	if !ok {
		return nil, nil
	}
	return s.opts.identityBinding(p)
}

func (s *Server) executable() string {
	if s.opts.executable != "" {
		return s.opts.executable
	}
	if p, err := os.Executable(); err == nil {
		return p
	}
	return os.Args[0]
}

// buildArgv reconstructs the child process arguments for a tool call: the
// command path, then options (sorted for deterministic output), then positional
// args, then an injected --yes for confirm-gated commands, and finally
// --format jsonl to force the NDJSON contract.
func buildArgv(tool *Tool, args []string, opts map[string]any, injectConfirm bool) []string {
	argv := append([]string{}, tool.path...)
	for _, name := range slices.Sorted(maps.Keys(opts)) {
		argv = append(argv, renderFlag(name, opts[name])...)
	}
	argv = append(argv, args...)
	if injectConfirm {
		argv = append(argv, "--yes")
	}
	return append(argv, "--format", "jsonl")
}

func renderFlag(name string, v any) []string {
	switch val := v.(type) {
	case nil:
		return nil
	case bool:
		return []string{fmt.Sprintf("--%s=%t", name, val)}
	case []any:
		out := make([]string, 0, len(val))
		for _, e := range val {
			out = append(out, fmt.Sprintf("--%s=%v", name, e))
		}
		return out
	default:
		return []string{fmt.Sprintf("--%s=%v", name, val)}
	}
}
