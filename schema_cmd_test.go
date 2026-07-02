package agentmcp

import (
	"bytes"
	"encoding/json"
	"testing"

	output "github.com/shhac/lib-agent-output"

	"github.com/shhac/lib-agent-mcp/oauth"
)

// mcp schema is the exec-based contract a host binary consumes: the full
// reflected tool manifest, without starting a server.
func TestSchemaCommandEmitsManifest(t *testing.T) {
	s := newServer(testRoot(),
		WithVersion("1.2.3"),
		WithFileRoots(output.FileRoot{Name: "cache", Path: t.TempDir()}),
		WithIdentityBinding(func(oauth.Verified) ([]string, []string) { return nil, nil }))

	cmd := schemaCommand(s)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}

	var manifest struct {
		Name            string           `json:"name"`
		Version         string           `json:"version"`
		Tools           []map[string]any `json:"tools"`
		FileRoots       []string         `json:"file_roots"`
		IdentityBinding bool             `json:"identity_binding"`
	}
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatalf("manifest not JSON: %v\n%s", err, out.String())
	}
	if manifest.Name != "widget" || manifest.Version != "1.2.3" {
		t.Errorf("name/version = %q/%q", manifest.Name, manifest.Version)
	}
	if len(manifest.Tools) == 0 {
		t.Fatal("manifest has no tools")
	}
	if _, ok := manifest.Tools[0]["inputSchema"]; !ok {
		t.Errorf("tool entry missing inputSchema: %v", manifest.Tools[0])
	}
	if len(manifest.FileRoots) != 2 || manifest.FileRoots[0] != "cache" {
		// WithFileRoots also mounts the fs tool root list verbatim; expect ours first.
		if len(manifest.FileRoots) == 0 || manifest.FileRoots[0] != "cache" {
			t.Errorf("file_roots = %v", manifest.FileRoots)
		}
	}
	if !manifest.IdentityBinding {
		t.Error("identity_binding should be true when a binding is declared")
	}
}

func TestSchemaCommandDefaults(t *testing.T) {
	cmd := schemaCommand(newServer(testRoot()))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(out.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["identity_binding"] != false {
		t.Errorf("identity_binding = %v, want false", manifest["identity_binding"])
	}
}
