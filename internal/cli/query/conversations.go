package query

import (
	"github.com/spf13/cobra"
)

// conversationsCmd returns the `orchard query conversations` subcommand.
//
// Issues a Conversation GraphQL query against the running daemon and
// prints the JSON response. Flags:
//
//   - `--open`  includes the `open` heartbeat boolean.
//   - `--recap` includes the (always-null in v1) `recap` field.
//
// Both default to false so the unflagged invocation produces a small,
// stable shape: id, sessionUuid, cwd, firstSeenAt, lastSeenAt,
// messageCount.
func conversationsCmd() *cobra.Command {
	var (
		showOpen  bool
		showRecap bool
	)
	c := &cobra.Command{
		Use:   "conversations",
		Short: "List Claude Code conversations discovered under CLAUDE_PROJECTS_ROOT",
		Long: "Issue a Conversation GraphQL query against the running orchard daemon and\n" +
			"print the JSON response. Reports per-conversation metadata (sessionUuid, cwd,\n" +
			"firstSeenAt, lastSeenAt, messageCount). Use --open to include the heartbeat\n" +
			"boolean and --recap to include the daemon-derived recap text (extracted from\n" +
			"the latest `/recap` slash-command invocation; null if no `/recap` was run).",
		Example: "  orchard query conversations\n" +
			"  orchard query conversations --open\n" +
			"  orchard query conversations --open --recap",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRaw(cmd.Context(), cmd.OutOrStdout(), conversationsQuery(showOpen, showRecap))
		},
	}
	c.Flags().BoolVar(&showOpen, "open", false, "include the `open` heartbeat boolean")
	c.Flags().BoolVar(&showRecap, "recap", false, "include the `recap` field (daemon-derived from the latest `/recap` invocation)")
	return c
}

// conversationsQuery composes the GraphQL document for the conversations verb.
func conversationsQuery(showOpen, showRecap bool) string {
	q := "query Conversations {\n  conversations {\n" +
		"    id\n" +
		"    sessionUuid\n" +
		"    cwd\n" +
		"    firstSeenAt\n" +
		"    lastSeenAt\n" +
		"    messageCount\n"
	if showOpen {
		q += "    open\n"
	}
	if showRecap {
		q += "    recap\n"
	}
	q += "  }\n}"
	return q
}
