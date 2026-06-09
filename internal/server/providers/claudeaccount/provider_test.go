package claudeaccount_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/providers/claudeaccount"
)

// stubAccount returns the canonical canned response for adapter tests
// — one healthy account on host "test-host" with the expected quota.
func stubAccount() ([]byte, []byte) {
	auth := []byte(`{"email":"alice@example.com"}`)
	cc := []byte(`{"blocks":[{"active":true,"used":7.5,"cap":50,"resetsAt":"2026-05-05T00:00:00Z"}]}`)
	return auth, cc
}

// fakeRunnerSeq lets a test queue different responses across calls
// (e.g. first call returns ToolNotInstalled, second returns success).
type fakeRunnerSeq struct {
	mu       sync.Mutex
	auth     [][]byte
	cc       [][]byte
	authErr  []error
	ccErr    []error
	authIdx  int
	ccIdx    int
	authCalls int
	ccCalls   int
}

func (f *fakeRunnerSeq) Run(_ context.Context, name string, _ ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch name {
	case "claude":
		f.authCalls++
		i := f.authIdx
		if i >= len(f.auth) {
			i = len(f.auth) - 1
		}
		f.authIdx++
		var err error
		if i < len(f.authErr) {
			err = f.authErr[i]
		}
		if err != nil {
			return nil, err
		}
		return f.auth[i], nil
	case "ccusage":
		f.ccCalls++
		i := f.ccIdx
		if i >= len(f.cc) {
			i = len(f.cc) - 1
		}
		f.ccIdx++
		var err error
		if i < len(f.ccErr) {
			err = f.ccErr[i]
		}
		if err != nil {
			return nil, err
		}
		return f.cc[i], nil
	default:
		return nil, errors.New("unexpected runner name " + name)
	}
}

// TestProvider_List_HappyPath asserts a single account is surfaced
// after Start hydrates the cache.
func TestProvider_List_HappyPath(t *testing.T) {
	auth, cc := stubAccount()
	fr := &fakeRunnerSeq{auth: [][]byte{auth}, cc: [][]byte{cc}}

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	p := claudeaccount.NewWith(a, nil, time.Now, time.Hour, time.Hour)
	defer func() { _ = p.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rows, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List returned %d, want 1", len(rows))
	}
	if rows[0].ID.Email != "alice@example.com" {
		t.Errorf("email = %q, want alice@example.com", rows[0].ID.Email)
	}
}

// TestProvider_List_ToolMissing_PropagatesTypedError asserts a missing
// `claude` binary yields a typed error from List, not an empty list.
// This is what lets the resolver render a per-field GraphQL error.
func TestProvider_List_ToolMissing_PropagatesTypedError(t *testing.T) {
	fr := &fakeRunnerSeq{
		auth:    [][]byte{nil},
		cc:      [][]byte{nil},
		authErr: []error{&claudeaccount.ToolNotInstalledError{Tool: "claude"}},
	}

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	p := claudeaccount.NewWith(a, nil, time.Now, time.Hour, time.Hour)
	defer func() { _ = p.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	rows, err := p.List(ctx)
	if err == nil {
		t.Fatalf("List succeeded with %d rows; want ErrToolNotInstalled", len(rows))
	}
	var typed *claudeaccount.ToolNotInstalledError
	if !errors.As(err, &typed) {
		t.Fatalf("err = %v, want *ToolNotInstalledError", err)
	}
}

// TestProvider_ToGraphQL_MapsHostStub asserts the resolver wire shape:
// every scalar surfaces; instances is an empty slice (not nil); the
// host edge carries a stub Host with the correct id.
func TestProvider_ToGraphQL_MapsHostStub(t *testing.T) {
	auth, cc := stubAccount()
	fr := &fakeRunnerSeq{auth: [][]byte{auth}, cc: [][]byte{cc}}

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	p := claudeaccount.NewWith(a, nil, time.Now, time.Hour, time.Hour)
	defer func() { _ = p.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	rows, err := p.List(ctx)
	if err != nil || len(rows) == 0 {
		t.Fatalf("List: rows=%d err=%v", len(rows), err)
	}

	g := p.ToGraphQL(rows[0])
	if g.ID != "ClaudeAccount:test-host:alice@example.com" {
		t.Errorf("ID = %q, want canonical form", g.ID)
	}
	if g.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", g.Email)
	}
	if !g.QuotaEstimated {
		t.Error("QuotaEstimated = false, want true (v1: ccusage is the only source)")
	}
	if g.Host == nil || g.Host.ID != "Host:test-host" {
		t.Errorf("Host stub = %#v, want id=Host:test-host", g.Host)
	}
	if g.Instances == nil {
		t.Error("Instances is nil; want non-nil empty slice (gqlgen requires non-null)")
	}
	if len(g.Instances) != 0 {
		t.Errorf("Instances has %d entries, want 0 (v1 placeholder)", len(g.Instances))
	}
}

// TestProvider_Subscribe_FiresOnRefresh asserts subscribers see an
// invalidation event when the cache flips from empty to populated.
// This proves the watcher fan-out wiring works end-to-end.
func TestProvider_Subscribe_FiresOnRefresh(t *testing.T) {
	auth, cc := stubAccount()
	fr := &fakeRunnerSeq{auth: [][]byte{auth}, cc: [][]byte{cc}}

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	p := claudeaccount.NewWith(a, nil, time.Now, 50*time.Millisecond, time.Hour)
	defer func() { _ = p.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub := p.Subscribe(ctx)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case ev := <-sub:
		if ev.Key.Email != "alice@example.com" {
			t.Errorf("invalidation key email = %q, want alice@example.com", ev.Key.Email)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("never received invalidation event")
	}
}

// TestProvider_PollLoop_RecoversFromTransientError asserts the poll
// loop keeps retrying after a refresh failure — a flaky `ccusage`
// invocation should not block subsequent good reads.
func TestProvider_PollLoop_RecoversFromTransientError(t *testing.T) {
	auth, cc := stubAccount()
	fr := &fakeRunnerSeq{
		auth:    [][]byte{auth, auth, auth},
		cc:      [][]byte{nil, cc, cc},
		ccErr:   []error{errors.New("transient ccusage failure"), nil, nil},
	}

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	p := claudeaccount.NewWith(a, nil, time.Now, 50*time.Millisecond, time.Hour)
	defer func() { _ = p.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := p.List(ctx)
		if err == nil && len(rows) == 1 && rows[0].QuotaUsed != nil {
			return
		}
		time.Sleep(75 * time.Millisecond)
	}
	t.Fatal("provider never recovered from transient ccusage failure")
}
