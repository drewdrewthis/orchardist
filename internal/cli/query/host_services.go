package query

import (
	"github.com/spf13/cobra"
)

// hostServicesQuery is the canonical projection of the curated
// launchd / systemd watchlist. Every field on HostService plus the
// owning Host's identity. Keeping the CLI in lockstep with the schema
// means a regression in the resolver surfaces immediately in
// `orchard query host-services`.
const hostServicesQuery = `query LocalHostServices {
  host {
    id
    machineId
    hostServices {
      id
      name
      state
      since
      exitCode
      logTail
      host {
        id
        machineId
      }
    }
  }
}`

// hostServicesCmd returns the `orchard query host-services` subcommand.
//
// Issues hostServicesQuery against the running daemon and prints the
// GraphQL response (data + any errors) as pretty JSON. The hidden
// --addr flag lets the e2e tests target an ephemeral daemon listening
// on a non-default port.
func hostServicesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "host-services",
		Short: "Print the curated launchd / systemd watchlist for this host",
		Long: "Issue the canonical HostService GraphQL query against the running orchard\n" +
			"daemon and print the JSON response. Reports each watched service's lifecycle\n" +
			"state (active|inactive|failed|unknown) plus optional since / exitCode / logTail.",
		Example: "  orchard query host-services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRaw(cmd.Context(), cmd.OutOrStdout(), hostServicesQuery)
		},
	}
	return c
}
