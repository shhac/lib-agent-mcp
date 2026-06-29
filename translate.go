package agentmcp

import (
	"bytes"
	"encoding/json"
	"strings"

	output "github.com/shhac/lib-agent-output"
)

// translate maps a subprocess result onto an MCP tools/call result, applying
// the lib-agent-output NDJSON contract:
//
//   - stdout: bare JSON objects become records; a single @-prefixed key is
//     metadata (e.g. @pagination). The raw stdout is also kept as a text block
//     so clients that ignore structuredContent still see the output.
//   - non-zero exit: isError, with the structured {error, fixable_by, hint}
//     from stderr surfaced for the calling agent.
func translate(r runResult, roots []output.FileRoot) toolResult {
	// When a root path appears in stdout, the text block is rebuilt from the
	// (rewritten) lines so it carries no host path either; otherwise the raw
	// stdout is kept verbatim for fidelity.
	scrub := r.exitCode == 0 && stdoutHasRootPath(r.stdout, roots)
	records, meta, scrubbed := processStdout(r.stdout, roots, scrub)

	structured := &structuredContent{Records: records, Meta: meta}
	result := toolResult{StructuredContent: structured, IsError: r.exitCode != 0}

	if r.exitCode != 0 {
		errObj := parseError(r.stderr)
		structured.Error = errObj // nil → omitted
		result.Content = []contentBlock{textBlock(errorText(errObj, r.stderr))}
		return result
	}

	text := string(r.stdout)
	if scrub {
		text = scrubbed
	}
	result.Content = []contentBlock{textBlock(text)}
	if notices := strings.TrimSpace(string(r.stderr)); notices != "" {
		result.Content = append(result.Content, textBlock(notices))
	}
	return result
}

// processStdout parses NDJSON stdout into structured records and @-metadata,
// rewriting any in-root file path to a FileRef atom. When scrub is true it also
// returns stdout re-rendered with those paths scrubbed, for a host-free text
// block; otherwise the caller keeps the raw stdout. Keeping the parse loop here
// leaves translate a clean parse → build-result flow.
func processStdout(stdout []byte, roots []output.FileRoot, scrub bool) (records []any, meta map[string]any, scrubbed string) {
	records = []any{}
	var lines [][]byte
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		var obj map[string]any
		if json.Unmarshal(trimmed, &obj) != nil {
			if scrub {
				lines = append(lines, line) // non-JSON line kept verbatim
			}
			continue
		}
		if key, value, ok := singleMetaKey(obj); ok {
			if meta == nil {
				meta = map[string]any{}
			}
			meta[key] = value
			if scrub {
				lines = append(lines, line)
			}
			continue
		}
		record := rewriteFileRefs(obj, roots)
		records = append(records, record)
		if scrub {
			if b, err := json.Marshal(record); err == nil {
				lines = append(lines, b)
			} else {
				lines = append(lines, line)
			}
		}
	}
	if scrub {
		scrubbed = string(bytes.Join(lines, []byte("\n")))
	}
	return records, meta, scrubbed
}

func singleMetaKey(obj map[string]any) (string, any, bool) {
	if len(obj) != 1 {
		return "", nil, false
	}
	for k, v := range obj {
		if strings.HasPrefix(k, "@") {
			return k, v, true
		}
	}
	return "", nil, false
}

// parseError returns the last stderr line that parses as a JSON object with an
// "error" field, matching the lib-agent-output error contract.
func parseError(stderr []byte) map[string]any {
	lines := bytes.Split(stderr, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := bytes.TrimSpace(lines[i])
		if len(trimmed) == 0 {
			continue
		}
		var obj map[string]any
		if json.Unmarshal(trimmed, &obj) == nil {
			if _, ok := obj["error"]; ok {
				return obj
			}
		}
	}
	return nil
}

func errorText(errObj map[string]any, stderr []byte) string {
	if errObj == nil {
		return strings.TrimSpace(string(stderr))
	}
	msg, _ := errObj["error"].(string)
	if hint, ok := errObj["hint"].(string); ok && hint != "" {
		return msg + "\nhint: " + hint
	}
	return msg
}
