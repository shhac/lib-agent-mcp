package agentmcp

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

// schemaCommand is `mcp schema`: it prints the server's manifest as JSON and
// exits, without serving anything. This is the exec-based contract a host
// binary uses to mount several CLIs' tools behind one endpoint — schemas stay
// out-of-process just like execution already is, so the host never needs to
// link a tool's code.
func schemaCommand(s *Server) *cobra.Command {
	return &cobra.Command{
		Use:         "schema",
		Short:       "Print the MCP tool manifest as JSON (for host binaries; no server is started)",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{AnnotationSkip: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			rootNames := make([]string, 0, len(s.opts.fileRoots))
			for _, r := range s.opts.fileRoots {
				rootNames = append(rootNames, r.Name)
			}
			manifest := map[string]any{
				"name":    s.opts.name,
				"version": s.opts.version,
				"tools":   s.tools,
				// file_roots names the read-only roots the fs tool exposes;
				// paths are host-local and deliberately not exported.
				"file_roots": rootNames,
				// identity_binding says whether this binary translates a
				// principal binding into argv/env when *it* runs the tools.
				// The translation itself is in-process code, so a host that
				// execs tools directly still needs this binary as the runner
				// (host mode invokes `<tool> mcp` per call, not raw commands).
				"identity_binding": s.opts.identityBinding != nil,
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(manifest)
		},
	}
}
