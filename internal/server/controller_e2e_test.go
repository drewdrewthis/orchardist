package server_test

// Workstream C end-to-end test — boots the full daemon (HTTP + GraphQL
// + WebSocket) over a real httptest.Server and exercises:
//
//  1. Query.node(id) over four different node types (Host, Repo,
//     Worktree, Process).
//  2. Subscription.processes pushes data when the ps provider's cache
//     invalidates (websocket transport + provider channel + emit
//     goroutine all wired).
//
// DataLoader batch-count assertions live in
// internal/server/loaders/loaders_test.go where we can construct
// loaders directly and observe the per-request counters. Tmux-node
// resolution lives in internal/server/providers/tmux/tmux_e2e_test.go
// against a real tmux server.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	psprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
)

const probeTimeout = 5 * time.Second

// fixedReposLister is a tiny in-memory ReposLister so the controller
// test stays focused on the controller — the config provider has its
// own e2e elsewhere.
type fixedReposLister struct {
	rows []config.Repo
}

func (f *fixedReposLister) List(_ context.Context) ([]config.Repo, error) {
	return f.rows, nil
}

func TestController_E2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	hostProvider, machineID := startHostProvider(t, ctx)

	gitProvider, projectID, worktreePath := setupGitProvider(t)
	defer gitProvider.Stop()

	repos := &fixedReposLister{
		rows: []config.Repo{{
			ID:   config.RepoID(projectID),
			Slug: projectID,
			Path: worktreePath,
		}},
	}

	psRunner := newSeededPsRunner()
	psRunner.Replace(50, 1000)
	psProvider := psprovider.New(
		psprovider.NewAdapter(machineID).
			WithRunner(psRunner).
			WithPollInterval(time.Hour),
		slog.Default(),
	)
	if err := psProvider.Start(ctx); err != nil {
		t.Fatalf("ps Start: %v", err)
	}

	srv := server.New("", slog.Default(),
		server.WithRepos(repos),
		server.WithGit(gitProvider),
		server.WithPS(psProvider),
	)
	srv.Resolver().HostProvider = hostProvider

	ts := httptest.NewServer(srv.GraphQLHandler())
	defer ts.Close()

	t.Run("Query.node resolves multiple node types", func(t *testing.T) {
		hostNodeID := "Host:" + machineID
		projectNodeID := projectID
		worktreeNodeID := projectID + ":main"
		processNodeID := psprovider.ProcessID{Host: machineID, PID: 1000}.String()

		cases := []struct {
			id      string
			typeKey string
		}{
			{hostNodeID, "Host"},
			{projectNodeID, "Repo"},
			{worktreeNodeID, "Worktree"},
			{processNodeID, "Process"},
		}
		for _, c := range cases {
			c := c
			t.Run(c.typeKey, func(t *testing.T) {
				resp := postQuery(t, ts.URL, fmt.Sprintf(`query {
					node(id: %q) { id __typename }
				}`, c.id))
				if len(resp.Errors) > 0 {
					t.Fatalf("graphql errors: %+v", resp.Errors)
				}
				node := resp.Data["node"]
				if node == nil {
					t.Fatalf("node(%q) returned nil", c.id)
				}
				m, ok := node.(map[string]any)
				if !ok {
					t.Fatalf("node payload not a map: %T", node)
				}
				if got := m["__typename"]; got != c.typeKey {
					t.Errorf("__typename = %v, want %s", got, c.typeKey)
				}
				if got := m["id"]; got != c.id {
					t.Errorf("id = %v, want %s", got, c.id)
				}
			})
		}
	})

	t.Run("Query.node returns null for foreign-host id", func(t *testing.T) {
		resp := postQuery(t, ts.URL, `query { node(id: "Host:foreign-machine-id") { id __typename } }`)
		if len(resp.Errors) > 0 {
			t.Fatalf("graphql errors: %+v", resp.Errors)
		}
		if resp.Data["node"] != nil {
			t.Errorf("expected nil for foreign host, got %+v", resp.Data["node"])
		}
	})

	t.Run("Subscription.processes pushes on invalidation", func(t *testing.T) {
		wsCtx, wsCancel := context.WithTimeout(ctx, probeTimeout)
		defer wsCancel()

		conn, ack := openSubscription(t, wsCtx, ts.URL)
		defer func() { _ = conn.Close() }()

		const subID = "sub-processes"
		startSubscription(t, conn, subID, `subscription { processes { id pid command } }`)

		// Give the subscription goroutine a moment to call Subscribe()
		// on the provider before we trigger an invalidation; otherwise
		// the fan-out happens before any subscriber is registered.
		time.Sleep(100 * time.Millisecond)

		// Trigger an invalidation: change the canned ps output, force
		// a refresh. The provider's diff-based broadcast pushes
		// events to all current subscribers.
		psRunner.Replace(50, 2000)
		if err := psProvider.Refresh(wsCtx); err != nil {
			t.Fatalf("ps Refresh: %v", err)
		}

		got := waitForSubscriptionData(t, wsCtx, conn, subID)
		if got == "" {
			t.Fatalf("subscription produced no data within %s", probeTimeout)
		}
		if !strings.Contains(got, machineID) {
			t.Errorf("unexpected subscription payload: %s", got)
		}
		_ = ack
	})
}

// startHostProvider wires the real host provider so the controller
// resolver has something to dispatch Host node lookups against.
func startHostProvider(t *testing.T, ctx context.Context) (*host.Provider, string) {
	t.Helper()
	provider := host.New()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("start host provider: %v", err)
	}
	return provider, string(provider.LocalID())
}

// setupGitProvider creates a one-project git provider whose worktree
// path lives under t.TempDir(). We `git init` the directory and write
// an initial commit so the git provider can read HEAD.
func setupGitProvider(t *testing.T) (*gitprovider.Provider, string, string) {
	t.Helper()
	dir := t.TempDir()
	gitInitWithInitialCommit(t, dir)
	provider := gitprovider.NewProvider(nil)
	const projectID = "demo"
	if err := provider.AddProject(gitprovider.Project{ID: projectID, Dir: dir}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return provider, projectID, dir
}

// gitInitWithInitialCommit shells out to `git` to initialise dir as a
// repo with one commit. Production code never shells out; setup may
// per the briefing.
func gitInitWithInitialCommit(t *testing.T, dir string) {
	t.Helper()
	must := func(name string, args ...string) {
		t.Helper()
		out, err := runIn(dir, name, args...)
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	must("git", "init", "--initial-branch=main")
	must("git", "config", "user.email", "alice@example.com")
	must("git", "config", "user.name", "Alice")
	must("git", "commit", "--allow-empty", "-m", "init")
}

// runIn runs cmd args in dir and returns combined output.
func runIn(dir, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}

// seededPsRunner is a fake ps.CommandRunner — it returns synthetic
// `ps -ax` output the adapter can parse, plus a header-only response
// for FetchArgs / FetchCwds. Mutating `lines` and Refresh()ing the
// provider triggers an invalidation broadcast we can observe.
type seededPsRunner struct {
	mu     sync.Mutex
	header string
	lines  []string
}

func newSeededPsRunner() *seededPsRunner {
	return &seededPsRunner{header: "PID PPID USER TTY %CPU RSS STARTED COMMAND"}
}

// Replace swaps the synthetic dataset to `count` rows starting at
// `startPid`. Subsequent Refresh()es will diff against the prior set
// and broadcast invalidations for every changed key.
func (r *seededPsRunner) Replace(count, startPid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := make([]string, 0, count)
	for i := 0; i < count; i++ {
		pid := startPid + i
		lines = append(lines, fmt.Sprintf("%d 1 alice ?? 0.1 1024 Sun May  4 10:00:00 2026 synthetic-%d", pid, i))
	}
	r.lines = lines
}

func (r *seededPsRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name != "ps" {
		// FetchCwds shells out to lsof on macOS; an empty response
		// reads as "no cwd" rather than an error.
		return []byte(""), nil
	}
	for _, a := range args {
		if a == "-wwax" {
			return []byte("PID COMMAND\n"), nil
		}
	}
	body := r.header + "\n" + strings.Join(r.lines, "\n") + "\n"
	return []byte(body), nil
}

// graphQLResponse is a free-form GraphQL response wrapper.
type graphQLResponse struct {
	Data   map[string]any `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

func postQuery(t *testing.T, baseURL, query string) graphQLResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query})
	resp, err := http.Post(baseURL+"?", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(raw))
	}
	var out graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// openSubscription opens a websocket connection to the GraphQL
// endpoint and performs the graphql-transport-ws "connection_init" /
// "connection_ack" handshake.
func openSubscription(t *testing.T, ctx context.Context, baseURL string) (*websocket.Conn, []byte) {
	t.Helper()
	wsURL := strings.Replace(baseURL, "http://", "ws://", 1)
	dialer := websocket.Dialer{
		Subprotocols: []string{"graphql-transport-ws"},
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "connection_init"}); err != nil {
		t.Fatalf("connection_init: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read connection_ack: %v", err)
	}
	if !strings.Contains(string(msg), "connection_ack") {
		t.Fatalf("expected connection_ack, got: %s", string(msg))
	}
	return conn, msg
}

func startSubscription(t *testing.T, conn *websocket.Conn, id, query string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{
		"id":      id,
		"type":    "subscribe",
		"payload": map[string]any{"query": query},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
}

// waitForSubscriptionData reads from the websocket until it sees a
// "next" message for the given id, returning the raw JSON payload.
func waitForSubscriptionData(t *testing.T, ctx context.Context, conn *websocket.Conn, id string) string {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(probeTimeout)
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return ""
		}
		var env struct {
			ID      string          `json:"id"`
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if jsonErr := json.Unmarshal(raw, &env); jsonErr != nil {
			continue
		}
		if env.ID == id && env.Type == "next" {
			return string(env.Payload)
		}
		if env.Type == "complete" || env.Type == "error" {
			return string(raw)
		}
	}
}
