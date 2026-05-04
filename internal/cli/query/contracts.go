package query

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// contractsBaseQuery is the canonical projection of every Contract
// node — every scalar plus the questions sub-list. Mirrors host.go's
// "one canonical query const" pattern so a regression in the resolver
// surfaces immediately in `orchard query contracts`.
const contractsBaseQuery = `query Contracts($filter: ContractFilter) {
  contracts(filter: $filter) {
    id
    contractId
    statement
    ownerSessionId
    ownerAgentName
    reportsTo
    parentContractId
    status
    createdAt
    updatedAt
    lastEventAt
    criteria
    openQuestions {
      questionId
      text
      askedBy
      askedAt
      deadline
      blocksClose
    }
  }
}`

// validStatuses lists the schema-recognised filter statuses, so we can
// reject typos client-side without making a round-trip to the daemon.
var validStatuses = map[string]string{
	"open":                                "OPEN",
	"delivered_pending_validation":        "DELIVERED_PENDING_VALIDATION",
	"delivered_pending_parent_validation": "DELIVERED_PENDING_PARENT_VALIDATION",
	"pending_drew_approval":               "PENDING_DREW_APPROVAL",
	"awaiting_cancel_ack":                 "AWAITING_CANCEL_ACK",
	"waiting_external":                    "WAITING_EXTERNAL",
	"satisfied":                           "SATISFIED",
	"cancelled":                           "CANCELLED",
	"judge_rejected_terminal":             "JUDGE_REJECTED_TERMINAL",
}

// contractsCmd returns the `orchard query contracts` subcommand.
//
// Issues contractsBaseQuery against the running daemon and prints the
// JSON response. The optional --status flag tightens the server-side
// filter; multiple statuses are comma-separated. The hidden --addr
// flag lets e2e tests target an ephemeral daemon.
func contractsCmd() *cobra.Command {
	var statusFlag string
	var ownerSession string
	var ownerAgent string
	var parentID string

	c := &cobra.Command{
		Use:   "contracts",
		Short: "List Contract nodes the daemon is tracking, with optional filters.",
		Long: "Query the contracts provider for every contract folded from the\n" +
			"claude-contracts JSONL log. Sorted descending by lastEventAt.",
		Example: "  orchard query contracts\n" +
			"  orchard query contracts --status open",
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := buildFilterPayload(statusFlag, ownerSession, ownerAgent, parentID)
			if err != nil {
				return err
			}
			query := buildQueryWithVars(contractsBaseQuery, filter)
			return runRaw(cmd.Context(), cmd.OutOrStdout(), query)
		},
	}
	c.Flags().StringVar(&statusFlag, "status", "", "filter by status (comma-separated)")
	c.Flags().StringVar(&ownerSession, "owner-session", "", "filter by owner session id")
	c.Flags().StringVar(&ownerAgent, "owner-agent", "", "filter by owner agent name")
	c.Flags().StringVar(&parentID, "parent", "", "filter by parent contract id")
	return c
}

// buildFilterPayload converts user-provided flag values into the
// ContractFilter literal embedded in the GraphQL query body. Empty
// flags collapse to nil so the server applies no filter.
func buildFilterPayload(statusFlag, ownerSession, ownerAgent, parentID string) (string, error) {
	parts := []string{}
	if statusFlag != "" {
		statuses, err := parseStatuses(statusFlag)
		if err != nil {
			return "", err
		}
		parts = append(parts, "statuses: ["+strings.Join(statuses, ", ")+"]")
	}
	if ownerSession != "" {
		parts = append(parts, fmt.Sprintf("ownerSessionId: %q", ownerSession))
	}
	if ownerAgent != "" {
		parts = append(parts, fmt.Sprintf("ownerAgentName: %q", ownerAgent))
	}
	if parentID != "" {
		parts = append(parts, fmt.Sprintf("parentContractId: %q", parentID))
	}
	if len(parts) == 0 {
		return "null", nil
	}
	return "{" + strings.Join(parts, ", ") + "}", nil
}

// parseStatuses turns "open,pending_drew_approval" into the SCREAMING
// enum constants the schema expects. Unknown statuses produce a fast
// client-side error so the user never has to hit the daemon to find
// out about a typo.
func parseStatuses(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		key := strings.ToLower(strings.TrimSpace(p))
		if key == "" {
			continue
		}
		val, ok := validStatuses[key]
		if !ok {
			return nil, fmt.Errorf("unknown status %q (try `orchard query contracts --help`)", p)
		}
		out = append(out, val)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--status was empty")
	}
	return out, nil
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
