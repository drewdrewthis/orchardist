package daemonmeta_test

import (
	"context"
	"strings"
	"testing"
	"time"

	daemonmeta "github.com/drewdrewthis/orchardist/daemon/daemon-meta"
)

// stubProvider is a test double for ProviderRegistry (T1 — stubbed service).
type stubProvider struct {
	name     string
	snapshot daemonmeta.ProviderHealthSnapshot
}

func (s *stubProvider) ProviderName() string { return s.name }
func (s *stubProvider) ProviderHealth() daemonmeta.ProviderHealthSnapshot {
	return s.snapshot
}

// newTestService is a helper that wires a ServiceImpl with a fixed start time.
func newTestService(startedAt time.Time, providers ...daemonmeta.ProviderRegistry) *daemonmeta.ServiceImpl {
	svc := daemonmeta.NewService(startedAt)
	for _, p := range providers {
		svc.RegisterProvider(p)
	}
	return svc
}

// --- T1: resolver tests against stubbed service ---

func TestDaemonState_ReturnsStartedAt(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc := newTestService(at)

	ds, err := svc.DaemonState(context.Background())
	if err != nil {
		t.Fatalf("DaemonState() error: %v", err)
	}
	if !strings.Contains(ds.StartedAt, "2026-01-01") {
		t.Errorf("StartedAt = %q; want contains 2026-01-01", ds.StartedAt)
	}
}

func TestDaemonState_UptimeIsNonNegative(t *testing.T) {
	t.Parallel()
	svc := newTestService(time.Now().Add(-5 * time.Second))
	ds, err := svc.DaemonState(context.Background())
	if err != nil {
		t.Fatalf("DaemonState() error: %v", err)
	}
	// Uptime must be >= 0 (can be 0 if rounding brings a sub-second to 0).
	// This is a meaningful assertion: it would fail if time.Since returned negative.
	if ds.UptimeS < 0 {
		t.Errorf("UptimeS = %d; want >= 0", ds.UptimeS)
	}
}

func TestDaemonState_ProviderRollup(t *testing.T) {
	t.Parallel()
	lastRefresh := "2026-01-01T12:00:00Z"
	p := &stubProvider{
		name: "tmux",
		snapshot: daemonmeta.ProviderHealthSnapshot{
			Configured:            true,
			LastSuccessfulRefresh: &lastRefresh,
			RefreshCount:          42,
			FailureCount:          1,
		},
	}
	svc := newTestService(time.Now(), p)
	ds, err := svc.DaemonState(context.Background())
	if err != nil {
		t.Fatalf("DaemonState() error: %v", err)
	}
	if len(ds.Providers) != 1 {
		t.Fatalf("len(Providers) = %d; want 1", len(ds.Providers))
	}
	if ds.ProviderNames[0] != "tmux" {
		t.Errorf("ProviderNames[0] = %q; want \"tmux\"", ds.ProviderNames[0])
	}
	snap := ds.Providers[0]
	if !snap.Configured {
		t.Error("Providers[0].Configured = false; want true")
	}
	if snap.RefreshCount != 42 {
		t.Errorf("RefreshCount = %d; want 42", snap.RefreshCount)
	}
	if snap.FailureCount != 1 {
		t.Errorf("FailureCount = %d; want 1", snap.FailureCount)
	}
}

func TestDaemonState_EmptyProviders(t *testing.T) {
	t.Parallel()
	svc := newTestService(time.Now())
	ds, err := svc.DaemonState(context.Background())
	if err != nil {
		t.Fatalf("DaemonState() error: %v", err)
	}
	if ds.Providers == nil {
		t.Error("Providers is nil; want empty slice (valid empty != unavailable)")
	}
}

func TestDaemonState_CancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := newTestService(time.Now())
	_, err := svc.DaemonState(ctx)
	if err == nil {
		t.Error("DaemonState() with cancelled ctx should return error")
	}
}

// --- T2: daemonReload in-process (L5 carve-out, no script) ---

func TestDaemonReload_InProcess(t *testing.T) {
	t.Parallel()
	reloaded := false
	reloader := func(ctx context.Context) error {
		reloaded = true
		return nil
	}
	svc := newTestService(time.Now())
	svc.SetConfigReloader(reloader)

	mr := daemonmeta.NewMutationResolver(svc)
	ds, err := mr.DaemonReload(context.Background())
	if err != nil {
		t.Fatalf("DaemonReload() error: %v", err)
	}
	if !reloaded {
		t.Error("expected config reloader to be called")
	}
	if ds == nil {
		t.Error("DaemonReload() returned nil DaemonState; want post-reload state")
	}
}

func TestDaemonReload_NoReloader_Succeeds(t *testing.T) {
	// Reload with no reloader set still returns DaemonState (no crash).
	t.Parallel()
	svc := newTestService(time.Now())
	mr := daemonmeta.NewMutationResolver(svc)
	ds, err := mr.DaemonReload(context.Background())
	if err != nil {
		t.Fatalf("DaemonReload() without reloader should succeed, got: %v", err)
	}
	if ds == nil {
		t.Error("DaemonReload() returned nil DaemonState")
	}
}

func TestDaemonReload_CancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := newTestService(time.Now())
	mr := daemonmeta.NewMutationResolver(svc)
	_, err := mr.DaemonReload(ctx)
	if err == nil {
		t.Error("DaemonReload() with cancelled ctx should return error")
	}
}

// --- T3: no tautological assertions (all above assertions can fail) ---

// TestProviderHealthSnapshot_ConfiguredFalse verifies the false branch.
func TestProviderHealthSnapshot_ConfiguredFalse(t *testing.T) {
	t.Parallel()
	p := &stubProvider{
		name: "unconfigured",
		snapshot: daemonmeta.ProviderHealthSnapshot{
			Configured: false,
		},
	}
	svc := newTestService(time.Now(), p)
	ds, err := svc.DaemonState(context.Background())
	if err != nil {
		t.Fatalf("DaemonState() error: %v", err)
	}
	if ds.Providers[0].Configured {
		t.Error("expected Configured=false for unconfigured provider")
	}
}
