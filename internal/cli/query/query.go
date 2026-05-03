// Package query hosts the `orchard query` cobra subcommand group, which
// dispatches GraphQL queries against the running daemon.
//
// Workstream A scope: only the `--raw <gql>` escape hatch is wired
// end-to-end — it proves the CLI -> daemon -> GraphQL path works against
// the stub `health` resolver. Verbs like `worktrees`, `panes`,
// `processes` per the impl guide land in Workstream C alongside the real
// node types and resolvers.
//
// The cobra alias `q` mirrors the impl guide's "permitted alias for
// query" so typing ergonomics match the documented CLI shape.
package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
)

// Command returns the `query` subcommand group with its alias.
func Command() *cobra.Command {
	var raw string
	cmd := &cobra.Command{
		Use:     "query",
		Aliases: []string{"q"},
		Short:   "Query the running orchard daemon via GraphQL",
		Long: "Dispatch GraphQL queries against the running daemon at " + server.DefaultAddr + ".\n\n" +
			"Workstream A only wires `--raw <gql>` end-to-end; named verbs (worktrees, panes,\n" +
			"processes, conversations, contracts, hosts) land with Workstream C.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if raw == "" {
				return fmt.Errorf("provide --raw '<graphql>' (named verbs land in Workstream C)")
			}
			return runRaw(cmd.OutOrStdout(), raw)
		},
	}
	cmd.Flags().StringVar(&raw, "raw", "", "raw GraphQL query string")
	return cmd
}

func runRaw(w io.Writer, query string) error {
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+server.DefaultAddr+"/graphql", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("daemon not running, start with `orchard daemon start` (%w)", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned %d: %s", resp.StatusCode, string(data))
	}
	// Pretty-print so humans reading stdout get something readable.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		// Fall back to raw bytes if the response wasn't JSON.
		_, _ = w.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			_, _ = w.Write([]byte("\n"))
		}
		return nil
	}
	_, _ = w.Write(pretty.Bytes())
	_, _ = w.Write([]byte("\n"))
	return nil
}
