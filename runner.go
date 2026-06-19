package agentmcp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

type runResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

// run executes the tool's command as a subprocess of the same binary, forcing
// NDJSON output and injecting --yes for gated (destructive) commands, since
// confirmation has already happened at the MCP host layer.
func (s *Server) run(ctx context.Context, tool *Tool, args []string, opts map[string]any) runResult {
	argv := append([]string{}, tool.path...)
	for name, v := range opts {
		argv = append(argv, renderFlag(name, v)...)
	}
	argv = append(argv, args...)
	if tool.gated {
		argv = append(argv, "--yes")
	}
	argv = append(argv, "--format", "jsonl")

	cmd := exec.CommandContext(ctx, s.executable(), argv...)
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
