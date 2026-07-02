package agentmcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	output "github.com/shhac/lib-agent-output"

	"github.com/shhac/lib-agent-mcp/oauth"
)

func namedPrincipalCtx(name string, binding map[string]string) context.Context {
	return oauth.WithPrincipal(context.Background(),
		oauth.Verified{PrincipalGrant: oauth.PrincipalGrant{Name: name, Binding: binding}})
}

func callFsCtx(ctx context.Context, s *Server, args ...string) toolResult {
	return s.callTool(ctx, s.toolsByName[s.opts.fileToolName], args, nil)
}

// A named principal on a server with roots but NO scope function must see no
// file roots at all: an unscoped shared root would let one principal read
// another's files, so absence fails closed.
func TestFsNamedPrincipalWithoutScopeSeesNoRoots(t *testing.T) {
	s, _ := fsServer(t)
	ctx := namedPrincipalCtx("alice", map[string]string{"workspace": "alice-acme"})

	res := callFsCtx(ctx, s, "get", "cache", "notes.txt")
	if !res.IsError {
		t.Fatal("named principal read a file through an unscoped root")
	}
	res = callFsCtx(ctx, s, "ls", "cache")
	if !res.IsError {
		t.Fatal("named principal listed an unscoped root")
	}
}

// With a scope function, a named principal's roots are rewritten — here to an
// identity subtree — and everything outside it is invisible.
func TestFsScopeConfinesNamedPrincipalToSubtree(t *testing.T) {
	s, dir := fsServer(t, WithFileRootScope(func(p oauth.Verified, root output.FileRoot) (output.FileRoot, bool) {
		sub := p.Binding["subtree"]
		if sub == "" {
			return output.FileRoot{}, false
		}
		return output.FileRoot{Name: root.Name, Path: filepath.Join(root.Path, sub)}, true
	}))
	writeFile(t, filepath.Join(dir, "T1", "U1", "downloads", "mine.txt"), []byte("alice's file"))
	writeFile(t, filepath.Join(dir, "T1", "U2", "downloads", "theirs.txt"), []byte("bob's file"))

	alice := namedPrincipalCtx("alice", map[string]string{"subtree": "T1/U1"})

	// Own subtree: readable, with paths relative to the scoped root.
	res := callFsCtx(alice, s, "get", "cache", "downloads/mine.txt")
	if res.IsError {
		t.Fatalf("alice cannot read her own file: %+v", res)
	}
	// Another identity's subtree: invisible via get and find alike.
	if res := callFsCtx(alice, s, "get", "cache", "../U2/downloads/theirs.txt"); !res.IsError {
		t.Error("path traversal escaped the scoped root")
	}
	found := callFsCtx(alice, s, "find", "cache", "-e", "txt")
	for p := range recordPaths(found) {
		if strings.Contains(p, "theirs") {
			t.Errorf("find surfaced another principal's file: %s", p)
		}
	}

	// A principal whose scope resolves to nothing sees no roots.
	unbound := namedPrincipalCtx("carol", nil)
	if res := callFsCtx(unbound, s, "ls", "cache"); !res.IsError {
		t.Error("scope-less principal listed the root")
	}
}

// Operator calls — no principal on the context, or the anonymous zero grant —
// keep the full configured roots.
func TestFsOperatorKeepsFullRoots(t *testing.T) {
	s, _ := fsServer(t, WithFileRootScope(func(oauth.Verified, output.FileRoot) (output.FileRoot, bool) {
		return output.FileRoot{}, false // named principals see nothing
	}))
	if res := callFs(s, "get", "cache", "notes.txt"); res.IsError {
		t.Errorf("operator (no principal) lost root access: %+v", res)
	}
	anon := oauth.WithPrincipal(context.Background(), oauth.Verified{ClientID: "c1"})
	if res := callFsCtx(anon, s, "get", "cache", "notes.txt"); res.IsError {
		t.Errorf("anonymous operator lost root access: %+v", res)
	}
}

// The CLI-subprocess path (callTool → run → translate) must rewrite FileRefs
// against the caller's SCOPED roots: a regression to the configured roots
// would mint refs in another principal's namespace and break round-tripping
// through the scoped fs get.
func TestCallToolRewritesFileRefsAgainstScopedRoots(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, "T1", "U1", "downloads", "mine.png")
	writeFile(t, mine, pngBytes())

	script := filepath.Join(t.TempDir(), "emit.sh")
	body := "#!/bin/sh\necho '{\"id\":\"F1\",\"path\":\"" + mine + "\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	s := newServer(testRoot(),
		WithExecutable(script),
		WithFileRoots(output.FileRoot{Name: "cache", Path: dir}),
		WithFileRootScope(func(p oauth.Verified, root output.FileRoot) (output.FileRoot, bool) {
			return output.FileRoot{Name: root.Name, Path: filepath.Join(root.Path, p.Binding["subtree"])}, true
		}))
	tool, ok := s.toolsByName["item_list"]
	if !ok {
		t.Fatalf("item_list tool missing; have %v", func() []string {
			names := make([]string, 0, len(s.toolsByName))
			for n := range s.toolsByName {
				names = append(names, n)
			}
			return names
		}())
	}

	// Named principal: the ref is relative to HER scoped root.
	res := s.callTool(namedPrincipalCtx("alice", map[string]string{"subtree": "T1/U1"}), tool, nil, nil)
	rec, ok := res.StructuredContent.Records[0].(map[string]any)
	if !ok {
		t.Fatalf("record = %#v", res.StructuredContent.Records[0])
	}
	ref, ok := rec["path"].(output.FileRef)
	if !ok {
		t.Fatalf("path not rewritten for scoped principal: %#v", rec["path"])
	}
	if ref.Root != "cache" || ref.Path != "downloads/mine.png" {
		t.Errorf("scoped ref = %+v, want path downloads/mine.png (relative to the scoped root)", ref)
	}

	// Operator: the same output rewrites relative to the full configured root.
	res = s.callTool(context.Background(), tool, nil, nil)
	rec = res.StructuredContent.Records[0].(map[string]any)
	ref, ok = rec["path"].(output.FileRef)
	if !ok {
		t.Fatalf("path not rewritten for operator: %#v", rec["path"])
	}
	if ref.Path != "T1/U1/downloads/mine.png" {
		t.Errorf("operator ref = %+v, want path T1/U1/downloads/mine.png", ref)
	}
}

// A symlink planted inside one principal's subtree pointing at another's file
// must not escape the scoped root through any verb.
func TestFsScopedRootRejectsSymlinkEscape(t *testing.T) {
	s, dir := fsServer(t, WithFileRootScope(func(p oauth.Verified, root output.FileRoot) (output.FileRoot, bool) {
		return output.FileRoot{Name: root.Name, Path: filepath.Join(root.Path, p.Binding["subtree"])}, true
	}))
	writeFile(t, filepath.Join(dir, "T1", "U2", "downloads", "theirs.txt"), []byte("bob's file"))
	if err := os.MkdirAll(filepath.Join(dir, "T1", "U1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "T1", "U2", "downloads", "theirs.txt"),
		filepath.Join(dir, "T1", "U1", "sneaky.txt")); err != nil {
		t.Fatal(err)
	}

	alice := namedPrincipalCtx("alice", map[string]string{"subtree": "T1/U1"})
	if res := callFsCtx(alice, s, "get", "cache", "sneaky.txt"); !res.IsError {
		t.Error("get followed a symlink out of the scoped root")
	}
	for p := range recordPaths(callFsCtx(alice, s, "find", "cache", "-e", "txt")) {
		if strings.Contains(p, "sneaky") || strings.Contains(p, "theirs") {
			t.Errorf("find surfaced a cross-subtree symlink: %s", p)
		}
	}
}

// Pin the full (Name, Binding) quadrant for effectiveFileRoots: only the
// zero grant counts as the operator.
func TestEffectiveFileRootsQuadrant(t *testing.T) {
	s, dir := fsServer(t, WithFileRootScope(func(p oauth.Verified, root output.FileRoot) (output.FileRoot, bool) {
		return output.FileRoot{Name: "scoped", Path: root.Path}, true
	}))
	cases := map[string]struct {
		grant    oauth.PrincipalGrant
		wantName string // "" = no principal semantics don't apply here
	}{
		"zero grant is operator": {oauth.PrincipalGrant{}, "cache"},
		"named, no binding":      {oauth.PrincipalGrant{Name: "a"}, "scoped"},
		"binding, no name":       {oauth.PrincipalGrant{Binding: map[string]string{"k": "v"}}, "scoped"},
		"named with binding":     {oauth.PrincipalGrant{Name: "a", Binding: map[string]string{"k": "v"}}, "scoped"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := oauth.WithPrincipal(context.Background(), oauth.Verified{PrincipalGrant: tc.grant})
			roots := s.effectiveFileRoots(ctx)
			if len(roots) != 1 || roots[0].Name != tc.wantName {
				t.Errorf("roots = %+v, want single %q root (path base %s)", roots, tc.wantName, dir)
			}
		})
	}
}
