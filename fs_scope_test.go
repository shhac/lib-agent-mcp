package agentmcp

import (
	"context"
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
