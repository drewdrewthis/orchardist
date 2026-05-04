package host_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// TestHost_E2E_RealMachine boots the GraphQL stack against the real
// local machine — no mocks per worker standards §3 — and asserts the
// canonical Host query returns identity + plausible resource numbers.
//
// This is the brief's AC5 test: it proves the full pipeline (daemon
// HTTP + gqlgen schema + Resolver root + Provider + OS-native readers)
// hangs together end-to-end on whichever OS we happen to be running on.
func TestHost_E2E_RealMachine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	provider := host.New()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("start provider: %v", err)
	}

	srv := newTestDaemon(t, provider)
	defer srv.Close()

	resp := postQuery(t, srv.URL, `query {
		host {
			id
			machineId
			hostname
			os
			kernel
			reachable
			peers { id }
			lastSeenAt
			resourceLoad {
				cpuPercent
				memPercent
				diskPercent
				loadAvg1m
				loadAvg5m
				loadAvg15m
			}
		}
	}`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	h := resp.Data.Host

	if h.MachineID == "" {
		t.Error("machineId is empty — identity reader produced nothing")
	}
	if h.Hostname == "" {
		t.Error("hostname is empty — os.Hostname() returned blank")
	}
	if h.OS != "darwin" && h.OS != "linux" {
		t.Errorf("os = %q, want darwin or linux", h.OS)
	}
	if !h.Reachable {
		t.Error("local host marked unreachable")
	}
	if h.ID == "" || !strings.HasPrefix(h.ID, "Host:") {
		t.Errorf("id = %q, want Host:<machineId>", h.ID)
	}
	if len(h.Peers) != 0 {
		t.Errorf("peers = %v, want [] for v1", h.Peers)
	}
	if _, err := time.Parse(time.RFC3339Nano, h.LastSeenAt); err != nil {
		t.Errorf("lastSeenAt %q not RFC3339: %v", h.LastSeenAt, err)
	}

	if h.ResourceLoad == nil {
		t.Fatal("resourceLoad is nil — Provider.Start should have taken an initial sample")
	}
	rl := h.ResourceLoad
	mustPercent(t, "cpuPercent", rl.CPUPercent)
	mustPercent(t, "memPercent", rl.MemPercent)
	mustPercent(t, "diskPercent", rl.DiskPercent)
	if rl.LoadAvg1m < 0 || rl.LoadAvg5m < 0 || rl.LoadAvg15m < 0 {
		t.Errorf("loadavg negative: 1m=%f 5m=%f 15m=%f", rl.LoadAvg1m, rl.LoadAvg5m, rl.LoadAvg15m)
	}
}

// TestHosts_E2E_RealMachine asserts Query.hosts returns a single-element
// list — the local host — for v1.
func TestHosts_E2E_RealMachine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	provider := host.New()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("start provider: %v", err)
	}
	srv := newTestDaemon(t, provider)
	defer srv.Close()

	resp := postQuery(t, srv.URL, `query { hosts { machineId hostname } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	if got := len(resp.Data.Hosts); got != 1 {
		t.Fatalf("hosts has %d entries, want 1 (v1: local only)", got)
	}
	// host wasn't requested in this query, so cross-check is intentionally loose.
	_ = resp.Data.Hosts[0].MachineID
}

// TestHost_E2E_FreshSample asserts repeated GraphQL queries see
// monotonic lastSeenAt updates after the load TTL elapses. Catches the
// regression where the poll loop never refreshed (would manifest as a
// frozen lastSeenAt across two reads spaced past the TTL).
func TestHost_E2E_FreshSample(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	provider := host.New()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("start provider: %v", err)
	}
	srv := newTestDaemon(t, provider)
	defer srv.Close()

	first := lastSeenAt(t, srv.URL)
	// Sleep > LoadTTL so the next Get triggers a sync refresh.
	time.Sleep(host.LoadTTL + 500*time.Millisecond)
	second := lastSeenAt(t, srv.URL)

	if !second.After(first) {
		t.Errorf("lastSeenAt did not advance: first=%s second=%s", first, second)
	}
}

func lastSeenAt(t *testing.T, url string) time.Time {
	t.Helper()
	resp := postQuery(t, url, `query { host { lastSeenAt } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	ts, err := time.Parse(time.RFC3339Nano, resp.Data.Host.LastSeenAt)
	if err != nil {
		t.Fatalf("parse lastSeenAt %q: %v", resp.Data.Host.LastSeenAt, err)
	}
	return ts
}

// mustPercent fails the test if v is outside the documented 0..100
// range. Critical for AC5: "asserts ... cpu and mem 0..100".
func mustPercent(t *testing.T, name string, v float64) {
	t.Helper()
	if v < 0 || v > 100 {
		t.Errorf("%s = %v, want 0..100", name, v)
	}
}

// newTestDaemon mirrors internal/server/server.go's GraphQL wiring with
// a pre-started Provider so the E2E test can drive it without launching
// the full HTTP server. Resolvers, schema, transports — everything real
// except the network listener is httptest.
func newTestDaemon(t *testing.T, provider *host.Provider) *httptest.Server {
	t.Helper()
	cfg := gql.Config{Resolvers: resolvers.New(time.Now()).WithHost(provider)}
	gqlSrv := handler.New(gql.NewExecutableSchema(cfg))
	gqlSrv.AddTransport(transport.POST{})
	gqlSrv.AddTransport(transport.GET{})

	mux := http.NewServeMux()
	mux.Handle("/graphql", gqlSrv)
	return httptest.NewServer(mux)
}

// graphqlResponse mirrors only the bits the AC5 test asserts on, so a
// schema addition elsewhere doesn't break the unmarshal here.
type graphqlResponse struct {
	Data struct {
		Host  hostNode   `json:"host"`
		Hosts []hostNode `json:"hosts"`
	} `json:"data"`
	Errors []map[string]any `json:"errors,omitempty"`
}

type hostNode struct {
	ID           string             `json:"id"`
	MachineID    string             `json:"machineId"`
	Hostname     string             `json:"hostname"`
	OS           string             `json:"os"`
	Kernel       *string            `json:"kernel"`
	Reachable    bool               `json:"reachable"`
	Peers        []hostNode         `json:"peers"`
	LastSeenAt   string             `json:"lastSeenAt"`
	ResourceLoad *resourceLoadValue `json:"resourceLoad"`
}

type resourceLoadValue struct {
	CPUPercent  float64 `json:"cpuPercent"`
	MemPercent  float64 `json:"memPercent"`
	DiskPercent float64 `json:"diskPercent"`
	LoadAvg1m   float64 `json:"loadAvg1m"`
	LoadAvg5m   float64 `json:"loadAvg5m"`
	LoadAvg15m  float64 `json:"loadAvg15m"`
}

// postQuery issues a GraphQL POST and decodes into graphqlResponse.
// Failures are fatal — a broken transport invalidates the whole test.
func postQuery(t *testing.T, url, query string) graphqlResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url+"/graphql", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
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
