// Package query — `orchard query processes` subcommand. Prints a JSON
// list of processes matching the given filters by hitting the local
// daemon's GraphQL surface.
package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// processesCmd implements `orchard query processes`. Mirrors the impl
// guide CLI shape: --by-cwd PATH and --command CMD, JSON output.
func processesCmd() *cobra.Command {
	var byCwd, byCommand string
	c := &cobra.Command{
		Use:   "processes",
		Short: "List processes visible to the local ps adapter (JSON)",
		Long: "Query the running orchard daemon for processes. " +
			"Filters compose: --by-cwd narrows by cwd prefix, --command by basename. " +
			"With no filters, every process visible to ps is returned.",
		Example: "  orchard query processes\n" +
			"  orchard query processes --command claude\n" +
			"  orchard query processes --by-cwd /Users/me/workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProcesses(cmd.Context(), cmd.OutOrStdout(), byCwd, byCommand)
		},
	}
	c.Flags().StringVar(&byCwd, "by-cwd", "", "filter by cwd prefix")
	c.Flags().StringVar(&byCommand, "command", "", "filter by command basename (e.g. 'claude')")
	return c
}

// processesQuery is the GraphQL query the CLI sends. Selecting the
// slow-path `args` and `cwd` fields here is intentional: a user asking
// `orchard query processes` expects a complete view, and the resolver's
// per-pid lsof cost is bounded by the filter (or the whole table when
// the user really wants the whole table).
const processesQuery = `query Processes($filter: ProcessFilter) {
  host {
    id
    processes(filter: $filter) {
      id
      pid
      ppid
      command
      args
      cwd
      startedAt
      cpuPercent
      memBytes
      tty
    }
  }
}`

// processesResponse is the JSON shape returned to the user. We project
// the GraphQL response into a flat list because nesting under host
// would be needless ceremony for a single-host CLI.
type processesResponse struct {
	Host      string            `json:"host"`
	Processes []processesEntry  `json:"processes"`
	Errors    []json.RawMessage `json:"errors,omitempty"`
}

type processesEntry struct {
	ID         string   `json:"id"`
	Pid        int64    `json:"pid"`
	Ppid       int64    `json:"ppid"`
	Command    string   `json:"command"`
	Args       []string `json:"args,omitempty"`
	Cwd        *string  `json:"cwd,omitempty"`
	StartedAt  string   `json:"startedAt"`
	CPUPercent float64  `json:"cpuPercent"`
	MemBytes   int64    `json:"memBytes"`
	Tty        *string  `json:"tty,omitempty"`
}

func runProcesses(ctx context.Context, w io.Writer, byCwd, byCommand string) error {
	filter := map[string]any{}
	if byCwd != "" {
		filter["cwdPrefix"] = byCwd
	}
	if byCommand != "" {
		filter["commandIn"] = []string{byCommand}
	}

	var variables map[string]any
	if len(filter) > 0 {
		variables = map[string]any{"filter": filter}
	}

	body, err := json.Marshal(map[string]any{
		"query":     processesQuery,
		"variables": variables,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resolveDaemonURL()+"/graphql", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("daemon not running, start with `orchard daemon start` (%w)", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned %d: %s", resp.StatusCode, string(raw))
	}

	var envelope struct {
		Data struct {
			Host struct {
				ID        string           `json:"id"`
				Processes []processesEntry `json:"processes"`
			} `json:"host"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	out := processesResponse{
		Host:      envelope.Data.Host.ID,
		Processes: envelope.Data.Host.Processes,
		Errors:    envelope.Errors,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	return nil
}
