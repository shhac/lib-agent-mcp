package agentmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	output "github.com/shhac/lib-agent-output"
)

// maxFindResults caps a single find so a large tree can't flood the result; the
// cap is reported as @truncated metadata rather than silently dropping files.
const maxFindResults = 1000

// nativeTools returns the bridge's in-process tools. Today that is just the
// read-only file tool, present only when the CLI opted in with ≥1 root.
func (s *Server) nativeTools() []Tool {
	if len(s.opts.fileRoots) == 0 {
		return nil
	}
	return []Tool{s.fileTool()}
}

// fileTool describes the read-only file tool: a group-shaped native tool whose
// args carry a verb (find/ls/get) and a root name, addressing files relative to
// that root. It never exposes a host path.
func (s *Server) fileTool() Tool {
	roots := strings.Join(s.fileRootNames(), ", ")
	desc := fmt.Sprintf("Read-only access to local files under named roots (%s). "+
		`Verbs via args: ["find","<root>","-e","png"] searches a root, `+
		`["ls","<root>","<dir?>"] lists a directory, `+
		`["get","<root>","<path>"] returns a file's contents (images inline as image blocks). `+
		"Paths are relative to the root.", roots)

	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"args": argsArraySchema(`The verb and its arguments, e.g. ["find","cache","-e","png"], ` +
				`["ls","cache"], or ["get","cache","downloads/F1.png"]. Use ["help"] for usage.`),
		},
		"required": []string{"args"},
	}

	return Tool{
		Name:        s.opts.fileToolName,
		Description: desc,
		InputSchema: input,
		Annotations: map[string]any{"readOnlyHint": true},
		handler:     s.handleFileTool,
	}
}

// handleFileTool dispatches a file-tool call: resolve the verb, then the named
// root, then run the verb. Unknown verbs and a missing/unknown root return
// usage or a structured error rather than failing opaquely.
func (s *Server) handleFileTool(_ context.Context, args []string, _ map[string]any) toolResult {
	verb := ""
	if len(args) > 0 {
		verb = args[0]
	}
	if verb == "" || verb == "help" {
		return helpResult(s.fileToolHelp())
	}
	handle, ok := map[string]func(output.FileRoot, []string) toolResult{
		"find": s.fsFind,
		"ls":   s.fsList,
		"get":  s.fsGet,
	}[verb]
	if !ok {
		return helpResult(fmt.Sprintf("unknown verb %q\n\n%s", verb, s.fileToolHelp()))
	}

	rest := args[1:]
	if len(rest) == 0 {
		return nativeError(output.Newf(output.FixableByAgent,
			"%s needs a root name; one of: %s", verb, strings.Join(s.fileRootNames(), ", ")))
	}
	root, ok := s.fileRoot(rest[0])
	if !ok {
		return nativeError(output.Newf(output.FixableByAgent,
			"unknown root %q; one of: %s", rest[0], strings.Join(s.fileRootNames(), ", ")))
	}
	return handle(root, rest[1:])
}

// fsError wraps an OS filesystem error as an agent-fixable tool error — one
// definition of "a filesystem failure is the agent's to fix".
func fsError(err error) toolResult {
	return nativeError(output.Wrap(err, output.FixableByAgent))
}

// fsFind walks a root, returning FileRef records for regular files matching the
// extension filters and/or glob. Symlinks and special files are skipped so a
// listing never points outside the root.
func (s *Server) fsFind(root output.FileRoot, args []string) toolResult {
	exts, glob, err := parseFindArgs(args)
	if err != nil {
		return nativeError(err)
	}

	var refs []output.FileRef
	truncated := false
	walkErr := filepath.WalkDir(root.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil //nolint:nilerr // skip unreadable/non-regular entries, keep walking
		}
		rel, rerr := filepath.Rel(root.Path, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !matchFind(rel, exts, glob) {
			return nil
		}
		ref, ferr := output.FileRefAt(root.Name, rel, p)
		if ferr != nil {
			return nil
		}
		refs = append(refs, ref)
		if len(refs) >= maxFindResults {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return nativeError(output.Newf(output.FixableByAgent, "cannot search %q", root.Name).WithCause(walkErr))
	}
	return fsRecordsResult(refs, truncated)
}

// fsList lists one directory level of a root (default the root itself). Listing
// a file yields that single file's record.
func (s *Server) fsList(root output.FileRoot, args []string) toolResult {
	rel := "."
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		rel = args[0]
	}
	abs, err := output.SafeResolve(root, rel)
	if err != nil {
		return nativeError(err)
	}
	relSlash := path.Clean(filepath.ToSlash(rel))

	info, err := os.Stat(abs)
	if err != nil {
		return fsError(err)
	}
	if !info.IsDir() {
		ref, _ := output.FileRefAt(root.Name, relSlash, abs)
		return fsRecordsResult([]output.FileRef{ref}, false)
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return nativeError(output.Newf(output.FixableByAgent, "cannot list %q in %q", relSlash, root.Name).WithCause(err))
	}
	refs := make([]output.FileRef, 0, len(entries))
	for _, e := range entries {
		childRel := e.Name()
		if relSlash != "." {
			childRel = path.Join(relSlash, e.Name())
		}
		refs = append(refs, dirEntryRef(root.Name, childRel, filepath.Join(abs, e.Name()), e))
	}
	return fsRecordsResult(refs, false)
}

// fsGet returns one file's contents as the spec-idiomatic content block (image,
// audio, text, or embedded resource), refusing files over the inline limit.
func (s *Server) fsGet(root output.FileRoot, args []string) toolResult {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nativeError(output.Newf(output.FixableByAgent, "get needs a file path relative to %q", root.Name))
	}
	rel := args[0]
	abs, err := output.SafeResolve(root, rel)
	if err != nil {
		return nativeError(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fsError(err)
	}
	if info.IsDir() {
		return nativeError(output.Newf(output.FixableByAgent, "%s is a directory; use ls", rel))
	}
	if info.Size() > s.opts.fileInlineLimit {
		return nativeError(output.Newf(output.FixableByHuman,
			"file is %d bytes, over the %d-byte inline limit", info.Size(), s.opts.fileInlineLimit).
			WithHint("the file is too large to return inline over MCP"))
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return fsError(err)
	}
	relSlash := path.Clean(filepath.ToSlash(rel))
	mimeType := output.SniffMimeType(path.Base(relSlash), data)
	ref := output.NewFileRef(root.Name, relSlash)
	ref.Size = info.Size()
	ref.MimeType = mimeType

	return toolResult{
		Content:           []contentBlock{fileContentBlock(root.Name, relSlash, mimeType, data)},
		StructuredContent: &structuredContent{Records: []any{ref}},
		IsError:           false,
	}
}

// dirEntryRef builds a FileRef for a directory listing entry, tagging
// directories with an inode/directory MIME so the agent can tell them apart.
func dirEntryRef(rootName, rel, abs string, e fs.DirEntry) output.FileRef {
	if e.IsDir() {
		ref := output.NewFileRef(rootName, rel)
		ref.MimeType = "inode/directory"
		return ref
	}
	ref, err := output.FileRefAt(rootName, rel, abs)
	if err != nil {
		return output.NewFileRef(rootName, rel)
	}
	return ref
}

// fsRecordsResult renders a list of FileRefs as a tool result: structured
// records plus an NDJSON text block for clients that ignore structuredContent.
func fsRecordsResult(refs []output.FileRef, truncated bool) toolResult {
	records := make([]any, len(refs))
	var b strings.Builder
	for i, r := range refs {
		records[i] = r
		if line, err := json.Marshal(r); err == nil {
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	sc := &structuredContent{Records: records}
	if truncated {
		sc.Meta = map[string]any{"@truncated": true}
		fmt.Fprintf(&b, "(results truncated at %d)\n", maxFindResults)
	}
	text := b.String()
	if text == "" {
		text = "(no files)"
	}
	return toolResult{Content: []contentBlock{textBlock(text)}, StructuredContent: sc, IsError: false}
}

// fileRoot looks up a configured root by name.
func (s *Server) fileRoot(name string) (output.FileRoot, bool) {
	for _, r := range s.opts.fileRoots {
		if r.Name == name {
			return r, true
		}
	}
	return output.FileRoot{}, false
}

// fileRootNames lists the configured root names, for help and error messages.
func (s *Server) fileRootNames() []string {
	names := make([]string, len(s.opts.fileRoots))
	for i, r := range s.opts.fileRoots {
		names[i] = r.Name
	}
	return names
}

// fileToolHelp is the usage text for the file tool's help verb.
func (s *Server) fileToolHelp() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — read-only local file access\n\n", s.opts.fileToolName)
	b.WriteString("Roots: " + strings.Join(s.fileRootNames(), ", ") + "\n\n")
	b.WriteString("Verbs (pass as args):\n")
	b.WriteString(`  ["find","<root>","-e","png","-e","jpg"]  — search a root by extension and/or a bare glob` + "\n")
	b.WriteString(`  ["ls","<root>","<dir?>"]                  — list a directory (default the root)` + "\n")
	b.WriteString(`  ["get","<root>","<path>"]                 — return a file's contents` + "\n\n")
	b.WriteString("Paths are relative to the root; the host filesystem path is never exposed.\n")
	return b.String()
}
