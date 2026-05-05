package contracts_test

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

	"github.com/drewdrewthis/git-orchard-rs/internal/server/adapter"
	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/contracts"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// TestContracts_E2E_LifecycleAndMidTestCancel walks one synthetic
// contract from creation through the full happy-path lifecycle into
// `satisfied`, asserts the GraphQL surface returns the right status,
// then appends a `cancelled` event and waits for the watcher to push
// an invalidation. The follow-up GraphQL query must observe the new
// status.
//
// All fixtures are generic: agent-1 / agent-2 / session-1 — no real
// PII per the briefing's NO PII rule.
func TestContracts_E2E_LifecycleAndMidTestCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dir := t.TempDir()
	logPath := contractFilePath(dir, happyContractID)

	// Seed the log with a happy-path lifecycle: created → judge_run
	// (PASS) → status_change to delivered_pending_validation →
	// status_change to satisfied. Mirrors the live plugin's JSONL.
	writeEvents(t, logPath, lifecycleHappyPathEvents()...)

	provider := contracts.NewWithPath(dir, nil)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("start contracts provider: %v", err)
	}
	defer func() { _ = provider.Stop() }()

	srv := newTestDaemon(t, provider)
	defer srv.Close()

	// AC: GraphQL returns the satisfied contract.
	resp := postQuery(t, srv.URL, fullContractQuery)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	if got := len(resp.Data.Contracts); got != 1 {
		t.Fatalf("contracts count = %d, want 1", got)
	}
	c := resp.Data.Contracts[0]
	if c.Status != "SATISFIED" {
		t.Errorf("status = %q, want SATISFIED", c.Status)
	}
	if c.ContractID != happyContractID {
		t.Errorf("contractId = %q, want %q", c.ContractID, happyContractID)
	}
	if c.Statement != happyStatement {
		t.Errorf("statement = %q, want %q", c.Statement, happyStatement)
	}
	if c.OwnerSessionID != "session-1" {
		t.Errorf("ownerSessionId = %q, want session-1", c.OwnerSessionID)
	}
	if c.OwnerAgentName != "agent-1" {
		t.Errorf("ownerAgentName = %q, want agent-1", c.OwnerAgentName)
	}

	// Subscribe so we can observe the post-cancel invalidation.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	sub := provider.Subscribe(subCtx)

	// Append a cancel transition mid-test.
	appendEvent(t, logPath, statusChangeEvent(happyContractID,
		mustParseTime(t, "2026-05-04T13:30:00Z"),
		"satisfied", "cancelled", "drew_cancel"))

	if err := waitForSubscriberEvent(sub, happyContractID, 5*time.Second); err != nil {
		t.Fatalf("waiting for invalidation: %v", err)
	}

	// AC: GraphQL surfaces the cancelled status after the watcher tick.
	if err := waitForStatus(t, srv.URL, happyContractID, "CANCELLED", 5*time.Second); err != nil {
		t.Fatalf("status never moved to cancelled: %v", err)
	}
}

// TestContracts_E2E_MissingFileEmpty asserts the daemon answers a
// `contracts {}` query with an empty list — and no error — when the
// JSONL file does not exist yet. The watcher remains warm so a future
// creation event would be picked up.
func TestContracts_E2E_MissingFileEmpty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dir := filepath.Join(t.TempDir(), "intentionally-missing")

	provider := contracts.NewWithPath(dir, nil)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("start contracts provider: %v", err)
	}
	defer func() { _ = provider.Stop() }()

	srv := newTestDaemon(t, provider)
	defer srv.Close()

	resp := postQuery(t, srv.URL, fullContractQuery)
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	if got := len(resp.Data.Contracts); got != 0 {
		t.Errorf("contracts count = %d, want 0 for missing file", got)
	}
}

// TestContracts_E2E_StatusFilter exercises Query.contracts(filter)
// with a status filter — the AC `orchard query contracts --status
// started` ultimately routes through this surface.
func TestContracts_E2E_StatusFilter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dir := t.TempDir()

	// Two contracts: one open, one satisfied. Each lives in its own
	// per-contract jsonl file under the directory.
	openID := "C-2026-05-04-deadbeef"
	satID := "C-2026-05-04-cafef00d"
	t0 := mustParseTime(t, "2026-05-04T12:00:00Z")
	t1 := t0.Add(30 * time.Minute)
	writeEvents(t, contractFilePath(dir, openID),
		creationEvent(openID, "open thing", "agent-2", "session-2", t0),
	)
	writeEvents(t, contractFilePath(dir, satID),
		creationEvent(satID, "done thing", "agent-1", "session-1", t0),
		judgeRunEvent(satID, t0.Add(5*time.Minute), "PASS"),
		statusChangeEvent(satID, t0.Add(10*time.Minute), "open", "delivered_pending_validation", "owner_judge_pass"),
		statusChangeEvent(satID, t1, "delivered_pending_validation", "satisfied", "drew_approve"),
	)

	provider := contracts.NewWithPath(dir, nil)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("start contracts provider: %v", err)
	}
	defer func() { _ = provider.Stop() }()

	srv := newTestDaemon(t, provider)
	defer srv.Close()

	// All — both contracts.
	all := postQuery(t, srv.URL, fullContractQuery)
	if got := len(all.Data.Contracts); got != 2 {
		t.Fatalf("unfiltered count = %d, want 2", got)
	}

	// Filter for OPEN only.
	openResp := postQuery(t, srv.URL, queryWithFilter(`{statuses: [OPEN]}`))
	if got := len(openResp.Data.Contracts); got != 1 {
		t.Fatalf("OPEN filter count = %d, want 1", got)
	}
	if openResp.Data.Contracts[0].ContractID != openID {
		t.Errorf("OPEN filter returned %q, want %q", openResp.Data.Contracts[0].ContractID, openID)
	}

	// Filter by owner session id.
	ownerResp := postQuery(t, srv.URL, queryWithFilter(`{ownerSessionId: "session-1"}`))
	if got := len(ownerResp.Data.Contracts); got != 1 {
		t.Fatalf("ownerSession filter count = %d, want 1", got)
	}
	if ownerResp.Data.Contracts[0].ContractID != satID {
		t.Errorf("ownerSession filter returned %q, want %q", ownerResp.Data.Contracts[0].ContractID, satID)
	}

	// Single-contract lookup by id.
	one := postQuery(t, srv.URL, fmt.Sprintf(`query { contract(id: %q) { contractId status } }`, satID))
	if one.Data.Contract == nil {
		t.Fatalf("contract(id) returned nil for known id")
	}
	if one.Data.Contract.ContractID != satID {
		t.Errorf("contract(id) returned %q, want %q", one.Data.Contract.ContractID, satID)
	}

	// Single-contract lookup with unknown id returns nil with no errors.
	none := postQuery(t, srv.URL, `query { contract(id: "C-1234-99-99-deadbeef") { contractId } }`)
	if none.Data.Contract != nil {
		t.Errorf("contract(id) for unknown id returned %+v, want nil", none.Data.Contract)
	}
}

// happyContractID and happyStatement back the happy-path fixtures so a
// single source of truth drives both the writer and the assertions.
const (
	happyContractID = "C-2026-05-04-aaaa1111"
	happyStatement  = "Stand up provider X"
)

// lifecycleHappyPathEvents is the canonical happy-path series for
// happyContractID: creation → judge_run PASS → status to
// delivered_pending_validation → status to satisfied.
func lifecycleHappyPathEvents() []map[string]any {
	t0, _ := time.Parse(time.RFC3339, "2026-05-04T12:00:00Z")
	t1 := t0.Add(30 * time.Minute)
	t2 := t1.Add(5 * time.Minute)
	t3 := t2.Add(10 * time.Minute)
	return []map[string]any{
		creationEvent(happyContractID, happyStatement, "agent-1", "session-1", t0),
		judgeRunEvent(happyContractID, t1, "PASS"),
		statusChangeEvent(happyContractID, t2, "open", "delivered_pending_validation", "owner_judge_pass"),
		statusChangeEvent(happyContractID, t3, "delivered_pending_validation", "satisfied", "drew_approve"),
	}
}

// creationEvent returns a `kind: contract` row matching the live JSONL
// shape exactly (top-level fields embedded, owner / reports_to as
// nested objects, drew as the reports_to so the routing field
// surfaces).
func creationEvent(id, statement, agentName, sessionID string, at time.Time) map[string]any {
	return map[string]any{
		"kind":      "contract",
		"id":        id,
		"statement": statement,
		"owner": map[string]any{
			"session_id": sessionID,
			"agent_name": agentName,
			"vm_address": "test-host",
		},
		"reports_to": map[string]any{
			"kind":       "drew",
			"agent_name": nil,
			"vm_address": nil,
		},
		"parent_contract_id": nil,
		"child_contract_ids": []string{},
		"created_on":         at.UTC().Format(time.RFC3339Nano),
		"updated_on":         at.UTC().Format(time.RFC3339Nano),
		"closed_on":          nil,
		"closed_on_reason":   nil,
		"status":             "open",
		"judge_verdict":      nil,
		"evidence":           nil,
	}
}

// judgeRunEvent matches the live shape: by, evidence_links, kind,
// reason, timestamp, verdict.
func judgeRunEvent(id string, at time.Time, verdict string) map[string]any {
	return map[string]any{
		"kind":           "judge_run",
		"timestamp":      at.UTC().Format(time.RFC3339Nano),
		"by":             "judge",
		"verdict":        verdict,
		"reason":         "fixture run",
		"evidence_links": []string{},
		"id":             id,
	}
}

// statusChangeEvent matches the system-generated row shape: by, from,
// to, kind, timestamp, trigger.
func statusChangeEvent(id string, at time.Time, from, to, trigger string) map[string]any {
	return map[string]any{
		"kind":      "status_change",
		"timestamp": at.UTC().Format(time.RFC3339Nano),
		"by":        "system",
		"from":      from,
		"to":        to,
		"trigger":   trigger,
		"id":        id,
	}
}

// contractFilePath returns the per-contract jsonl path under dir,
// matching the layout the claude-contracts plugin writes
// (`<dir>/<contract-id>.jsonl`).
func contractFilePath(dir, contractID string) string {
	return filepath.Join(dir, contractID+".jsonl")
}

// writeEvents writes the given events as a freshly created JSONL file.
func writeEvents(t *testing.T, path string, events ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	for _, e := range events {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

// appendEvent appends a single event onto an existing JSONL file.
// Triggers an fsnotify Write the watcher will react to.
func appendEvent(t *testing.T, path string, ev map[string]any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("append open: %v", err)
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

// waitForSubscriberEvent blocks until an invalidation arrives whose key
// matches the expected contract id. Times out per the deadline.
func waitForSubscriberEvent(sub <-chan adapter.InvalidationEvent[contracts.ContractID], expected string, deadline time.Duration) error {
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-sub:
			if !ok {
				return fmt.Errorf("subscriber channel closed")
			}
			if string(ev.Key) == expected {
				return nil
			}
		case <-timer.C:
			return fmt.Errorf("timeout waiting for invalidation of %q", expected)
		}
	}
}

// waitForStatus polls the GraphQL surface until the named contract has
// the expected status, or returns a timeout error.
func waitForStatus(t *testing.T, url, contractID, expected string, deadline time.Duration) error {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		resp := postQuery(t, url, fmt.Sprintf(`query { contract(id: %q) { contractId status } }`, contractID))
		if resp.Data.Contract != nil && resp.Data.Contract.Status == expected {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("status never became %s within %v", expected, deadline)
}

// fullContractQuery is the projection used in the e2e tests. Mirrors
// the CLI subcommand's canonical query (kept in lockstep so any drift
// fails this test before it fails a user).
const fullContractQuery = `query {
  contracts {
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

// queryWithFilter splices a literal filter into the full contracts
// query — the e2e test exercises the CLI's same approach.
func queryWithFilter(filterLiteral string) string {
	return strings.Replace(`query { contracts(filter: $$F$$) { contractId status ownerSessionId } }`,
		"$$F$$", filterLiteral, 1)
}

// newTestDaemon mirrors internal/server/server.go's GraphQL wiring with
// a pre-started Provider so the e2e test can drive it without launching
// the full HTTP server.
//
// We construct a host provider too because the resolver root requires
// one to be non-nil — the host resolver is unused in this test but
// satisfies the dependency.
func newTestDaemon(t *testing.T, provider *contracts.Provider) *httptest.Server {
	t.Helper()
	hostProvider := host.New()
	if err := hostProvider.Start(context.Background()); err != nil {
		t.Fatalf("start host provider: %v", err)
	}
	cfg := gql.Config{Resolvers: resolvers.New(time.Now()).WithHost(hostProvider).WithContracts(provider)}
	gqlSrv := handler.New(gql.NewExecutableSchema(cfg))
	gqlSrv.AddTransport(transport.POST{})
	gqlSrv.AddTransport(transport.GET{})
	mux := http.NewServeMux()
	mux.Handle("/graphql", gqlSrv)
	return httptest.NewServer(mux)
}

// graphqlResponse decodes the e2e test's expected envelope. Only the
// fields we assert are spelled out; new schema additions cannot break
// this unmarshal.
type graphqlResponse struct {
	Data struct {
		Contracts []contractNode `json:"contracts"`
		Contract  *contractNode  `json:"contract,omitempty"`
	} `json:"data"`
	Errors []map[string]any `json:"errors,omitempty"`
}

type contractNode struct {
	ID               string             `json:"id"`
	ContractID       string             `json:"contractId"`
	Statement        string             `json:"statement"`
	OwnerSessionID   string             `json:"ownerSessionId"`
	OwnerAgentName   string             `json:"ownerAgentName"`
	ReportsTo        *string            `json:"reportsTo,omitempty"`
	ParentContractID *string            `json:"parentContractId,omitempty"`
	Status           string             `json:"status"`
	CreatedAt        string             `json:"createdAt"`
	UpdatedAt        string             `json:"updatedAt"`
	LastEventAt      string             `json:"lastEventAt"`
	Criteria         []string           `json:"criteria"`
	OpenQuestions    []contractQuestion `json:"openQuestions"`
}

type contractQuestion struct {
	QuestionID  string  `json:"questionId"`
	Text        string  `json:"text"`
	AskedBy     string  `json:"askedBy"`
	AskedAt     string  `json:"askedAt"`
	Deadline    *string `json:"deadline,omitempty"`
	BlocksClose bool    `json:"blocksClose"`
}

func postQuery(t *testing.T, url, query string) graphqlResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url+"/graphql", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, string(raw))
	}
	var out graphqlResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return out
}

// mustParseTime is the test-side time parser. Unlike fold_test.go's
// version (which is in package contracts), this one lives in
// contracts_test so we don't pull internal helpers across package
// boundaries.
func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}
