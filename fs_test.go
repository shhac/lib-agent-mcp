package agentmcp

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	output "github.com/shhac/lib-agent-output"
)

// fsServer builds a server whose only tool is the file tool over a temp root
// seeded with a small tree. WithExecutable guards against any accidental exec.
func fsServer(t *testing.T, opts ...Option) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.png"), pngBytes())
	writeFile(t, filepath.Join(dir, "notes.txt"), []byte("hello world"))
	writeFile(t, filepath.Join(dir, "sub", "c.png"), pngBytes())
	writeFile(t, filepath.Join(dir, "data.bin"), []byte{0x00, 0x01, 0x02, 0x03, 0xFF})

	base := []Option{WithExecutable("/nonexistent/must-not-exec"), WithFileRoots(output.FileRoot{Name: "cache", Path: dir})}
	s := newServer(testRoot(), append(base, opts...)...)
	return s, dir
}

func callFs(s *Server, args ...string) toolResult {
	return s.callTool(context.Background(), s.toolsByName[s.opts.fileToolName], args, nil)
}

func recordPaths(res toolResult) map[string]string {
	out := map[string]string{}
	if res.StructuredContent == nil {
		return out
	}
	for _, r := range res.StructuredContent.Records {
		if ref, ok := r.(output.FileRef); ok {
			out[ref.Path] = ref.MimeType
		}
	}
	return out
}

func TestFileToolAbsentWithoutRoots(t *testing.T) {
	s := newServer(testRoot())
	if _, ok := s.toolsByName["fs"]; ok {
		t.Error("fs tool present without any registered roots")
	}
}

func TestFileToolPresentAndReadOnly(t *testing.T) {
	s, _ := fsServer(t)
	tool, ok := s.toolsByName["fs"]
	if !ok {
		t.Fatal("fs tool missing despite a registered root")
	}
	if tool.Annotations["readOnlyHint"] != true {
		t.Error("fs tool should carry readOnlyHint")
	}
}

func TestFileToolNameOverride(t *testing.T) {
	s, _ := fsServer(t, WithFileToolName("files"))
	if _, ok := s.toolsByName["files"]; !ok {
		t.Error("renamed file tool not found under override name")
	}
	if _, ok := s.toolsByName["fs"]; ok {
		t.Error("default name still present after override")
	}
}

func TestFsFindByExtension(t *testing.T) {
	s, _ := fsServer(t)
	res := callFs(s, "find", "cache", "-e", "png")
	paths := recordPaths(res)
	if _, ok := paths["a.png"]; !ok {
		t.Errorf("find missing a.png: %v", paths)
	}
	if _, ok := paths["sub/c.png"]; !ok {
		t.Errorf("find missing sub/c.png: %v", paths)
	}
	if _, ok := paths["notes.txt"]; ok {
		t.Error("find -e png should not include notes.txt")
	}
}

func TestFsListDirectory(t *testing.T) {
	s, _ := fsServer(t)
	paths := recordPaths(callFs(s, "ls", "cache"))
	if paths["sub"] != "inode/directory" {
		t.Errorf("ls should mark sub as a directory: %v", paths)
	}
	if _, ok := paths["a.png"]; !ok {
		t.Errorf("ls missing a.png: %v", paths)
	}
	// Listing a nested dir scopes correctly.
	nested := recordPaths(callFs(s, "ls", "cache", "sub"))
	if _, ok := nested["sub/c.png"]; !ok {
		t.Errorf("ls sub missing sub/c.png: %v", nested)
	}
}

func TestFsListSingleFile(t *testing.T) {
	s, _ := fsServer(t)
	paths := recordPaths(callFs(s, "ls", "cache", "notes.txt"))
	if len(paths) != 1 {
		t.Fatalf("ls of a file returned %d records, want 1: %v", len(paths), paths)
	}
	if _, ok := paths["notes.txt"]; !ok {
		t.Errorf("ls notes.txt missing the file: %v", paths)
	}
}

func TestFsGetImageInlines(t *testing.T) {
	s, _ := fsServer(t)
	res := callFs(s, "get", "cache", "a.png")
	if res.IsError || len(res.Content) == 0 {
		t.Fatalf("get a.png errored: %+v", res)
	}
	b := res.Content[0]
	if b.Type != "image" || b.MimeType != "image/png" || b.Data == "" {
		t.Errorf("expected base64 image/png block, got %+v", b)
	}
}

func TestFsGetTextVerbatim(t *testing.T) {
	s, _ := fsServer(t)
	res := callFs(s, "get", "cache", "notes.txt")
	if res.IsError || res.Content[0].Type != "text" || res.Content[0].Text != "hello world" {
		t.Errorf("expected verbatim text block, got %+v", res.Content)
	}
}

func TestFsGetOverInlineLimitErrors(t *testing.T) {
	s, _ := fsServer(t, WithFileInlineLimit(4))
	res := callFs(s, "get", "cache", "notes.txt") // 11 bytes > 4
	if !res.IsError {
		t.Fatal("get over inline limit should error")
	}
	if fb, _ := res.StructuredContent.Error["fixable_by"].(string); fb != string(output.FixableByHuman) {
		t.Errorf("over-limit error fixable_by = %q, want human", fb)
	}
}

func TestFsGetEscapeRejected(t *testing.T) {
	s, _ := fsServer(t)
	res := callFs(s, "get", "cache", "../escape.txt")
	if !res.IsError {
		t.Error("get with .. escape should error")
	}
}

func TestFsUnknownRootAndVerb(t *testing.T) {
	s, _ := fsServer(t)
	if res := callFs(s, "get", "nope", "x"); !res.IsError {
		t.Error("unknown root should error")
	}
	// Unknown verb falls back to help (not an error result).
	if res := callFs(s, "frobnicate"); res.IsError || len(res.Content) == 0 {
		t.Error("unknown verb should return help text")
	}
	if res := callFs(s, "help"); res.IsError || res.Content[0].Text == "" {
		t.Error("help verb should return usage")
	}
}

func TestFsFindWithGlob(t *testing.T) {
	s, _ := fsServer(t)
	// Basename glob matches both pngs regardless of directory.
	star := recordPaths(callFs(s, "find", "cache", "*.png"))
	if _, ok := star["a.png"]; !ok {
		t.Errorf("glob *.png missing a.png: %v", star)
	}
	if _, ok := star["sub/c.png"]; !ok {
		t.Errorf("glob *.png missing sub/c.png: %v", star)
	}
	// Path glob scopes to a subdirectory via the full relative path.
	scoped := recordPaths(callFs(s, "find", "cache", "sub/*"))
	if _, ok := scoped["sub/c.png"]; !ok {
		t.Errorf("glob sub/* missing sub/c.png: %v", scoped)
	}
	if _, ok := scoped["a.png"]; ok {
		t.Errorf("glob sub/* should not match top-level a.png: %v", scoped)
	}
}

func TestFsFindMultipleExtensions(t *testing.T) {
	s, _ := fsServer(t)
	// Both spellings: repeated -e and the -e=ext form.
	paths := recordPaths(callFs(s, "find", "cache", "-e", "png", "-e", "txt"))
	for _, want := range []string{"a.png", "sub/c.png", "notes.txt"} {
		if _, ok := paths[want]; !ok {
			t.Errorf("multi -e missing %s: %v", want, paths)
		}
	}
	if _, ok := paths["data.bin"]; ok {
		t.Error("multi -e png/txt should not include data.bin")
	}

	eq := recordPaths(callFs(s, "find", "cache", "-e=png"))
	if _, ok := eq["a.png"]; !ok {
		t.Errorf("-e=png missing a.png: %v", eq)
	}
}

func TestFsFindTruncates(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i <= maxFindResults; i++ { // one more than the cap
		writeFile(t, filepath.Join(dir, "f"+strconv.Itoa(i)+".dat"), []byte("x"))
	}
	s := newServer(testRoot(), WithExecutable("/nonexistent/must-not-exec"),
		WithFileRoots(output.FileRoot{Name: "big", Path: dir}))
	res := callFs(s, "find", "big", "-e", "dat")
	if len(res.StructuredContent.Records) != maxFindResults {
		t.Errorf("records = %d, want cap %d", len(res.StructuredContent.Records), maxFindResults)
	}
	if res.StructuredContent.Meta["@truncated"] != true {
		t.Errorf("expected @truncated meta, got %v", res.StructuredContent.Meta)
	}
	if !strings.Contains(res.Content[0].Text, "truncated") {
		t.Error("text block should mention truncation")
	}
}

func TestFsFindNoMatches(t *testing.T) {
	s, _ := fsServer(t)
	res := callFs(s, "find", "cache", "-e", "xyz")
	if res.IsError {
		t.Fatalf("find with no matches should not error: %+v", res)
	}
	if len(res.StructuredContent.Records) != 0 {
		t.Errorf("expected zero records, got %d", len(res.StructuredContent.Records))
	}
	if _, ok := res.StructuredContent.Meta["@truncated"]; ok {
		t.Error("@truncated should be absent for a small result set")
	}
	if res.Content[0].Text != "(no files)" {
		t.Errorf("empty find text = %q, want (no files)", res.Content[0].Text)
	}
}

func TestFsGetBinaryAsResource(t *testing.T) {
	s, _ := fsServer(t)
	res := callFs(s, "get", "cache", "data.bin")
	if res.IsError || len(res.Content) == 0 {
		t.Fatalf("get data.bin errored: %+v", res)
	}
	b := res.Content[0]
	if b.Type != "resource" || b.Resource == nil {
		t.Fatalf("expected resource block, got %+v", b)
	}
	if b.Resource.URI != "agent-file://cache/data.bin" || b.Resource.Blob == "" {
		t.Errorf("resource block = %+v, want agent-file URI + base64 blob", b.Resource)
	}
}

func TestFsGetDirectoryErrors(t *testing.T) {
	s, _ := fsServer(t)
	res := callFs(s, "get", "cache", "sub")
	if !res.IsError {
		t.Fatal("get on a directory should error")
	}
	if !strings.Contains(res.Content[0].Text, "is a directory") {
		t.Errorf("error text = %q, want mention of directory", res.Content[0].Text)
	}
}

func TestFsFindUnknownFlag(t *testing.T) {
	s, _ := fsServer(t)
	res := callFs(s, "find", "cache", "--unknown")
	if !res.IsError {
		t.Fatal("unknown find flag should error")
	}
	if !strings.Contains(res.Content[0].Text, "unknown find flag") {
		t.Errorf("error text = %q, want 'unknown find flag'", res.Content[0].Text)
	}
}

func TestFsListedInToolsList(t *testing.T) {
	s, _ := fsServer(t)
	found := false
	for _, tool := range s.tools {
		if tool.Name == "fs" {
			found = true
		}
	}
	if !found {
		t.Error("fs tool not present in the tool list")
	}
}

func pngBytes() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01}
}

func writeFile(t *testing.T, p string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
}
