package claudeprojects_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// TestConversation_E2E_OpenThenClosed boots the GraphQL stack against a
// temp dir containing one synthetic JSONL transcript and asserts the
// brief's three transitions:
//
//  1. Three records, fresh mtime → open=true, messageCount=3.
//  2. Append a fourth record → lastSeenAt advances, messageCount=4.
//  3. Stamp mtime backwards past the heartbeat → open=false.
//
// The transcript content is deliberately generic — no project names,
// no usernames, no real session text. That keeps the fixture safe to
// commit.
func TestConversation_E2E_OpenThenClosed(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "tmp-fixture-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	const sessionUUID = "00000000-1111-2222-3333-444444444444"
	jsonlPath := filepath.Join(projectDir, sessionUUID+".jsonl")

	now := time.Now().UTC().Round(time.Millisecond)
	t0 := now.Add(-3 * time.Second)
	writeRecords(t, jsonlPath, []recordFixture{
		{ts: t0, role: "user", body: "hello", cwd: projectDir},
		{ts: t0.Add(time.Second), role: "assistant", body: "ack"},
		{ts: t0.Add(2 * time.Second), role: "user", body: "next"},
	})

	provider, srv := bootDaemon(t, root)
	defer srv.Close()

	// Phase 1 — three records, fresh; expect open=true, messageCount=3.
	c := queryConversations(t, srv.URL)
	if len(c) != 1 {
		t.Fatalf("phase1: got %d conversations, want 1", len(c))
	}
	got := c[0]
	if got.SessionUUID != sessionUUID {
		t.Errorf("phase1: sessionUuid = %q, want %q", got.SessionUUID, sessionUUID)
	}
	if got.MessageCount != 3 {
		t.Errorf("phase1: messageCount = %d, want 3", got.MessageCount)
	}
	if !got.Open {
		t.Errorf("phase1: open = false, want true (mtime is fresh)")
	}
	if got.Cwd == nil || *got.Cwd != projectDir {
		t.Errorf("phase1: cwd = %v, want %q", got.Cwd, projectDir)
	}
	if got.FirstSeenAt == nil {
		t.Fatalf("phase1: firstSeenAt is null")
	}
	if got.LastSeenAt == nil {
		t.Fatalf("phase1: lastSeenAt is null")
	}
	firstAt := *got.LastSeenAt

	// Phase 2 — append one record; expect lastSeenAt to advance.
	t1 := t0.Add(3 * time.Second)
	appendRecord(t, jsonlPath, recordFixture{ts: t1, role: "assistant", body: "more"})
	// Force a refresh through the cache: stop the watcher race by
	// asking the provider to reload manually. The watcher is also
	// running, but tests must not depend on fsnotify timing.
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatalf("phase2: refresh: %v", err)
	}

	c = queryConversations(t, srv.URL)
	if len(c) != 1 {
		t.Fatalf("phase2: got %d conversations, want 1", len(c))
	}
	got = c[0]
	if got.MessageCount != 4 {
		t.Errorf("phase2: messageCount = %d, want 4", got.MessageCount)
	}
	if got.LastSeenAt == nil {
		t.Fatalf("phase2: lastSeenAt is null")
	}
	if !got.LastSeenAt.After(firstAt) {
		t.Errorf("phase2: lastSeenAt = %v, want > %v", got.LastSeenAt, firstAt)
	}

	// Phase 3 — stamp mtime backwards beyond the heartbeat.
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(jsonlPath, old, old); err != nil {
		t.Fatalf("phase3: chtimes: %v", err)
	}
	// Also rewrite the file to push the last record's timestamp
	// backwards — `open` is computed from the last record's
	// timestamp, not the file's mtime, so a lone Chtimes is not
	// sufficient. We replace the contents with three records all
	// timestamped well before the heartbeat.
	writeRecords(t, jsonlPath, []recordFixture{
		{ts: old, role: "user", body: "old hello", cwd: projectDir},
		{ts: old.Add(time.Second), role: "assistant", body: "old ack"},
		{ts: old.Add(2 * time.Second), role: "user", body: "old next"},
	})
	if err := provider.Refresh(context.Background()); err != nil {
		t.Fatalf("phase3: refresh: %v", err)
	}

	c = queryConversations(t, srv.URL)
	if len(c) != 1 {
		t.Fatalf("phase3: got %d conversations, want 1", len(c))
	}
	got = c[0]
	if got.Open {
		t.Errorf("phase3: open = true, want false (last record is %v ago)",
			time.Since(*got.LastSeenAt))
	}
}

// TestConversation_Lookup_ByID asserts Query.conversation(id) round-
// trips a stable id and returns nil for unknown ids.
func TestConversation_Lookup_ByID(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "tmp-fixture-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	const sessionUUID = "abcd1234-0000-0000-0000-000000000000"
	jsonlPath := filepath.Join(projectDir, sessionUUID+".jsonl")
	writeRecords(t, jsonlPath, []recordFixture{
		{ts: time.Now().UTC(), role: "user", body: "hi", cwd: projectDir},
	})

	_, srv := bootDaemon(t, root)
	defer srv.Close()

	id := "Conversation:" + sessionUUID
	got := queryConversation(t, srv.URL, id)
	if got == nil {
		t.Fatalf("conversation(id=%q) returned nil", id)
	}
	if got.SessionUUID != sessionUUID {
		t.Errorf("conversation(id).sessionUuid = %q, want %q", got.SessionUUID, sessionUUID)
	}
	if got.MessageCount != 1 {
		t.Errorf("conversation(id).messageCount = %d, want 1", got.MessageCount)
	}

	if got := queryConversation(t, srv.URL, "Conversation:does-not-exist"); got != nil {
		t.Errorf("conversation(unknown) = %+v, want nil", got)
	}
	if got := queryConversation(t, srv.URL, "Garbage:no-prefix"); got != nil {
		t.Errorf("conversation(garbage) = %+v, want nil", got)
	}
}

// TestConversation_E2E_EmptyRoot asserts a daemon booted with an empty
// projects root returns conversations: [].
func TestConversation_E2E_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	_, srv := bootDaemon(t, root)
	defer srv.Close()

	c := queryConversations(t, srv.URL)
	if len(c) != 0 {
		t.Errorf("got %d conversations from empty root, want 0", len(c))
	}
}

// TestConversation_RecapAlwaysNull is the v1 contract assertion — the
// resolver must not surface recap content; the conversations plugin
// will.
func TestConversation_RecapAlwaysNull(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "tmp-fixture-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	const sessionUUID = "11111111-2222-3333-4444-555555555555"
	jsonlPath := filepath.Join(projectDir, sessionUUID+".jsonl")
	writeRecords(t, jsonlPath, []recordFixture{
		{ts: time.Now().UTC(), role: "user", body: "hello", cwd: projectDir},
	})

	_, srv := bootDaemon(t, root)
	defer srv.Close()

	resp := postQuery(t, srv.URL, `query { conversations { id recap } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	if len(resp.Data.Conversations) != 1 {
		t.Fatalf("got %d conversations, want 1", len(resp.Data.Conversations))
	}
	if resp.Data.Conversations[0].Recap != nil {
		t.Errorf("recap = %v, want nil per v1 contract", resp.Data.Conversations[0].Recap)
	}
}

// TestConversation_JsonlPath_Integration is the AC1 integration test:
//
//	{ conversations { sessionUuid jsonlPath } } returns a non-empty
//	absolute path for every conversation, and every path resolves to a
//	readable regular file on the daemon's host.
func TestConversation_JsonlPath_Integration(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "test-project")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	const sessionUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	jsonlPath := filepath.Join(projectDir, sessionUUID+".jsonl")
	writeRecords(t, jsonlPath, []recordFixture{
		{ts: time.Now().UTC(), role: "user", body: "hello", cwd: projectDir},
	})

	_, srv := bootDaemon(t, root)
	defer srv.Close()

	resp := postQuery(t, srv.URL, `query { conversations { sessionUuid jsonlPath } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	if len(resp.Data.Conversations) == 0 {
		t.Fatal("no conversations returned; expected at least one")
	}

	for _, c := range resp.Data.Conversations {
		if c.JsonlPath == "" {
			t.Errorf("conversation %q: jsonlPath is empty", c.SessionUUID)
			continue
		}
		if !filepath.IsAbs(c.JsonlPath) {
			t.Errorf("conversation %q: jsonlPath %q is not absolute", c.SessionUUID, c.JsonlPath)
			continue
		}
		fi, err := os.Stat(c.JsonlPath)
		if err != nil {
			t.Errorf("conversation %q: jsonlPath %q: stat failed: %v", c.SessionUUID, c.JsonlPath, err)
			continue
		}
		if !fi.Mode().IsRegular() {
			t.Errorf("conversation %q: jsonlPath %q is not a regular file (mode=%v)", c.SessionUUID, c.JsonlPath, fi.Mode())
		}
	}
}

// recordFixture is a tiny synthetic JSONL record. We intentionally do
// not match the full Claude Code schema — the provider only reads
// `timestamp` and `cwd`, so anything else is ignored. The body is
// generic (`hello`, `ack`, …) so the file is safe to commit and to
// surface in CI logs.
type recordFixture struct {
	ts   time.Time
	role string
	body string
	cwd  string
}

func (r recordFixture) marshal(t *testing.T) []byte {
	rec := map[string]any{
		"type":      r.role,
		"timestamp": r.ts.UTC().Format(time.RFC3339Nano),
		"sessionId": "fixture",
		"message": map[string]any{
			"role":    r.role,
			"content": r.body,
		},
	}
	if r.cwd != "" {
		rec["cwd"] = r.cwd
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return append(b, '\n')
}

// writeRecords replaces path with the given records, one per line.
func writeRecords(t *testing.T, path string, records []recordFixture) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	for _, r := range records {
		if _, err := f.Write(r.marshal(t)); err != nil {
			t.Fatalf("write record: %v", err)
		}
	}
}

// appendRecord adds one line to path without truncating.
func appendRecord(t *testing.T, path string, r recordFixture) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s for append: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(r.marshal(t)); err != nil {
		t.Fatalf("append record: %v", err)
	}
}

// bootDaemon constructs a Provider rooted at root, starts it, wires it
// to a gqlgen handler, and exposes that on an httptest.Server. The
// returned Provider is non-nil so tests can call helper methods like
// InvalidateAll between phases without depending on fsnotify timing.
//
// NOTE: This intentionally does NOT use server.New / server.Run — those
// bind to a fixed port and read $CLAUDE_PROJECTS_ROOT. Tests speak the
// same GraphQL the daemon does, but on an ephemeral httptest socket.
func bootDaemon(t *testing.T, root string) (*claudeprojects.Provider, *httptest.Server) {
	t.Helper()

	provider := claudeprojects.New(root, "test-host", nil)
	if err := provider.Start(context.Background()); err != nil {
		t.Fatalf("start provider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Stop() })

	cfg := gql.Config{Resolvers: resolvers.New(time.Now()).WithClaudeProjects(provider)}
	hh := handler.New(gql.NewExecutableSchema(cfg))
	hh.AddTransport(transport.POST{})
	hh.AddTransport(transport.GET{})

	mux := http.NewServeMux()
	mux.Handle("/graphql", hh)
	srv := httptest.NewServer(mux)
	return provider, srv
}

// graphqlResponse is the projected shape we deserialize from the
// daemon's JSON reply. We model only the fields the tests assert
// against; gqlgen's responses include surrounding noise (extensions,
// path) we ignore.
type graphqlResponse struct {
	Data   graphqlData `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type graphqlData struct {
	Conversations []*conversationDTO `json:"conversations"`
	Conversation  *conversationDTO   `json:"conversation"`
}

type conversationDTO struct {
	ID           string     `json:"id"`
	SessionUUID  string     `json:"sessionUuid"`
	Cwd          *string    `json:"cwd"`
	FirstSeenAt  *time.Time `json:"firstSeenAt"`
	LastSeenAt   *time.Time `json:"lastSeenAt"`
	MessageCount int64      `json:"messageCount"`
	Open         bool       `json:"open"`
	Recap        *string    `json:"recap"`
	JsonlPath    string     `json:"jsonlPath"`
}

// queryConversations issues the canonical Conversations query and
// fails the test on any GraphQL error. Returns the projected list.
func queryConversations(t *testing.T, baseURL string) []*conversationDTO {
	t.Helper()
	resp := postQuery(t, baseURL, `query {
		conversations {
			id
			sessionUuid
			cwd
			firstSeenAt
			lastSeenAt
			messageCount
			open
			recap
		}
	}`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	return resp.Data.Conversations
}

// queryConversation issues a Query.conversation(id) lookup and returns
// the single result (or nil for not-found).
func queryConversation(t *testing.T, baseURL, id string) *conversationDTO {
	t.Helper()
	q := fmt.Sprintf(`query { conversation(id: %q) { id sessionUuid messageCount } }`, id)
	resp := postQuery(t, baseURL, q)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	return resp.Data.Conversation
}

// postQuery is the GraphQL transport for tests. Identical shape to the
// CLI's runRaw but without pretty-printing.
func postQuery(t *testing.T, baseURL, query string) graphqlResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/graphql", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	var out graphqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}
