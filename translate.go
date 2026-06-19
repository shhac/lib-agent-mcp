package agentmcp

import (
	"bytes"
	"encoding/json"
	"strings"
)

// translate maps a subprocess result onto an MCP tools/call result, applying
// the lib-agent-output NDJSON contract:
//
//   - stdout: bare JSON objects become records; a single @-prefixed key is
//     metadata (e.g. @pagination). The raw stdout is also kept as a text block
//     so clients that ignore structuredContent still see the output.
//   - non-zero exit: isError, with the structured {error, fixable_by, hint}
//     from stderr surfaced for the calling agent.
func translate(r runResult) map[string]any {
	records := []any{}
	meta := map[string]any{}
	for _, line := range bytes.Split(r.stdout, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		var obj map[string]any
		if json.Unmarshal(trimmed, &obj) != nil {
			continue // non-JSON line: it still lives in the text block
		}
		if key, value, ok := singleMetaKey(obj); ok {
			meta[key] = value
			continue
		}
		records = append(records, obj)
	}

	structured := map[string]any{"records": records}
	if len(meta) > 0 {
		structured["meta"] = meta
	}

	result := map[string]any{
		"structuredContent": structured,
		"isError":           r.exitCode != 0,
	}

	if r.exitCode != 0 {
		errObj := parseError(r.stderr)
		if errObj != nil {
			structured["error"] = errObj
		}
		result["content"] = []any{textContent(errorText(errObj, r.stderr))}
		return result
	}

	content := []any{textContent(string(r.stdout))}
	if notices := strings.TrimSpace(string(r.stderr)); notices != "" {
		content = append(content, textContent(notices))
	}
	result["content"] = content
	return result
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

func textContent(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
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
