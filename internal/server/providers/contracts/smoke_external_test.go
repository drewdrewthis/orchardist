package contracts_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/contracts"
)

// TestContracts_LiveDirectory_Smoke verifies the provider successfully
// reads the live `~/.claude/contracts/` directory when present. Skipped
// in CI (no $HOME contracts dir).
//
// Guarded by ORCHARD_CONTRACTS_LIVE_SMOKE=1 so it stays opt-in.
func TestContracts_LiveDirectory_Smoke(t *testing.T) {
	if os.Getenv("ORCHARD_CONTRACTS_LIVE_SMOKE") != "1" {
		t.Skip("ORCHARD_CONTRACTS_LIVE_SMOKE!=1; skip live-dir smoke")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir: %v", err)
	}
	dir := filepath.Join(home, ".claude", "contracts")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("live contracts dir missing: %v", err)
	}
	p := contracts.NewWithPath(dir, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	keys, err := p.Keys(ctx)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	if len(keys) == 0 {
		t.Fatalf("0 contracts read from %s; expected ≥1 on a system with the plugin running", dir)
	}
	t.Logf("OK: %d contracts read from %s", len(keys), dir)
}
