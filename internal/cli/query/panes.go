// `orchard query panes` — list tmux panes from the local daemon.
//
// Usage:
//
//	orchard query panes
//	orchard query panes --where currentCommand=claude
//	orchard query panes --where session=main --where currentCommand=zsh
//
// `--where KEY=VALUE` is repeatable; multiple filters AND-combine.
// Recognised keys (mirror `TmuxPaneFilter`):
//
//	currentCommand    foreground command basename
//	paneId            tmux pane id (e.g. %26)
//	session           session name
//	titleContains     case-sensitive substring of pane_title
//	dead              true / false
//
// Output is JSON for downstream tooling.

package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func panesCmd() *cobra.Command {
	var where []string
	c := &cobra.Command{
		Use:   "panes",
		Short: "List tmux panes visible to the local daemon (JSON)",
		Long: "Query the running orchard daemon for tmux panes. " +
			"Use --where KEY=VALUE (repeatable) to filter; multiple " +
			"filters AND-combine.\n\n" +
			"Recognised keys: currentCommand, paneId, session, " +
			"titleContains, dead.",
		Example: "  orchard query panes\n" +
			"  orchard query panes --where currentCommand=claude\n" +
			"  orchard query panes --where session=main --where currentCommand=zsh",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPanes(cmd.Context(), cmd.OutOrStdout(), where)
		},
	}
	c.Flags().StringSliceVar(&where, "where", nil, "filter expression KEY=VALUE (repeat for AND)")
	return c
}

const panesQuery = `query Panes($filter: TmuxPaneFilter) {
  tmuxPanes(filter: $filter) {
    id
    paneId
    title
    currentCommand
    currentPid
    width
    height
    dead
  }
}`

type panesEntry struct {
	ID             string `json:"id"`
	PaneID         string `json:"paneId"`
	Title          string `json:"title"`
	CurrentCommand string `json:"currentCommand"`
	CurrentPid     *int64 `json:"currentPid,omitempty"`
	Width          int64  `json:"width"`
	Height         int64  `json:"height"`
	Dead           bool   `json:"dead"`
}

type panesResponse struct {
	Panes  []panesEntry      `json:"panes"`
	Errors []json.RawMessage `json:"errors,omitempty"`
}

func runPanes(ctx context.Context, w io.Writer, where []string) error {
	filter, err := parsePaneWhere(where)
	if err != nil {
		return err
	}

	var variables map[string]any
	if len(filter) > 0 {
		variables = map[string]any{"filter": filter}
	}

	body, err := json.Marshal(map[string]any{
		"query":     panesQuery,
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
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned %d: %s", resp.StatusCode, string(raw))
	}

	var envelope struct {
		Data struct {
			TmuxPanes []panesEntry `json:"tmuxPanes"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	out := panesResponse{
		Panes:  envelope.Data.TmuxPanes,
		Errors: envelope.Errors,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	return nil
}

// parsePaneWhere converts `--where KEY=VALUE` flags into the GraphQL
// TmuxPaneFilter shape. Unknown keys are an error so users notice typos
// instead of silently getting the unfiltered set.
func parsePaneWhere(exprs []string) (map[string]any, error) {
	out := map[string]any{}
	for _, raw := range exprs {
		eq := strings.IndexByte(raw, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("--where %q: expected KEY=VALUE", raw)
		}
		key := strings.TrimSpace(raw[:eq])
		value := strings.TrimSpace(raw[eq+1:])
		if value == "" {
			return nil, fmt.Errorf("--where %q: empty value", raw)
		}
		switch key {
		case "currentCommand":
			out["currentCommandIn"] = appendStringList(out["currentCommandIn"], value)
		case "paneId":
			out["paneIdIn"] = appendStringList(out["paneIdIn"], value)
		case "session":
			out["sessionIn"] = appendStringList(out["sessionIn"], value)
		case "titleContains":
			out["titleContains"] = value
		case "dead":
			b, err := parseBool(value)
			if err != nil {
				return nil, fmt.Errorf("--where dead=%q: %w", value, err)
			}
			out["dead"] = b
		default:
			return nil, fmt.Errorf("--where %q: unknown key (allowed: currentCommand, paneId, session, titleContains, dead)", key)
		}
	}
	return out, nil
}

func appendStringList(prev any, value string) []string {
	if prev == nil {
		return []string{value}
	}
	if list, ok := prev.([]string); ok {
		return append(list, value)
	}
	return []string{value}
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "1", "yes", "y":
		return true, nil
	case "false", "0", "no", "n":
		return false, nil
	}
	return false, fmt.Errorf("expected true|false")
}
