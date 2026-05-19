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

// TestContracts_E2E_LifecycleAndMidTestDeliver walks one synthetic
// contract from creation through delivery, asserts the GraphQL surface
// returns the right status, then appends a second "delivered" event and
// waits for the watcher to push an invalidation (idempotent re-delivery
// confirms the watch loop still fires).
//
// All fixtures are generic: agent-1 / agent-2 / session-1 — no real
// PII per the briefing's NO PII rule.
func TestContracts_E2E_LifecycleAndMidTestDeliver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dir := t.TempDir()
	logPath := contractFilePath(dir, happyContractID)

	// Seed the log with a happy-path lifecycle: created → delivered.
	writeEvents(t, logPath, lifecycleHappyPathEvents()...)

	provider := contracts.NewWithPath(dir, nil)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("start contracts provider: %v", err)
	}
	defer func() { _ = provider.Stop() }()

	srv := newTestDaemon(t, provider)
	defer srv.Close()

	// AC: GraphQL returns the delivered contract.
	resp := postQuery(t, srv.URL, fullContractQuery)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	if got := len(resp.Data.Contracts); got != 1 {
		t.Fatalf("contracts count = %d, want 1", got)
	}
	c := resp.Data.Contracts[0]
	if c.Status != "DELIVERED" {
		t.Errorf("status = %q, want DELIVERED", c.Status)
	}
	if c.ContractID != happyContractID {
		t.Errorf("contractId = %q, want %q", c.ContractID, happyContractID)
	}
	if c.Summary != happySummary {
		t.Errorf("summary = %q, want %q", c.Summary, happySummary)
	}
	if c.OwnerSessionID != "orchard:claude:session-1" {
		t.Errorf("ownerSessionId = %q, want orchard:claude:session-1", c.OwnerSessionID)
	}

	// Subscribe so we can observe a follow-up watcher push.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	sub := provider.Subscribe(subCtx)

	// Append a re-deliver event mid-test to trigger the watcher.
	appendEvent(t, logPath, v7UpdateEvent(happyContractID, "delivered",
		mustParseTime(t, "2026-05-04T13:30:00Z")))

	if err := waitForSubscriberEvent(sub, happyContractID, 5*time.Second); err != nil {
		t.Fatalf("waiting for invalidation: %v", err)
	}

	// AC: GraphQL still shows DELIVERED after the re-deliver event.
	if err := waitForStatus(t, srv.URL, happyContractID, "DELIVERED", 5*time.Second); err != nil {
		t.Fatalf("status never stayed delivered: %v", err)
	}
}

// TestContracts_E2E_MissingFileEmpty asserts the daemon answers a
// `contracts {}` query with an empty list — and no error — when the
// JSONL file does not exist yet.
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
// with a status filter. One OPEN contract, one DELIVERED contract.
func TestContracts_E2E_StatusFilter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dir := t.TempDir()

	openID := "C-2026-05-04-deadbeef"
	deliveredID := "C-2026-05-04-cafef00d"
	t0 := mustParseTime(t, "2026-05-04T12:00:00Z")
	t1 := t0.Add(30 * time.Minute)

	writeEvents(t, contractFilePath(dir, openID),
		v7CreationEvent(openID, "open thing", "orchard:claude:session-2", t0),
	)
	writeEvents(t, contractFilePath(dir, deliveredID),
		v7CreationEvent(deliveredID, "done thing", "orchard:claude:session-1", t0),
		v7UpdateEvent(deliveredID, "delivered", t1),
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

	// Filter for DELIVERED only.
	deliveredResp := postQuery(t, srv.URL, queryWithFilter(`{statuses: [DELIVERED]}`))
	if got := len(deliveredResp.Data.Contracts); got != 1 {
		t.Fatalf("DELIVERED filter count = %d, want 1", got)
	}
	if deliveredResp.Data.Contracts[0].ContractID != deliveredID {
		t.Errorf("DELIVERED filter returned %q, want %q", deliveredResp.Data.Contracts[0].ContractID, deliveredID)
	}

	// Filter by owner session id.
	ownerResp := postQuery(t, srv.URL, queryWithFilter(`{ownerSessionId: "orchard:claude:session-1"}`))
	if got := len(ownerResp.Data.Contracts); got != 1 {
		t.Fatalf("ownerSession filter count = %d, want 1", got)
	}
	if ownerResp.Data.Contracts[0].ContractID != deliveredID {
		t.Errorf("ownerSession filter returned %q, want %q", ownerResp.Data.Contracts[0].ContractID, deliveredID)
	}

	// Filter by ownerContains substring.
	containsResp := postQuery(t, srv.URL, queryWithFilter(`{ownerContains: "session-2"}`))
	if got := len(containsResp.Data.Contracts); got != 1 {
		t.Fatalf("ownerContains filter count = %d, want 1", got)
	}
	if containsResp.Data.Contracts[0].ContractID != openID {
		t.Errorf("ownerContains filter returned %q, want %q", containsResp.Data.Contracts[0].ContractID, openID)
	}

	// Single-contract lookup by id.
	one := postQuery(t, srv.URL, fmt.Sprintf(`query { contract(id: %q) { contractId status } }`, deliveredID))
	if one.Data.Contract == nil {
		t.Fatalf("contract(id) returned nil for known id")
	}
	if one.Data.Contract.ContractID != deliveredID {
		t.Errorf("contract(id) returned %q, want %q", one.Data.Contract.ContractID, deliveredID)
	}

	// Single-contract lookup with unknown id returns nil with no errors.
	none := postQuery(t, srv.URL, `query { contract(id: "C-1234-99-99-deadbeef") { contractId } }`)
	if none.Data.Contract != nil {
		t.Errorf("contract(id) for unknown id returned %+v, want nil", none.Data.Contract)
	}
}

// happyContractID and happySummary back the happy-path fixtures so a
// single source of truth drives both the writer and the assertions.
const (
	happyContractID = "C-2026-05-04-aaaa1111"
	happySummary    = "Stand up provider X"
)

// lifecycleHappyPathEvents is the canonical happy-path series:
// creation → delivered.
func lifecycleHappyPathEvents() []map[string]any {
	t0, _ := time.Parse(time.RFC3339, "2026-05-04T12:00:00Z")
	t1 := t0.Add(30 * time.Minute)
	return []map[string]any{
		v7CreationEvent(happyContractID, happySummary, "orchard:claude:session-1", t0),
		v7UpdateEvent(happyContractID, "delivered", t1),
	}
}

// v7CreationEvent returns a flat v0.7 creation event map.
func v7CreationEvent(id, summary, owner string, at time.Time) map[string]any {
	return map[string]any{
		"timestamp":   at.UTC().Format(time.RFC3339Nano),
		"contract_id": id,
		"status":      "started",
		"summary":     summary,
		"reasoning":   "contract filed",
		"owner":       owner,
		"created_by":  "test-agent",
		"source":      nil,
	}
}

// v7UpdateEvent returns a flat v0.7 update event map (null summary,
// null owner — both inherit from prior state).
func v7UpdateEvent(id, status string, at time.Time) map[string]any {
	return map[string]any{
		"timestamp":   at.UTC().Format(time.RFC3339Nano),
		"contract_id": id,
		"status":      status,
		"summary":     nil,
		"reasoning":   "status updated",
		"owner":       nil,
		"created_by":  "test-agent",
		"source":      nil,
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
    summary
    ownerSessionId
    ownerAgentName
    status
    reasoning
    createdBy
    source
    createdAt
    updatedAt
    lastEventAt
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
	ID             string  `json:"id"`
	ContractID     string  `json:"contractId"`
	Summary        string  `json:"summary"`
	OwnerSessionID string  `json:"ownerSessionId"`
	OwnerAgentName string  `json:"ownerAgentName"`
	Status         string  `json:"status"`
	Reasoning      string  `json:"reasoning"`
	CreatedBy      string  `json:"createdBy"`
	Source         *string `json:"source,omitempty"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
	LastEventAt    string  `json:"lastEventAt"`
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

// mustParseTime is the test-side time parser.
func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}
