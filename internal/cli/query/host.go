package query

import (
	"github.com/spf13/cobra"
)

// hostQuery is the canonical projection of the local Host node — every
// scalar plus the full resource-load block. Keeps the CLI in lockstep
// with the schema so a regression in the resolver surfaces immediately
// in `orchard query host`.
const hostQuery = `query LocalHost {
  host {
    id
    machineId
    hostname
    os
    kernel
    address
    reachable
    resourceLoad {
      cpuPercent
      memPercent
      diskPercent
      loadAvg1m
      loadAvg5m
      loadAvg15m
    }
    lastSeenAt
    peers {
      id
    }
  }
}`

// hostCmd returns the `orchard query host` subcommand.
//
// Issues hostQuery against the running daemon and prints the GraphQL
// response (data + any errors) as pretty JSON. Tests that need to
// retarget the daemon URL use SetDaemonURLForTest.
func hostCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "host",
		Short: "Print the local Host node with current resource load",
		Long: "Issue the canonical Host GraphQL query against the running orchard daemon and\n" +
			"print the JSON response. Reports identity (machineId, hostname, os, kernel) plus\n" +
			"a fresh ResourceLoad sample (cpu%, mem%, disk%, loadavg{1,5,15}m).",
		Example: "  orchard query host",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRaw(cmd.Context(), cmd.OutOrStdout(), hostQuery)
		},
	}
}
