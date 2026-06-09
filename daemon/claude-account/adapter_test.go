package claudeaccount_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	claudeaccount "github.com/drewdrewthis/orchardist/daemon/claude-account"
)

// fakeRunner emits canned bytes per command name. Lets adapter tests exercise
// parse paths without touching a real `claude` / `ccusage` binary.
type fakeRunner struct {
	mu      sync.Mutex
	outputs map[string][]byte
	errs    map[string]error
	calls   []fakeCall
}

type fakeCall struct {
	name string
	args []string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		outputs: map[string][]byte{},
		errs:    map[string]error{},
	}
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{name: name, args: append([]string(nil), args...)})
	if e, ok := f.errs[name]; ok {
		return nil, e
	}
	return f.outputs[name], nil
}

// Calls returns the total number of Run calls made.
func (f *fakeRunner) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// TestShellAdapter_FetchAll_HappyPath_ParsesEmailAndQuota asserts the adapter
// merges `claude auth status` and `ccusage blocks` into the single Account v1
// surfaces, with quota fields populated.
func TestShellAdapter_FetchAll_HappyPath_ParsesEmailAndQuota(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["claude"] = []byte(`{"email":"alice@example.com"}`)
	fr.outputs["ccusage"] = []byte(`{
	  "blocks": [
	    {"active": false, "used": 99.0, "cap": 100.0, "resetsAt": "2026-04-01T00:00:00Z"},
	    {"active": true, "used": 12.5, "cap": 50.0, "resetsAt": "2026-05-05T00:00:00Z"}
	  ]
	}`)

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	got, err := a.FetchAll(context.Background())
	if err != nil {
		t.Fatalf("FetchAll returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchAll returned %d accounts, want 1", len(got))
	}

	id := claudeaccount.AccountID{HostID: "test-host", Email: "alice@example.com"}
	acc, ok := got[id]
	if !ok {
		t.Fatalf("FetchAll missing account %s", id.GraphQLID())
	}

	if !acc.QuotaEstimated {
		t.Error("QuotaEstimated = false, want true (ccusage is the only v1 source)")
	}
	if acc.QuotaUsed == nil || *acc.QuotaUsed != 12.5 {
		t.Errorf("QuotaUsed = %v, want 12.5", acc.QuotaUsed)
	}
	if acc.QuotaCap == nil || *acc.QuotaCap != 50.0 {
		t.Errorf("QuotaCap = %v, want 50.0", acc.QuotaCap)
	}
	if acc.QuotaResetsAt == nil {
		t.Error("QuotaResetsAt is nil, want parsed RFC 3339 timestamp")
	}
}

// TestShellAdapter_FetchAll_ClaudeMissing_ReturnsTypedError asserts the adapter
// surfaces a *ToolNotInstalledError when the `claude` binary is not on PATH.
// errors.Is must match the sentinel.
func TestShellAdapter_FetchAll_ClaudeMissing_ReturnsTypedError(t *testing.T) {
	fr := newFakeRunner()
	fr.errs["claude"] = &claudeaccount.ToolNotInstalledError{Tool: "claude"}

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	_, err := a.FetchAll(context.Background())
	if err == nil {
		t.Fatal("FetchAll succeeded without claude; want ErrToolNotInstalled")
	}
	var typed *claudeaccount.ToolNotInstalledError
	if !errors.As(err, &typed) {
		t.Fatalf("err = %v, want *ToolNotInstalledError", err)
	}
	if typed.Tool != "claude" {
		t.Errorf("typed.Tool = %q, want %q", typed.Tool, "claude")
	}
	if !errors.Is(err, claudeaccount.ErrToolNotInstalled) {
		t.Error("errors.Is(err, ErrToolNotInstalled) = false; want true")
	}
}

// TestShellAdapter_FetchAll_CcusageMissing_PreservesEmail asserts that a
// missing ccusage degrades gracefully — the email field resolves and only
// quota fields surface as a typed error.
func TestShellAdapter_FetchAll_CcusageMissing_PreservesEmail(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["claude"] = []byte(`{"email":"alice@example.com"}`)
	fr.errs["ccusage"] = &claudeaccount.ToolNotInstalledError{Tool: "ccusage"}

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	got, err := a.FetchAll(context.Background())

	if err == nil {
		t.Fatal("FetchAll returned no error; want ErrToolNotInstalled for ccusage")
	}
	if !errors.Is(err, claudeaccount.ErrToolNotInstalled) {
		t.Errorf("errors.Is(err, ErrToolNotInstalled) = false; want true")
	}
	if len(got) != 1 {
		t.Fatalf("FetchAll returned %d accounts, want 1 (email-only partial)", len(got))
	}
	for id, acc := range got {
		if id.Email != "alice@example.com" {
			t.Errorf("partial account email = %q, want alice@example.com", id.Email)
		}
		if acc.QuotaUsed != nil || acc.QuotaCap != nil {
			t.Errorf("partial account should not have quota fields; got used=%v cap=%v",
				acc.QuotaUsed, acc.QuotaCap)
		}
	}
}

// TestShellAdapter_FetchAll_AcceptsAccountEmailVariant asserts the parser
// tolerates the older `account.email` JSON layout.
func TestShellAdapter_FetchAll_AcceptsAccountEmailVariant(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["claude"] = []byte(`{"account":{"email":"bob@example.com"}}`)
	fr.outputs["ccusage"] = []byte(`{"blocks": []}`)

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	got, err := a.FetchAll(context.Background())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d accounts, want 1", len(got))
	}
	for id := range got {
		if id.Email != "bob@example.com" {
			t.Errorf("email = %q, want bob@example.com", id.Email)
		}
	}
}

// TestShellAdapter_FetchAll_ContextCancel_PropagatesError asserts the adapter
// respects ctx cancellation.
func TestShellAdapter_FetchAll_ContextCancel_PropagatesError(t *testing.T) {
	fr := newFakeRunner()
	fr.errs["claude"] = context.Canceled

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.FetchAll(ctx)
	if err == nil {
		t.Fatal("FetchAll succeeded under cancelled ctx; want non-nil error")
	}
}

// TestShellAdapter_FetchAll_RunnerCalledExactlyOnceEach asserts no double-call
// per binary per FetchAll.
func TestShellAdapter_FetchAll_RunnerCalledExactlyOnceEach(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["claude"] = []byte(`{"email":"alice@example.com"}`)
	fr.outputs["ccusage"] = []byte(`{"blocks":[]}`)

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	if _, err := a.FetchAll(context.Background()); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	counts := map[string]int{}
	for _, c := range fr.calls {
		counts[c.name]++
	}
	if counts["claude"] != 1 {
		t.Errorf("`claude` called %d times, want 1", counts["claude"])
	}
	if counts["ccusage"] != 1 {
		t.Errorf("`ccusage` called %d times, want 1", counts["ccusage"])
	}
}

// _ ensures the time import stays referenced.
var _ = time.Second
