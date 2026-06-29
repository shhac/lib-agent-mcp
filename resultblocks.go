package agentmcp

import output "github.com/shhac/lib-agent-output"

// toolResult is the MCP tools/call result envelope. Content is always present;
// StructuredContent is omitted for plain-text results (e.g. group help). IsError
// is always emitted, even when false.
type toolResult struct {
	Content           []contentBlock     `json:"content"`
	StructuredContent *structuredContent `json:"structuredContent,omitempty"`
	IsError           bool               `json:"isError"`
}

// contentBlock is one MCP content item. The cobra-exec path produces only text
// blocks; the native file tool also produces image/audio/resource blocks (see
// the constructors below), which is why every field beyond Type is optional.
type contentBlock struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	Data     string            `json:"data,omitempty"`     // base64 payload for image/audio
	MimeType string            `json:"mimeType,omitempty"` // for image/audio blocks
	Resource *embeddedResource `json:"resource,omitempty"` // type:resource payload
}

// embeddedResource is the body of a type:resource content block — an embedded
// resource carrying binary (Blob, base64) or text contents addressed by URI.
type embeddedResource struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Blob     string `json:"blob,omitempty"`
	Text     string `json:"text,omitempty"`
}

// structuredContent carries the parsed NDJSON contract: records always (possibly
// empty), with meta and error included only when present.
type structuredContent struct {
	Records []any          `json:"records"`
	Meta    map[string]any `json:"meta,omitempty"`
	Error   map[string]any `json:"error,omitempty"`
}

// textBlock is the text content block constructor.
func textBlock(text string) contentBlock {
	return contentBlock{Type: "text", Text: text}
}

// nativeError renders a Go error as a tool result matching the bridge's error
// contract: a {error, fixable_by, hint} structured error plus a text block. It
// is the native-tool counterpart to the subprocess error path — any in-process
// tool surfaces failures through it, so the contract stays uniform.
func nativeError(err error) toolResult {
	errObj := map[string]any{"error": err.Error(), "fixable_by": string(output.FixableByAgent)}
	msg := err.Error()
	var e *output.Error
	if output.As(err, &e) {
		errObj["error"] = e.Message
		errObj["fixable_by"] = string(e.FixableBy)
		msg = e.Message
		if e.Hint != "" {
			errObj["hint"] = e.Hint
			msg += "\nhint: " + e.Hint
		}
	}
	return toolResult{
		Content:           []contentBlock{textBlock(msg)},
		StructuredContent: &structuredContent{Records: []any{}, Error: errObj},
		IsError:           true,
	}
}

// imageBlock returns an image content block carrying base64 data — the form a
// host injects into the model's vision context.
func imageBlock(base64Data, mimeType string) contentBlock {
	return contentBlock{Type: "image", Data: base64Data, MimeType: mimeType}
}

// audioBlock returns an audio content block carrying base64 data.
func audioBlock(base64Data, mimeType string) contentBlock {
	return contentBlock{Type: "audio", Data: base64Data, MimeType: mimeType}
}

// resourceBlock returns an embedded-resource content block carrying binary data
// as a base64 blob addressed by uri — for binary the host can't render inline
// but should still receive over the protocol.
func resourceBlock(uri, mimeType, base64Blob string) contentBlock {
	return contentBlock{Type: "resource", Resource: &embeddedResource{URI: uri, MimeType: mimeType, Blob: base64Blob}}
}
