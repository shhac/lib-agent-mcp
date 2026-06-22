package agentmcp

import (
	"bytes"
	"encoding/json"
	"strings"
)

// toolResult is the MCP tools/call result envelope. Content is always present;
// StructuredContent is omitted for plain-text results (e.g. group help). IsError
// is always emitted, even when false.
type toolResult struct {
	Content           []contentBlock     `json:"content"`
	StructuredContent *structuredContent `json:"structuredContent,omitempty"`
	IsError           bool               `json:"isError"`
}

// contentBlock is one MCP content item; only text blocks are produced here.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// structuredContent carries the parsed NDJSON contract: records always (possibly
// empty), with meta and error included only when present.
type structuredContent struct {
	Records []any          `json:"records"`
	Meta    map[string]any `json:"meta,omitempty"`
	Error   map[string]any `json:"error,omitempty"`
}

// textBlock is the single text content block constructor.
func textBlock(text string) contentBlock {
	return contentBlock{Type: "text", Text: text}
}

// translate maps a subprocess result onto an MCP tools/call result, applying
// the lib-agent-output NDJSON contract:
//
//   - stdout: bare JSON objects become records; a single @-prefixed key is
//     metadata (e.g. @pagination). The raw stdout is also kept as a text block
//     so clients that ignore structuredContent still see the output.
//   - non-zero exit: isError, with the structured {error, fixable_by, hint}
//     from stderr surfaced for the calling agent.
func translate(r runResult) toolResult {
	structured := &structuredContent{Records: []any{}}
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
			if structured.Meta == nil {
				structured.Meta = map[string]any{}
			}
			structured.Meta[key] = value
			continue
		}
		structured.Records = append(structured.Records, obj)
	}

	result := toolResult{
		StructuredContent: structured,
		IsError:           r.exitCode != 0,
	}

	if r.exitCode != 0 {
		errObj := parseError(r.stderr)
		structured.Error = errObj // nil → omitted
		result.Content = []contentBlock{textBlock(errorText(errObj, r.stderr))}
		return result
	}

	result.Content = []contentBlock{textBlock(string(r.stdout))}
	if notices := strings.TrimSpace(string(r.stderr)); notices != "" {
		result.Content = append(result.Content, textBlock(notices))
	}
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
