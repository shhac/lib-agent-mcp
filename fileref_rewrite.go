package agentmcp

import (
	"bytes"

	output "github.com/shhac/lib-agent-output"
)

// rewriteFileRefs replaces, in place, any string value in a decoded record that
// is an absolute host path under a configured root with a FileRef atom — so a
// tool that printed a raw download path (e.g. {"path":"/…/cache/downloads/F1.png"})
// yields a host-free, fetchable {"path":{"@type":"file","root":"cache",…}} the
// agent can hand straight to the file tool's get verb. It recurses through maps
// and slices. With no roots it is a no-op, so non-file CLIs pay nothing.
func rewriteFileRefs(v any, roots []output.FileRoot) any {
	if len(roots) == 0 {
		return v
	}
	switch t := v.(type) {
	case string:
		if ref, ok := output.FileRefFor(roots, t); ok {
			return ref
		}
		return t
	case map[string]any:
		for k, val := range t {
			t[k] = rewriteFileRefs(val, roots)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = rewriteFileRefs(val, roots)
		}
		return t
	default:
		return v
	}
}

// stdoutHasRootPath reports whether any root's host path appears in stdout — a
// cheap gate so the text block is only re-serialized (losing exact formatting)
// for output that actually carries a host path to scrub.
func stdoutHasRootPath(stdout []byte, roots []output.FileRoot) bool {
	for _, r := range roots {
		if r.Path != "" && bytes.Contains(stdout, []byte(r.Path)) {
			return true
		}
	}
	return false
}
