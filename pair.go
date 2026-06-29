package agentmcp

import (
	"errors"
	"fmt"

	"github.com/shhac/lib-agent-mcp/oauth"
	"github.com/spf13/cobra"
)

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
		Use:         "rotate",
		Short:       "Issue a fresh pairing code, invalidating the old one",
		Long: "Rotate the pairing code — use this if the code leaks. Already-connected " +
			"clients keep working (their tokens are unaffected); only new pairings need the new code.",
		Annotations: skip,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := s.oauthStore()
			if err != nil {
				return err
			}
			code, err := oauth.NewPairing(store).Rotate()
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
		Use:         "reset",
		Short:       "Wipe ALL local-OAuth state (signing key, clients, tokens, pairing code)",
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
	return pair
}
