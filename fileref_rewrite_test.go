package agentmcp

import (
	"strings"
	"testing"

	output "github.com/shhac/lib-agent-output"
)

func TestTranslateRewritesPathsUnderRoot(t *testing.T) {
	roots := []output.FileRoot{{Name: "cache", Path: "/home/u/.cache/app"}}
	stdout := []byte(`{"id":"F1","path":"/home/u/.cache/app/downloads/F1.png"}` + "\n")

	res := translate(runResult{stdout: stdout, exitCode: 0}, roots)

	rec, ok := res.StructuredContent.Records[0].(map[string]any)
	if !ok {
		t.Fatalf("record type = %T", res.StructuredContent.Records[0])
	}
	ref, ok := rec["path"].(output.FileRef)
	if !ok {
		t.Fatalf("path was not rewritten to a FileRef: %#v", rec["path"])
	}
	if ref.Root != "cache" || ref.Path != "downloads/F1.png" {
		t.Errorf("rewritten ref = %+v, want root cache path downloads/F1.png", ref)
	}
	// The host path must not survive in the text block either.
	if strings.Contains(res.Content[0].Text, "/home/u/.cache/app") {
		t.Errorf("text block still leaks host path: %q", res.Content[0].Text)
	}
}

func TestTranslateLeavesUnrelatedPaths(t *testing.T) {
	roots := []output.FileRoot{{Name: "cache", Path: "/home/u/.cache/app"}}
	stdout := []byte(`{"id":"F1","note":"/etc/hosts","n":3}` + "\n")

	res := translate(runResult{stdout: stdout, exitCode: 0}, roots)
	rec := res.StructuredContent.Records[0].(map[string]any)
	if _, isRef := rec["note"].(output.FileRef); isRef {
		t.Error("a path outside every root should not be rewritten")
	}
	// No root path in stdout → text block kept verbatim.
	if res.Content[0].Text != string(stdout) {
		t.Errorf("text block = %q, want verbatim stdout", res.Content[0].Text)
	}
}

func TestTranslateRewritesNestedPaths(t *testing.T) {
	roots := []output.FileRoot{{Name: "cache", Path: "/home/u/.cache/app"}}
	stdout := []byte(`{"id":"X","result":{"path":"/home/u/.cache/app/a.png"},` +
		`"files":["/home/u/.cache/app/b.png","keep-me"]}` + "\n")

	res := translate(runResult{stdout: stdout, exitCode: 0}, roots)
	rec := res.StructuredContent.Records[0].(map[string]any)

	nested := rec["result"].(map[string]any)
	if ref, ok := nested["path"].(output.FileRef); !ok || ref.Path != "a.png" {
		t.Errorf("nested object path not rewritten: %#v", nested["path"])
	}
	arr := rec["files"].([]any)
	if ref, ok := arr[0].(output.FileRef); !ok || ref.Path != "b.png" {
		t.Errorf("array element path not rewritten: %#v", arr[0])
	}
	if arr[1] != "keep-me" {
		t.Errorf("non-path array element changed: %#v", arr[1])
	}
}

func TestTranslateMetaPreservedDuringScrub(t *testing.T) {
	roots := []output.FileRoot{{Name: "cache", Path: "/home/u/.cache/app"}}
	stdout := []byte(`{"id":"F1","path":"/home/u/.cache/app/F1.png"}` + "\n" +
		`{"@pagination":{"has_more":true}}` + "\n")

	res := translate(runResult{stdout: stdout, exitCode: 0}, roots)
	if res.StructuredContent.Meta["@pagination"] == nil {
		t.Error("@pagination metadata lost")
	}
	if !strings.Contains(res.Content[0].Text, "@pagination") {
		t.Error("@pagination line dropped from rebuilt text block")
	}
	if len(res.StructuredContent.Records) != 1 {
		t.Errorf("records = %d, want 1 (meta line is not a record)", len(res.StructuredContent.Records))
	}
}

func TestTranslateMultipleRecordsMixed(t *testing.T) {
	roots := []output.FileRoot{{Name: "cache", Path: "/home/u/.cache/app"}}
	stdout := []byte(`{"id":"A","path":"/home/u/.cache/app/A.png"}` + "\n" +
		`{"id":"B","note":"no path here"}` + "\n")

	res := translate(runResult{stdout: stdout, exitCode: 0}, roots)
	if len(res.StructuredContent.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(res.StructuredContent.Records))
	}
	a := res.StructuredContent.Records[0].(map[string]any)
	if _, ok := a["path"].(output.FileRef); !ok {
		t.Error("first record path not rewritten")
	}
	b := res.StructuredContent.Records[1].(map[string]any)
	if b["note"] != "no path here" {
		t.Errorf("second record altered: %#v", b)
	}
}

func TestTranslateNoRootsIsNoop(t *testing.T) {
	stdout := []byte(`{"path":"/home/u/.cache/app/downloads/F1.png"}` + "\n")
	res := translate(runResult{stdout: stdout, exitCode: 0}, nil)
	rec := res.StructuredContent.Records[0].(map[string]any)
	if _, isRef := rec["path"].(output.FileRef); isRef {
		t.Error("with no roots configured, paths must be left untouched")
	}
}
