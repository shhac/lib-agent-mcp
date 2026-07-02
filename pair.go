package agentmcp

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shhac/lib-agent-mcp/oauth"
	"github.com/spf13/cobra"
)

// pairing opens the local-OAuth secret store and wraps it in the pairing
// layer — the shared prologue of every pair subcommand.
func (s *Server) pairing() (*oauth.Pairing, error) {
	store, err := s.oauthStore()
	if err != nil {
		return nil, err
	}
	return oauth.NewPairing(store), nil
}

// pairCommand is `mcp pair`, the operator-facing maintenance surface for the
// local-OAuth pairing code and stored secrets. It runs without starting the
// server — it just reaches into the keyring namespace the server uses.
func pairCommand(s *Server) *cobra.Command {
	skip := map[string]string{AnnotationSkip: "true"}
	pair := &cobra.Command{
		Use:         "pair",
		Short:       "Manage the local-OAuth pairing code and stored secrets",
		Annotations: skip,
	}

	rotate := &cobra.Command{
		Use:   "rotate",
		Short: "Issue a fresh pairing code, invalidating the old one",
		Long: "Rotate the pairing code — use this if the code leaks. Already-connected " +
			"clients keep working (their tokens are unaffected); only new pairings need the new code.",
		Annotations: skip,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pairing, err := s.pairing()
			if err != nil {
				return err
			}
			code, err := pairing.Rotate()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"new pairing code: %s\n⚠ Treat it like a password. Existing connections keep their tokens; "+
					"only new pairings use this code.\n", code)
			return err
		},
	}

	var yes bool
	reset := &cobra.Command{
		Use:   "reset",
		Short: "Wipe ALL local-OAuth state (signing key, clients, tokens, pairing code)",
		Long: "Reset the local-OAuth layer to a clean slate: rotates the token-signing key " +
			"(invalidating every issued access and refresh token), and clears registered clients and the " +
			"pairing code. Use this if a token may be compromised. Every connector must re-register and " +
			"re-pair afterwards. Requires --yes.",
		Annotations: skip,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return errors.New("pair reset wipes the signing key, all registered clients, refresh tokens, " +
					"and the pairing code — every connector must re-pair. Re-run with --yes to confirm")
			}
			store, err := s.oauthStore()
			if err != nil {
				return err
			}
			if err := store.DeleteAll(); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(),
				"local-OAuth state cleared. Restart the server to generate a fresh signing key and pairing "+
					"code; every client must re-register and re-pair.")
			return err
		},
	}
	reset.Flags().BoolVar(&yes, "yes", false, "confirm the destructive reset")

	pair.AddCommand(rotate, reset)
	pair.AddCommand(pairAddCommand(s), pairListCommand(s), pairRemoveCommand(s))
	return pair
}

// pairAddCommand mints (or rotates) a named principal's pairing code, with
// optional --bind key=value routing data that WithIdentityBinding translates
// into subprocess argv/env for every call that principal makes.
func pairAddCommand(s *Server) *cobra.Command {
	var binds []string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Mint a pairing code for a named principal (repeatable --bind key=value attaches binding data)",
		Long: "Add (or rotate) a named principal: a person who pairs with their own code and whose " +
			"tool calls carry the attached binding — e.g. --bind workspace=<alias> to pin their credential set. " +
			"Re-running for an existing name rotates its code; already-issued tokens keep their old binding until expiry.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			binding, err := parseBindPairs(binds)
			if err != nil {
				return err
			}
			pairing, err := s.pairing()
			if err != nil {
				return err
			}
			code, err := pairing.AddPrincipal(args[0], binding)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"pairing code for %s: %s\n⚠ Share it only with %s — whoever completes the OAuth approval "+
					"with this code acts under this principal's binding.\n", args[0], code, args[0])
			return err
		},
	}
	cmd.Flags().StringArrayVar(&binds, "bind", nil, "binding data as key=value (repeatable), carried in the principal's tokens")
	Skip(cmd)
	return cmd
}

// pairListCommand lists principals and bindings — never codes.
func pairListCommand(s *Server) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List named principals and their bindings (codes are never shown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pairing, err := s.pairing()
			if err != nil {
				return err
			}
			principals, err := pairing.Principals()
			if err != nil {
				return err
			}
			if len(principals) == 0 {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "no named principals (only the shared operator pairing code)")
				return err
			}
			for _, name := range slices.Sorted(maps.Keys(principals)) {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), formatPrincipalLine(name, principals[name])); err != nil {
					return err
				}
			}
			return nil
		},
	}
	Skip(cmd)
	return cmd
}

// formatPrincipalLine renders one principal as "name k=v k2=v2" with binding
// keys in stable sorted order.
func formatPrincipalLine(name string, binding map[string]string) string {
	line := name
	for _, k := range slices.Sorted(maps.Keys(binding)) {
		line += fmt.Sprintf(" %s=%s", k, binding[k])
	}
	return line
}

// pairRemoveCommand revokes a principal: its code stops pairing and its
// refresh tokens are deleted; outstanding access tokens expire on their TTL.
func pairRemoveCommand(s *Server) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Revoke a named principal (pairing code + refresh tokens; access tokens lapse on expiry)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pairing, err := s.pairing()
			if err != nil {
				return err
			}
			removed, err := pairing.RemovePrincipal(args[0])
			if err != nil {
				return err
			}
			if !removed {
				return fmt.Errorf("no principal named %q", args[0])
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"%s removed: pairing revoked, refresh tokens deleted. Outstanding access tokens expire on their own TTL.\n", args[0])
			return err
		},
	}
	Skip(cmd)
	return cmd
}

// parseBindPairs turns repeated key=value flags into a binding map.
func parseBindPairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	m := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--bind %q is not key=value", kv)
		}
		m[k] = v
	}
	return m, nil
}
