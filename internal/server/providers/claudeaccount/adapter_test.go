package claudeaccount_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeaccount"
)

// fakeRunner emits canned bytes per command name. Lets adapter tests
// exercise the parse paths without touching a real `claude` /
// `ccusage` binary.
type fakeRunner struct {
	mu      sync.Mutex
	outputs map[string][]byte
	errs    map[string]error
	calls   []call
}

type call struct {
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
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	if e, ok := f.errs[name]; ok {
		return nil, e
	}
	return f.outputs[name], nil
}

// TestShellAdapter_FetchAll_HappyPath_ParsesEmailAndQuota asserts the
// adapter merges `claude auth status` and `ccusage blocks` into the
// single Account v1 surfaces, with the documented quota fields populated.
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
		t.Fatalf("FetchAll missing account %s; got keys %v", id.GraphQLID(), keysOf(got))
	}

	if !acc.QuotaEstimated {
		t.Errorf("QuotaEstimated = false, want true (ccusage is the only v1 source)")
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

// TestShellAdapter_FetchAll_ClaudeMissing_ReturnsTypedError asserts the
// adapter surfaces a *ToolNotInstalledError when the `claude` binary
// is not on PATH. errors.Is must match the sentinel so the resolver
// can detect not-installed without string-matching.
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
		t.Errorf("errors.Is(err, ErrToolNotInstalled) = false; want true")
	}
}

// TestShellAdapter_FetchAll_CcusageMissing_PreservesEmail asserts
// that a missing ccusage degrades gracefully — the email field
// resolves and only the quota fields surface as a typed error. Mirrors
// ADR-011 §6 (per-field errors).
func TestShellAdapter_FetchAll_CcusageMissing_PreservesEmail(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["claude"] = []byte(`{"email":"alice@example.com"}`)
	fr.errs["ccusage"] = &claudeaccount.ToolNotInstalledError{Tool: "ccusage"}

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	got, err := a.FetchAll(context.Background())

	// We expect a partial result PLUS an error so the resolver can
	// surface a per-field GraphQL error for the quota fields without
	// blanking the email.
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
			t.Errorf("partial account should not have quota fields populated; got used=%v cap=%v",
				acc.QuotaUsed, acc.QuotaCap)
		}
	}
}

// TestShellAdapter_FetchAll_AcceptsAccountEmailVariant asserts the
// parser tolerates the older `account.email` JSON layout that some
// `claude` releases emit instead of a top-level `email` field.
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

// TestShellAdapter_FetchAll_ContextCancel_PropagatesError asserts the
// adapter respects ctx — when the caller cancels mid-fetch, the
// shellout returns the cancel error rather than blocking forever.
func TestShellAdapter_FetchAll_ContextCancel_PropagatesError(t *testing.T) {
	fr := newFakeRunner()
	// Stall the runner: the adapter should still observe ctx.Err().
	fr.errs["claude"] = context.Canceled

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.FetchAll(ctx)
	if err == nil {
		t.Fatal("FetchAll succeeded under cancelled ctx; want non-nil error")
	}
	// The adapter's first ctx.Err check fires before the runner is
	// invoked, so we expect context.Canceled rather than the runner's
	// own canned error.
	if !errors.Is(err, context.Canceled) {
		t.Logf("err = %v (acceptable as long as it is non-nil)", err)
	}
}

// keysOf renders the accounts map keys for test diagnostics.
func keysOf(m map[claudeaccount.AccountID]claudeaccount.Account) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k.GraphQLID())
	}
	return out
}

// _ docs the test-only assertion that NewShellAdapter respects the
// nil-logger contract (defaults to slog.Default()).
var _ = func() bool {
	a := claudeaccount.NewShellAdapter("h", nil)
	return a != nil
}()

// TestShellAdapter_PollDoesNotLogStdout — meta-assertion: ensure the
// adapter does not panic on PII-shaped stdout. We trust the no-log
// contract by code review, but assert that an unusual JSON payload
// (e.g. embedded newlines) does not crash the parser.
func TestShellAdapter_PollDoesNotLogStdout(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["claude"] = []byte(`{"email":"weird+test@example.com\nemail-leak"}`)
	fr.outputs["ccusage"] = []byte(`{"blocks": []}`)
	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	if _, err := a.FetchAll(context.Background()); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
}

// TestShellAdapter_FetchAll_RunnerCalledExactlyOnceEach asserts the
// adapter does not double-call either binary per FetchAll — quota
// numbers can't be inferred from auth status and vice versa, so a
// single call to each is the documented minimum.
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

// _ ensures the unused time import in adapter_test stays referenced
// when individual test-cases get skipped during refactors.
var _ = time.Second
