// Package query hosts the `orchard query` cobra subcommand group, which
// dispatches GraphQL queries against the running daemon.
//
// Named verbs land per workstream — host, projects, processes, panes,
// conversations, claude-account, host-services. The `--raw <gql>` escape hatch always works.
//
// The cobra alias `q` mirrors the impl guide's "permitted alias for
// query" so typing ergonomics match the documented CLI shape.
package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
)

// daemonURLEnv lets E2E tests redirect the CLI at an httptest.Server
// without binding the production port.
const daemonURLEnv = "ORCHARD_DAEMON_URL"

// daemonURL is the base URL of the local daemon's GraphQL endpoint.
// Overridable in tests via SetDaemonURLForTest, or via the
// ORCHARD_DAEMON_URL env var (which takes precedence per call so a
// running test process can drive multiple isolated daemons).
var daemonURL = "http://" + server.DefaultAddr

func resolveDaemonURL() string {
	if v := os.Getenv(daemonURLEnv); v != "" {
		return v
	}
	return daemonURL
}

// httpTimeout bounds GraphQL roundtrips. Conservative — the daemon is
// local and responses are small JSON.
const httpTimeout = 10 * time.Second

// Command returns the `query` subcommand group with its alias.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "query",
		Aliases: []string{"q"},
		Short:   "Query the running orchard daemon via GraphQL",
		Long: "Dispatch GraphQL queries against the running daemon at " + server.DefaultAddr + ".\n\n" +
			"Use a named verb (e.g. `host`, `projects`, `processes`, `panes`, `conversations`, `claude-account`, `host-services`) for high-level reads,\n" +
			"or `--raw '<gql>'` as the escape hatch when you need a custom GraphQL query.",
		Example: "  orchard query host\n" +
			"  orchard query projects\n" +
			"  orchard query processes\n" +
			"  orchard query panes\n" +
			"  orchard query conversations\n" +
			"  orchard query claude-account\n" +
			"  orchard query host-services\n" +
			"  orchard query --raw 'query { health { status } }'",
	}
	var raw string
	cmd.Flags().StringVar(&raw, "raw", "", "raw GraphQL query string")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if raw == "" {
			return fmt.Errorf("provide a verb (e.g. `host`, `projects`, `processes`, `panes`, `conversations`, `claude-account`, `host-services`) or --raw '<graphql>'")
		}
		return runRaw(cmd.Context(), cmd.OutOrStdout(), raw)
	}
	cmd.AddCommand(hostCmd())
	cmd.AddCommand(projectsCmd())
	cmd.AddCommand(processesCmd())
	cmd.AddCommand(panesCmd())
	cmd.AddCommand(conversationsCmd())
	cmd.AddCommand(claudeAccountCmd())
	cmd.AddCommand(hostServicesCmd())
	cmd.AddCommand(contractsCmd())
	cmd.AddCommand(claudeInstancesCmd())
	return cmd
}

// projectsQuery is the GraphQL document fetched by `query projects`.
const projectsQuery = `{ projects { id directory name } }`

func projectsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "projects",
		Short: "List configured projects (Project nodes)",
		Long: "Calls the running daemon's GraphQL `projects` query and prints the\n" +
			"result as a JSON array. Returns the empty array when no projects are\n" +
			"configured. Use `orchard config add-repo PATH` to register a new one.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runProjects(cmd.Context(), cmd.OutOrStdout())
		},
	}
}

// runProjects POSTs the projects query, extracts the `data.projects`
// array, and prints it as pretty JSON. The shape is intentionally a
// plain `[]Project` (not the GraphQL envelope) so shell consumers can
// `jq` straight in without learning the envelope.
func runProjects(ctx context.Context, w io.Writer) error {
	raw, err := postGraphQL(ctx, projectsQuery)
	if err != nil {
		return err
	}
	var env struct {
		Data struct {
			Projects []map[string]any `json:"projects"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode response: %w (body: %s)", err, raw)
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("graphql error: %s", env.Errors[0].Message)
	}
	if env.Data.Projects == nil {
		env.Data.Projects = []map[string]any{}
	}
	out, err := json.MarshalIndent(env.Data.Projects, "", "  ")
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	_, _ = w.Write(out)
	_, _ = w.Write([]byte("\n"))
	return nil
}

// runRaw POSTs `query` to the daemon's /graphql endpoint and pretty-prints
// the JSON response. Used by both the root --raw escape hatch and named
// verbs that don't decode the response into a typed shape.
func runRaw(ctx context.Context, w io.Writer, query string) error {
	raw, err := postGraphQL(ctx, query)
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		// Fall back to raw bytes if the response wasn't JSON.
		_, _ = w.Write(raw)
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			_, _ = w.Write([]byte("\n"))
		}
		return nil
	}
	_, _ = w.Write(pretty.Bytes())
	_, _ = w.Write([]byte("\n"))
	return nil
}

// postGraphQLWithVars sends a GraphQL document with optional variables.
func postGraphQLWithVars(ctx context.Context, query string, variables map[string]any) ([]byte, error) {
	payload := map[string]any{"query": query}
	if len(variables) > 0 {
		payload["variables"] = variables
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resolveDaemonURL()+"/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon not running, start with `orchard daemon start` (%w)", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon returned %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// postGraphQL sends a GraphQL document to the daemon and returns the
// raw response body.
func postGraphQL(ctx context.Context, query string) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resolveDaemonURL()+"/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon not running, start with `orchard daemon start` (%w)", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon returned %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// SetDaemonURLForTest overrides the daemon URL. Tests that drive the
// CLI against an httptest.Server use this to redirect HTTP traffic
// without monkey-patching net/http.
func SetDaemonURLForTest(u string) func() {
	prev := daemonURL
	daemonURL = u
	return func() { daemonURL = prev }
}
