package agentmcp

import (
	"path"
	"strings"

	output "github.com/shhac/lib-agent-output"
)

// parseFindArgs reads find filters: -e/--ext <ext> (repeatable, leading dot
// optional) and a single bare glob. Unknown dash-flags are rejected.
func parseFindArgs(args []string) (exts map[string]bool, glob string, err error) {
	exts = map[string]bool{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-e" || a == "--ext":
			if i+1 >= len(args) {
				return nil, "", output.Newf(output.FixableByAgent, "%s needs an extension argument", a)
			}
			i++
			exts[normalizeExt(args[i])] = true
		case strings.HasPrefix(a, "-e="):
			exts[normalizeExt(strings.TrimPrefix(a, "-e="))] = true
		case strings.HasPrefix(a, "-"):
			return nil, "", output.Newf(output.FixableByAgent, "unknown find flag %q", a).
				WithHint("supported: -e <ext> (repeatable) and a single bare glob")
		default:
			glob = a
		}
	}
	return exts, glob, nil
}

func normalizeExt(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "."))
}

// matchFind reports whether a root-relative file path satisfies the extension
// set (if any) and the glob (if any); both filters must pass. The glob matches
// either the basename or the full relative path.
func matchFind(rel string, exts map[string]bool, glob string) bool {
	if len(exts) > 0 && !exts[normalizeExt(path.Ext(rel))] {
		return false
	}
	if glob == "" {
		return true
	}
	matchedBase, _ := path.Match(glob, path.Base(rel))
	matchedFull, _ := path.Match(glob, rel)
	return matchedBase || matchedFull
}
