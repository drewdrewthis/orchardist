package query

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// contractsBaseQuery is the canonical projection of every Contract
// node. Mirrors host.go's "one canonical query const" pattern so a
// regression in the resolver surfaces immediately in
// `orchard query contracts`.
//
// v0.8 schema: two-status model (SIGNED/CLOSED) + closedReason
// (DELIVERED/ABANDONED). Heavy fields (criteria, openQuestions,
// reportsTo, parentContractId) have been removed from the schema.
const contractsBaseQuery = `query Contracts($filter: ContractFilter) {
  contracts(filter: $filter) {
    id
    contractId
    statement
    ownerSessionId
    status
    closedReason
    createdAt
    updatedAt
    lastEventAt
  }
}`

// validStatuses lists the v0.8 schema-recognised filter statuses so we
// can reject typos client-side without a round-trip to the daemon.
var validStatuses = map[string]string{
	"signed": "SIGNED",
	"closed": "CLOSED",
}

// validReasons lists the v0.8 schema-recognised closed-reason values.
var validReasons = map[string]string{
	"delivered": "DELIVERED",
	"abandoned": "ABANDONED",
}

// contractsCmd returns the `orchard query contracts` subcommand.
//
// Issues contractsBaseQuery against the running daemon and prints the
// JSON response. The optional --status flag tightens the server-side
// filter; multiple statuses are comma-separated. The optional
// --reason flag filters by closedReason (only meaningful when
// --status closed is also set). The hidden --addr flag lets e2e tests
// target an ephemeral daemon.
func contractsCmd() *cobra.Command {
	var statusFlag string
	var reasonFlag string
	var ownerSession string

	c := &cobra.Command{
		Use:   "contracts",
		Short: "List Contract nodes the daemon is tracking, with optional filters.",
		Long: "Query the contracts provider for every contract folded from the\n" +
			"session JSONL open_contract/close_contract events. Sorted descending by lastEventAt.",
		Example: "  orchard query contracts\n" +
			"  orchard query contracts --status signed\n" +
			"  orchard query contracts --status closed --reason delivered",
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := buildFilterPayload(statusFlag, reasonFlag, ownerSession)
			if err != nil {
				return err
			}
			query := buildQueryWithVars(contractsBaseQuery, filter)
			return runRaw(cmd.Context(), cmd.OutOrStdout(), query)
		},
	}
	c.Flags().StringVar(&statusFlag, "status", "", "filter by status: signed or closed (comma-separated)")
	c.Flags().StringVar(&reasonFlag, "reason", "", "filter by closed reason: delivered or abandoned (comma-separated)")
	c.Flags().StringVar(&ownerSession, "owner-session", "", "filter by owner session id")
	return c
}

// buildFilterPayload converts user-provided flag values into the
// ContractFilter literal embedded in the GraphQL query body. Empty
// flags collapse to nil so the server applies no filter.
func buildFilterPayload(statusFlag, reasonFlag, ownerSession string) (string, error) {
	parts := []string{}
	if statusFlag != "" {
		statuses, err := parseFromMap(statusFlag, validStatuses, "status")
		if err != nil {
			return "", err
		}
		parts = append(parts, "statuses: ["+strings.Join(statuses, ", ")+"]")
	}
	if reasonFlag != "" {
		reasons, err := parseFromMap(reasonFlag, validReasons, "reason")
		if err != nil {
			return "", err
		}
		parts = append(parts, "closedReasons: ["+strings.Join(reasons, ", ")+"]")
	}
	if ownerSession != "" {
		parts = append(parts, fmt.Sprintf("ownerSessionId: %q", ownerSession))
	}
	if len(parts) == 0 {
		return "null", nil
	}
	return "{" + strings.Join(parts, ", ") + "}", nil
}

// parseFromMap splits raw on commas, maps each token to the enum
// constant in allowed, and returns an error on the first unknown token.
func parseFromMap(raw string, allowed map[string]string, flagName string) ([]string, error) {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		key := strings.ToLower(strings.TrimSpace(p))
		if key == "" {
			continue
		}
		val, ok := allowed[key]
		if !ok {
			return nil, fmt.Errorf("unknown %s %q (try `orchard query contracts --help`)", flagName, p)
		}
		out = append(out, val)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--%s was empty", flagName)
	}
	return out, nil
}

// parseStatuses is retained for backward-compat with any existing
// tests that call it directly.
//
// Deprecated: use parseFromMap with validStatuses.
func parseStatuses(raw string) ([]string, error) {
	return parseFromMap(raw, validStatuses, "status")
}

// buildQueryWithVars splices the filter literal into the parameterised
// query body. We take this lightweight path instead of GraphQL
// variables to keep the CLI's JSON envelope identical to runRaw — one
// transport, one decode path.
func buildQueryWithVars(base, filter string) string {
	// Replace `($filter: ContractFilter)` and `filter: $filter` with
	// the inlined literal. The replacements are exact substrings so we
	// don't need a real parser.
	q := strings.Replace(base, "($filter: ContractFilter)", "", 1)
	return strings.Replace(q, "filter: $filter", "filter: "+filter, 1)
}
