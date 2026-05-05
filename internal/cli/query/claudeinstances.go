package query

import (
	"github.com/spf13/cobra"
)

// claudeInstancesQuery is the canonical projection of every
// ClaudeInstance node. Mirrors hostQuery: keeps the CLI in lockstep
// with the schema so a regression in the resolver surfaces immediately
// in `orchard query claude-instances`.
const claudeInstancesQuery = `query LocalClaudeInstances {
  claudeInstances {
    id
    state
    rcUrl
    rcEnabled
    sessionUuid
    startedAt
    pane {
      id
    }
    process {
      id
    }
    account {
      id
    }
  }
}`

// claudeInstancesCmd returns the `orchard query claude-instances`
// subcommand. The hyphenated name mirrors the schema field that turns
// into camelCase on the GraphQL side and reads naturally on the CLI.
func claudeInstancesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "claude-instances",
		Short: "List Claude instances running on the local host",
		Long: "Issue the canonical ClaudeInstance GraphQL query against the running orchard daemon\n" +
			"and print the JSON response. One row per heartbeat file present at\n" +
			"$ORCHARD_HEARTBEAT_DIR (default $TMPDIR). Edges to TmuxPane / Process / ClaudeAccount\n" +
			"are populated when the relevant sibling provider is wired (ws-b-tmux, ws-b-ps,\n" +
			"ws-b-claudeaccount).",
		Example: "  orchard query claude-instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRaw(cmd.Context(), cmd.OutOrStdout(), claudeInstancesQuery)
		},
	}
	return c
}
