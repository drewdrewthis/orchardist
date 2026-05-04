// Package ps — end-to-end test for AC7.
//
// This test does what the briefing demands: real `ps`, real subprocess,
// real GraphQL handler hosted in `httptest.Server`. No mocks. The
// provider polls at 150ms so the test loop converges quickly.
//
// Shape:
//  1. Construct ps.Provider with a real PsAdapter on a 150ms tick.
//  2. Wire it through the gqlgen handler the daemon would wire.
//  3. Spawn `sleep 30`.
//  4. Wait for the provider to observe the new pid.
//  5. POST `query { host { processes(filter:{pidIn:[<pid>]}) { command pid cwd } } }`
//     and assert the spawned process is returned with the expected basename.
//  6. Kill the subprocess.
//  7. Wait for the provider's next tick to evict it.
//  8. Re-POST the same query and assert an empty result list.
package ps_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// TestPS_E2E_SpawnAndDisappear is the AC7 test: real subprocess + real ps
// + real graphql handler. macOS + Linux only — Windows lacks the `ps` we
// shell out to.
func TestPS_E2E_SpawnAndDisappear(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("ps e2e is darwin/linux only (no ps binary on %s)", runtime.GOOS)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Provider: real adapter, snappy poll interval so the test sees
	// spawn/exit transitions inside the deadline budget.
	provider := ps.New(ps.NewAdapter("local").WithPollInterval(150*time.Millisecond), nil)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("provider Start: %v", err)
	}

	// gqlgen handler with the resolver root pointed at our provider.
	resolver := resolvers.New(time.Now()).WithPS(provider)
	gqlSrv := handler.New(gql.NewExecutableSchema(gql.Config{Resolvers: resolver}))
	gqlSrv.AddTransport(transport.POST{})

	mux := http.NewServeMux()
	mux.Handle("/graphql", gqlSrv)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()
	graphqlURL := httpSrv.URL + "/graphql"

	// Spawn `sleep 30` — long enough that it's guaranteed alive across
	// the visibility-poll window. We always kill it before the test exits.
	cmd := exec.CommandContext(ctx, "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep subprocess: %v", err)
	}
	pid := cmd.Process.Pid
	cleanup := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	defer cleanup()

	// AC7 step 5: spawn must be visible in `host.processes`.
	if err := waitForProcessVisible(t, ctx, graphqlURL, pid, "sleep", 5*time.Second); err != nil {
		t.Fatalf("expected spawned pid %d to appear: %v", pid, err)
	}

	// AC7 step 6: kill the subprocess.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill sleep subprocess: %v", err)
	}
	_, _ = cmd.Process.Wait()

	// AC7 step 7-8: after the next poll cycle the provider must drop the
	// dead pid. The provider polls every 150ms, so a 5s budget is comfortable.
	if err := waitForProcessAbsent(t, ctx, graphqlURL, pid, 5*time.Second); err != nil {
		t.Fatalf("expected pid %d to disappear after kill: %v", pid, err)
	}
}

// processesByPidResponse mirrors the GraphQL envelope for the pidIn query.
type processesByPidResponse struct {
	Data struct {
		Host struct {
			Processes []struct {
				ID      string  `json:"id"`
				Pid     int     `json:"pid"`
				Command string  `json:"command"`
				Cwd     *string `json:"cwd"`
			} `json:"processes"`
		} `json:"host"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

// queryByPid hits the test daemon and returns whatever processes the
// resolver matched against the given pid filter.
func queryByPid(ctx context.Context, graphqlURL string, pid int) (processesByPidResponse, error) {
	const query = `query($pids:[Int!]) {
	  host { processes(filter: {pidIn: $pids}) { id pid command cwd } }
	}`
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": map[string]any{"pids": []int{pid}},
	})
	if err != nil {
		return processesByPidResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, bytes.NewReader(body))
	if err != nil {
		return processesByPidResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return processesByPidResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return processesByPidResponse{}, err
	}
	var out processesByPidResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return processesByPidResponse{}, err
	}
	return out, nil
}

// waitForProcessVisible polls the daemon until the spawned pid shows up
// (or the deadline elapses). The watcher tick is 150ms; we poll every
// 100ms which is fast enough not to miss the transition.
func waitForProcessVisible(t *testing.T, ctx context.Context, graphqlURL string, pid int, wantCommand string, budget time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		got, err := queryByPid(ctx, graphqlURL, pid)
		if err != nil {
			return err
		}
		for _, p := range got.Data.Host.Processes {
			if p.Pid == pid && p.Command == wantCommand {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("pid %d / command %q never appeared in graphql results within %s", pid, wantCommand, budget)
}

// waitForProcessAbsent polls until the pid is no longer in the resolver's
// list. After cmd.Process.Kill the provider's next tick should evict it.
func waitForProcessAbsent(t *testing.T, ctx context.Context, graphqlURL string, pid int, budget time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		got, err := queryByPid(ctx, graphqlURL, pid)
		if err != nil {
			return err
		}
		present := false
		for _, p := range got.Data.Host.Processes {
			if p.Pid == pid {
				present = true
				break
			}
		}
		if !present {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("pid %d still visible after kill within %s", pid, budget)
}
