package agentmcp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
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
	cmd := exec.CommandContext(ctx, s.executable(), buildArgv(tool, args, opts, injectConfirm)...)
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
	for _, name := range sortedOptionKeys(opts) {
		argv = append(argv, renderFlag(name, opts[name])...)
	}
	argv = append(argv, args...)
	if injectConfirm {
		argv = append(argv, "--yes")
	}
	return append(argv, "--format", "jsonl")
}

func sortedOptionKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

func toArg(v any) string {
	return fmt.Sprintf("%v", v)
}
