package agentmcp

import (
	"encoding/base64"
	"strings"
)

// fileContentBlock picks the content-block shape for a file by MIME type:
// images/audio inline as their typed blocks, text returns verbatim, everything
// else rides as an embedded base64 resource addressed by a non-host URI.
func fileContentBlock(rootName, rel, mimeType string, data []byte) contentBlock {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return imageBlock(stdB64(data), mimeType)
	case strings.HasPrefix(mimeType, "audio/"):
		return audioBlock(stdB64(data), mimeType)
	case isTextMime(mimeType):
		return textBlock(string(data))
	default:
		return resourceBlock(fileURI(rootName, rel), mimeType, stdB64(data))
	}
}

// stdB64 base64-encodes a content payload — the form the image/audio/resource
// content blocks carry. The text branch deliberately skips it (no encode cost
// for large text files).
func stdB64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// isTextMime reports whether a MIME type should be returned as a text block
// rather than a base64 payload.
func isTextMime(mimeType string) bool {
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	switch {
	case strings.Contains(mimeType, "json"),
		strings.Contains(mimeType, "xml"),
		strings.Contains(mimeType, "javascript"):
		return true
	}
	return false
}

// fileURI is the non-host-revealing address for a file: agent-file://<root>/<path>.
func fileURI(rootName, rel string) string {
	return "agent-file://" + rootName + "/" + rel
}
