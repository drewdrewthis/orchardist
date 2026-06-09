// E2E coverage for the tmux provider — spins a real tmux server in a
// throwaway socket, drives it through the standard Adapter / Provider
// pipeline, and asserts the GraphQL surface returns what the user
// would expect.
//
// macOS-only assumptions: `tmux` is on PATH (CI installs it) and the
// `bash` interpreter is available for the dummy pane process. Linux
// CI satisfies both.

package tmux_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server"
	"github.com/drewdrewthis/orchardist/internal/server/providers/tmux"
)

// pollInterval is the test-time tick rate. Snappier than the 1s
// production default so the kill/disappear assertion does not push the
// total runtime past `go test -timeout` defaults.
const testPollInterval = 100 * time.Millisecond

// waitFor polls fn until it returns true or the context fires. Returns
// the last (false) value when the context dies. Used for the
// "session disappears after one poll cycle" assertion.
func waitFor(ctx context.Context, fn func() bool) bool {
	deadline, _ := ctx.Deadline()
	for {
		if fn() {
			return true
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// tmuxAvailable returns nil when `tmux` is on PATH; the test skips
// otherwise so a sandbox without tmux doesn't fail the suite.
func tmuxAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not on PATH; skipping E2E: %v", err)
	}
}

// startSandboxTmux launches a tmux server on a temporary socket so it
// cannot collide with the developer's primary tmux. Cleanup kills the
// server and removes the socket.
//
// Unix-domain socket paths are capped at ~104 bytes on macOS. `t.TempDir()`
// nests under `os.TempDir()` *and* the test name, easily blowing past
// the cap; we mkdir under /tmp directly so the path stays short.
func startSandboxTmux(t *testing.T) (socket string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "orchard-tmux-test-")
	if err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket = filepath.Join(dir, "s.sock")

	// Spawn the server with a single dummy session so list-sessions
	// returns at least one row immediately. `tmux new-session -d`
	// daemonises the server; the -t target is unused.
	args := []string{
		"-S", socket,
		"new-session", "-d", "-s", "alpha",
		"-x", "80", "-y", "24",
		"bash", "-c", "while true; do sleep 1; done",
	}
	cmd := exec.Command("tmux", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start sandbox tmux: %v: %s", err, out)
	}

	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
	})
	return socket
}

// killSession kills a session by name on the sandbox socket. Helper
// keeps the assertion site readable.
func killSession(t *testing.T, socket, session string) {
	t.Helper()
	out, err := exec.Command("tmux", "-S", socket, "kill-session", "-t", session).CombinedOutput()
	if err != nil {
		t.Fatalf("kill session %q: %v: %s", session, err, out)
	}
}

// graphQLPost issues a single GraphQL POST and returns the decoded
// envelope so the caller can dive into `data`/`errors` without
// duplicating the HTTP boilerplate.
func graphQLPost(t *testing.T, base, query string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(base+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errs, ok := env["errors"]; ok && errs != nil {
		// Promote graphql errors so the test fails loudly with the
		// resolver message instead of a confusing nil-deref.
		t.Fatalf("graphql errors: %v", errs)
	}
	return env
}

// TestEndToEnd_SessionAppearsAndDisappears is the headline AC test.
// Spin a sandbox tmux, walk it through the provider, serve it through
// httptest, then kill the session and watch the cache evict it.
func TestEndToEnd_SessionAppearsAndDisappears(t *testing.T) {
	tmuxAvailable(t)

	socket := startSandboxTmux(t)
	host := tmux.HostID("sandbox")

	adapter := tmux.NewAdapter(host).WithSocket(socket)
	provider := tmux.New(adapter, nil).WithPollInterval(testPollInterval)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("provider start: %v", err)
	}

	srv := server.New("", nil, server.WithTmux(provider))
	httpSrv := httptest.NewServer(srv.GraphQLHandler())
	t.Cleanup(httpSrv.Close)

	// 1. Session "alpha" should be visible — we created it in the
	//    sandbox tmux above.
	env := graphQLPost(t, httpSrv.URL, `{ tmuxSessions { name } }`)
	names := extractSessionNames(t, env)
	if !contains(names, "alpha") {
		t.Fatalf("expected 'alpha' in sessions; got %v", names)
	}

	// 2. Server entry point returns Alive=true with our socket path.
	env = graphQLPost(t, httpSrv.URL, `{ tmuxServer { id alive socketPath } }`)
	srvData := env["data"].(map[string]any)["tmuxServer"].(map[string]any)
	if alive, _ := srvData["alive"].(bool); !alive {
		t.Fatalf("tmuxServer.alive false: %v", srvData)
	}
	if got, _ := srvData["socketPath"].(string); got != socket {
		t.Fatalf("socketPath: want %q, got %q", socket, got)
	}

	// 3. Add another session, force a refresh, observe the new entry.
	add := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", "beta",
		"bash", "-c", "while true; do sleep 1; done")
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("create beta: %v: %s", err, out)
	}
	if err := provider.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	env = graphQLPost(t, httpSrv.URL, `{ tmuxSessions { name } }`)
	if names = extractSessionNames(t, env); !contains(names, "beta") {
		t.Fatalf("expected 'beta' after create; got %v", names)
	}

	// 4. Pane filtering — the bash dummy should be visible per-session.
	env = graphQLPost(t, httpSrv.URL,
		`{ tmuxPanes(filter: { sessionIn: ["alpha"] }) { paneId currentCommand } }`)
	panes := mustList(t, env, "tmuxPanes")
	if len(panes) == 0 {
		t.Fatalf("expected at least one alpha pane")
	}
	for _, p := range panes {
		if !strings.HasPrefix(p["paneId"].(string), "%") {
			t.Fatalf("pane id should begin with %%: %v", p)
		}
	}

	// 5. Kill alpha and watch it disappear after one poll cycle.
	killSession(t, socket, "alpha")
	gone := waitFor(ctx, func() bool {
		env := graphQLPost(t, httpSrv.URL, `{ tmuxSessions { name } }`)
		return !contains(extractSessionNames(t, env), "alpha")
	})
	if !gone {
		t.Fatalf("alpha did not disappear after kill")
	}
}

// TestProvider_NoTmux verifies the resolver gracefully reports a dead
// server when the socket path points at no daemon.
func TestProvider_NoTmux(t *testing.T) {
	tmuxAvailable(t)

	dir, err := os.MkdirTemp("/tmp", "orchard-tmux-dead-")
	if err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dead := filepath.Join(dir, "x.sock")

	adapter := tmux.NewAdapter("solo").WithSocket(dead)
	provider := tmux.New(adapter, nil).WithPollInterval(testPollInterval)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("provider start: %v", err)
	}

	srv := server.New("", nil, server.WithTmux(provider))
	httpSrv := httptest.NewServer(srv.GraphQLHandler())
	t.Cleanup(httpSrv.Close)

	// tmuxServer is nullable and should be null when the daemon is dead.
	body, err := json.Marshal(map[string]string{"query": `{ tmuxServer { id alive } }`})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(httpSrv.URL+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing data: %v", env)
	}
	if data["tmuxServer"] != nil {
		t.Fatalf("expected null tmuxServer, got %v", data["tmuxServer"])
	}
}

// ----------------------------------------------------------------------
// Tiny test helpers — purely local to the package.
// ----------------------------------------------------------------------

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func mustList(t *testing.T, env map[string]any, key string) []map[string]any {
	t.Helper()
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("envelope missing data: %v", env)
	}
	raw, ok := data[key].([]any)
	if !ok {
		t.Fatalf("data[%q] not list: %v", key, data[key])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("data[%q] item not object: %v", key, item)
		}
		out = append(out, m)
	}
	return out
}

func extractSessionNames(t *testing.T, env map[string]any) []string {
	t.Helper()
	list := mustList(t, env, "tmuxSessions")
	names := make([]string, 0, len(list))
	for _, s := range list {
		name, ok := s["name"].(string)
		if !ok {
			t.Fatalf("session missing name: %v", s)
		}
		names = append(names, name)
	}
	return names
}

// errorsAreNil keeps the bash linter happy — referenced from a path
// that may not always run, depending on how tmux behaves under load.
var _ = errors.Is
