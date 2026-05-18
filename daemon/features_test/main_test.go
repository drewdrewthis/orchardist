// Package features_test wires godog Gherkin step definitions for the 24
// consumer-facing feature files under daemon/features/.
//
// Architecture:
//   - main_test.go          — godog runner + suite setup + shared helpers
//   - steps_daemon.go       — shared daemon lifecycle + health steps
//   - steps_query.go        — query-shape assertions
//   - steps_subscription.go — subscription push assertions
//   - steps_mutation.go     — mutation + response shape assertions
//   - steps_perf.go         — latency + coalescing budget assertions
//
// Every scenario runs against a real httptest.Server built from
// internal/server.New(). The GraphQL boundary is the test boundary (T4).
//
// Scenarios that exercise client-side projection logic (TUI Rust binary,
// orchard-gui Svelte stores, Houdini cache, Tauri bridge) are asserted at
// the daemon boundary only. Steps verifying pure UI logic are marked pending
// with a documented gap so failures surface as information, not crashes.
package daemonsteps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/gorilla/websocket"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// TestFeatures is the single godog entry point. Feature files are loaded
// manually (rather than via Paths) so we can pre-process any lines that
// break the Gherkin parser without modifying the consumer-spec source files.
func TestFeatures(t *testing.T) {
	features, err := loadFeatures("../features")
	if err != nil {
		t.Fatalf("load features: %v", err)
	}

	suite := godog.TestSuite{
		Name:                 "daemon-consumer-specs",
		ScenarioInitializer:  InitializeScenario,
		TestSuiteInitializer: InitializeSuite,
		Options: &godog.Options{
			Format:          "pretty",
			FeatureContents: features,
			Strict:          false, // pending steps do not fail the suite
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog: one or more feature scenarios failed — see output above for details")
	}
}

// loadFeatures reads all *.feature files from dir, applies the Gherkin
// compatibility shim (see fixGherkin), and returns them as godog.Feature values.
func loadFeatures(dir string) ([]godog.Feature, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("readdir %s: %w", dir, err)
	}
	var features []godog.Feature
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".feature") {
			continue
		}
		raw, err := os.ReadFile(dir + "/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		features = append(features, godog.Feature{
			Name:     e.Name(),
			Contents: fixGherkin(raw),
		})
	}
	return features, nil
}

// fixGherkin applies minimal source-compatible transformations to feature
// file content to make it parseable by the Gherkin parser without
// altering semantic meaning. Only applied to lines that would cause
// parser errors.
//
// Known issue: tui-federated-session-switcher.feature has a continuation
// line on a step (a parenthetical URL on its own indented line). Gherkin
// treats any non-blank, non-step, non-comment content at step indentation
// as a syntax error. We fold the continuation into the preceding step line.
func fixGherkin(src []byte) []byte {
	lines := strings.Split(string(src), "\n")
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// A "continuation line" is indented, non-empty, not starting with
		// a Gherkin keyword, and the previous line was a step.
		if i > 0 && trimmed != "" &&
			!isGherkinKeyword(trimmed) &&
			!strings.HasPrefix(trimmed, "#") &&
			!strings.HasPrefix(trimmed, "|") &&
			!strings.HasPrefix(trimmed, `"""`) &&
			len(out) > 0 &&
			isStepLine(strings.TrimSpace(out[len(out)-1])) {
			// Fold into the previous step line.
			out[len(out)-1] = out[len(out)-1] + " " + trimmed
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// gherkinKeywords are the Gherkin DSL keywords that start valid lines.
var gherkinKeywords = []string{
	"Feature:", "Background:", "Scenario:", "Scenario Outline:", "Scenario Template:",
	"Examples:", "Given ", "When ", "Then ", "And ", "But ", "Rule:", "*",
	"@", "#", "|", `"""`,
}

func isGherkinKeyword(s string) bool {
	for _, kw := range gherkinKeywords {
		if strings.HasPrefix(s, kw) {
			return true
		}
	}
	return false
}

var stepPrefixes = []string{"Given ", "When ", "Then ", "And ", "But "}

func isStepLine(s string) bool {
	for _, p := range stepPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// InitializeSuite runs once before all scenarios.
func InitializeSuite(ctx *godog.TestSuiteContext) {
	ctx.BeforeSuite(func() { slog.Info("daemon consumer spec suite starting") })
	ctx.AfterSuite(func() { slog.Info("daemon consumer spec suite finished") })
}

// InitializeScenario registers all step definitions and per-scenario lifecycle hooks.
func InitializeScenario(ctx *godog.ScenarioContext) {
	ts := &testState{}

	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		ts.reset()
		return nil, nil
	})
	ctx.After(func(_ context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		ts.teardown()
		return nil, err
	})

	registerDaemonSteps(ctx, ts)
	registerQuerySteps(ctx, ts)
	registerSubscriptionSteps(ctx, ts)
	registerMutationSteps(ctx, ts)
	registerPerfSteps(ctx, ts)
}

// ---------------------------------------------------------------------------
// testState — per-scenario shared state
// ---------------------------------------------------------------------------

// testState holds state threaded through all step implementations for a
// single scenario. A fresh instance is created (via reset()) for every
// scenario so scenarios are fully isolated.
type testState struct {
	httpServer    *httptest.Server
	serverCancel  context.CancelFunc // cancels background providers

	lastResponse map[string]any
	lastErrors   []map[string]any

	wsConn               *websocket.Conn
	subscriptionPayloads []string

	gitProvider  *gitprovider.Provider
	hostProvider *host.Provider

	requestDuration  time.Duration
	scenarioTempDir  string
	claudeProjTmpDir string
	repoSlug         string
}

func (ts *testState) reset() {
	*ts = testState{}
}

func (ts *testState) teardown() {
	if ts.wsConn != nil {
		_ = ts.wsConn.Close()
	}
	if ts.httpServer != nil {
		ts.httpServer.Close()
	}
	if ts.gitProvider != nil {
		ts.gitProvider.Stop()
	}
	// Cancel the server context so background provider goroutines exit.
	if ts.serverCancel != nil {
		ts.serverCancel()
	}
	if ts.scenarioTempDir != "" {
		_ = os.RemoveAll(ts.scenarioTempDir)
	}
	if ts.claudeProjTmpDir != "" {
		_ = os.RemoveAll(ts.claudeProjTmpDir)
	}
}

// ---------------------------------------------------------------------------
// Server construction helpers
// ---------------------------------------------------------------------------

// startMinimalServer builds a daemon httptest.Server with the host provider
// and a claudeprojects provider pointing at a temp dir. Extra providers can
// be added via opts. Idempotent.
//
// The host provider is NOT started (skips the top/vm_stat/sysctl subprocess
// calls) because no step asserts on host-load data. The provider still
// satisfies the HostProvider interface; health queries return an empty
// snapshot rather than erroring.
func (ts *testState) startMinimalServer(opts ...server.Option) error {
	if ts.httpServer != nil {
		return nil
	}
	hp := host.New()
	ts.hostProvider = hp

	// Empty temp dir so the claudeprojects provider starts cleanly with no
	// conversations but without returning "not initialised" errors.
	cpDir, err := os.MkdirTemp("", "orchard-cp-*")
	if err != nil {
		return fmt.Errorf("claudeprojects tmpdir: %w", err)
	}
	cp := claudeprojects.New(cpDir, "test-host", nil)
	ts.claudeProjTmpDir = cpDir

	ctx, cancel := context.WithCancel(context.Background())
	ts.serverCancel = cancel

	// Start claudeprojects provider (starts a lightweight fsnotify watcher).
	if err := cp.Start(ctx); err != nil {
		cancel()
		_ = os.RemoveAll(cpDir)
		return fmt.Errorf("claudeprojects start: %w", err)
	}

	srv := server.New("", slog.Default(), append(opts, server.WithClaudeProjects(cp))...)
	srv.Resolver().HostProvider = hp

	ts.httpServer = httptest.NewServer(srv.GraphQLHandler())
	return nil
}

// startServerWithRepo creates a tempdir git repo, wires git+repos providers,
// and starts the httptest.Server. Idempotent.
func (ts *testState) startServerWithRepo() error {
	if ts.httpServer != nil {
		return nil
	}
	dir, err := os.MkdirTemp("", "orchard-features-*")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	ts.scenarioTempDir = dir

	if err := gitInitWithCommit(dir); err != nil {
		return fmt.Errorf("git init: %w", err)
	}

	gp := gitprovider.NewProvider(nil)
	ts.repoSlug = "demo-repo"
	if err := gp.AddProject(gitprovider.Project{ID: ts.repoSlug, Dir: dir}); err != nil {
		return fmt.Errorf("AddProject: %w", err)
	}
	ts.gitProvider = gp

	repos := &fixedReposLister{rows: []config.Repo{{
		ID:   config.RepoID(ts.repoSlug),
		Slug: ts.repoSlug,
		Path: dir,
	}}}

	return ts.startMinimalServer(
		server.WithRepos(repos),
		server.WithGit(gp),
	)
}

// ---------------------------------------------------------------------------
// GraphQL helpers
// ---------------------------------------------------------------------------

// postQuery fires a GraphQL query and stores the result in ts.lastResponse /
// ts.lastErrors.
func (ts *testState) postQuery(query string) error {
	return ts.postQueryVars(query, nil)
}

// postQueryVars fires a GraphQL query with optional variables.
func (ts *testState) postQueryVars(query string, variables map[string]any) error {
	if ts.httpServer == nil {
		return fmt.Errorf("daemon not started — call a 'Given the daemon is running' step first")
	}
	payload := map[string]any{"query": query}
	if variables != nil {
		payload["variables"] = variables
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	start := time.Now()
	resp, err := http.Post(ts.httpServer.URL+"?", "application/json", bytes.NewReader(body))
	ts.requestDuration = time.Since(start)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("unmarshal: %w; raw=%s", err, string(raw))
	}
	ts.lastResponse = decoded

	if errs, ok := decoded["errors"]; ok {
		if errsSlice, ok := errs.([]any); ok {
			ts.lastErrors = make([]map[string]any, 0, len(errsSlice))
			for _, e := range errsSlice {
				if em, ok := e.(map[string]any); ok {
					ts.lastErrors = append(ts.lastErrors, em)
				}
			}
		}
	} else {
		ts.lastErrors = nil
	}
	return nil
}

// getDataAt navigates a dot-separated path into ts.lastResponse["data"].
// Returns (value, true) on success, (nil, false) if any segment is absent.
func (ts *testState) getDataAt(path string) (any, bool) {
	data, ok := ts.lastResponse["data"]
	if !ok {
		return nil, false
	}
	cur, ok := data.(map[string]any)
	if !ok {
		return nil, false
	}
	parts := strings.Split(path, ".")
	for i, part := range parts {
		val, exists := cur[part]
		if !exists {
			return nil, false
		}
		if i == len(parts)-1 {
			return val, true
		}
		next, ok := val.(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return nil, false
}

// hasNoGraphQLErrors returns an error if ts.lastErrors is non-empty.
func (ts *testState) hasNoGraphQLErrors() error {
	if len(ts.lastErrors) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(ts.lastErrors))
	for _, e := range ts.lastErrors {
		if m, ok := e["message"].(string); ok {
			msgs = append(msgs, m)
		}
	}
	return fmt.Errorf("unexpected GraphQL errors: %s", strings.Join(msgs, "; "))
}

// ---------------------------------------------------------------------------
// WebSocket helpers
// ---------------------------------------------------------------------------

// openWS opens a graphql-transport-ws connection and performs connection_init.
func (ts *testState) openWS() error {
	wsURL := strings.Replace(ts.httpServer.URL, "http://", "ws://", 1)
	dialer := websocket.Dialer{Subprotocols: []string{"graphql-transport-ws"}}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "connection_init"}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("connection_init: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		_ = conn.Close()
		return err
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("read connection_ack: %w", err)
	}
	if !strings.Contains(string(msg), "connection_ack") {
		_ = conn.Close()
		return fmt.Errorf("expected connection_ack, got: %s", msg)
	}
	ts.wsConn = conn
	return nil
}

// subscribeGQL sends a subscribe message on the open WS.
func (ts *testState) subscribeGQL(id, query string) error {
	return ts.wsConn.WriteJSON(map[string]any{
		"id":      id,
		"type":    "subscribe",
		"payload": map[string]any{"query": query},
	})
}

// waitForNext reads WS messages until a "next" message with id arrives or
// timeout elapses.
func (ts *testState) waitForNext(id string, timeout time.Duration) (string, error) {
	if err := ts.wsConn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}
	for {
		_, raw, err := ts.wsConn.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("ws read: %w", err)
		}
		var env struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if jsonErr := json.Unmarshal(raw, &env); jsonErr != nil {
			continue
		}
		if env.ID == id && env.Type == "next" {
			return string(raw), nil
		}
	}
}

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// fixedReposLister satisfies resolvers.ReposLister with a static slice.
type fixedReposLister struct {
	rows []config.Repo
}

func (f *fixedReposLister) List(_ context.Context) ([]config.Repo, error) {
	return f.rows, nil
}

// ensure fixedReposLister satisfies the interface at compile time.
var _ resolvers.ReposLister = (*fixedReposLister)(nil)

// gitInitWithCommit runs git init + initial empty commit so the git provider
// can read HEAD. Production code never shells out; fixture setup may.
func gitInitWithCommit(dir string) error {
	cmds := [][]string{
		{"git", "init", "--initial-branch=main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		out, err := runIn(dir, args[0], args[1:]...)
		if err != nil {
			return fmt.Errorf("%v: %w\n%s", args, err, out)
		}
	}
	return nil
}

// runIn executes name args in dir and returns combined output.
func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
