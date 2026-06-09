package query

import (
	"github.com/spf13/cobra"

	"github.com/drewdrewthis/orchardist/internal/server"
)

// claudeAccountQuery is the canonical projection of the local
// ClaudeAccount node — every scalar plus the host id back-edge.
const claudeAccountQuery = `query LocalClaudeAccount {
  claudeAccounts {
    id
    email
    quotaUsed
    quotaCap
    quotaEstimated
    quotaResetsAt
    host { id }
    instances { id }
  }
}`

// claudeAccountCmd returns the `orchard query claude-account` subcommand.
// The hidden --addr flag lets e2e tests target an ephemeral daemon.
func claudeAccountCmd() *cobra.Command {
	var addr string
	c := &cobra.Command{
		Use:     "claude-account",
		Aliases: []string{"claude-accounts"},
		Short:   "Print the local ClaudeAccount node with current quota",
		Long: "Issue the canonical ClaudeAccount GraphQL query against the running orchard daemon\n" +
			"and print the JSON response.",
		Example: "  orchard query claude-account",
		RunE: func(cmd *cobra.Command, args []string) error {
			restore := setAddrForCommand(addr)
			defer restore()
			return runRaw(cmd.Context(), cmd.OutOrStdout(), claudeAccountQuery)
		},
	}
	c.Flags().StringVar(&addr, "addr", "", "host:port the daemon is listening on (overrides ORCHARD_DAEMON_URL)")
	return c
}

// setAddrForCommand temporarily overrides daemonURL for the duration of
// a single command. Returns a restore function the caller must defer.
func setAddrForCommand(addr string) func() {
	if addr == "" {
		return func() {}
	}
	prev := daemonURL
	daemonURL = "http://" + addr
	return func() { daemonURL = prev }
}

// reference server to keep the import alive even when --addr is empty.
var _ = server.DefaultAddr
