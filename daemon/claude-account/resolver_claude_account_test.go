package claudeaccount_test

import (
	"context"
	"testing"
	"time"

	claudeaccount "github.com/drewdrewthis/orchardist/daemon/claude-account"
)

// T1: Every typed field has a resolver test against a stubbed service.

// TestClaudeAccountResolver_QueryClaudeAccounts_Projects asserts every scalar
// field is projected correctly from Account → ResolvedAccount.
func TestClaudeAccountResolver_QueryClaudeAccounts_Projects(t *testing.T) {
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

	svc := claudeaccount.NewService(p)
	resolver := claudeaccount.NewClaudeAccountResolver(svc)

	accounts, err := resolver.QueryClaudeAccounts(ctx, "test-host")
	if err != nil {
		t.Fatalf("QueryClaudeAccounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	acc := accounts[0]

	// T1: assert every typed field (T3: all can fail on wrong data).
	if acc.ID != "ClaudeAccount:test-host:alice@example.com" {
		t.Errorf("ID = %q, want ClaudeAccount:test-host:alice@example.com", acc.ID)
	}
	if acc.Email != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", acc.Email)
	}
	if !acc.QuotaEstimated {
		t.Error("QuotaEstimated = false, want true (ccusage is the only v1 source)")
	}
	if acc.QuotaUsed == nil || *acc.QuotaUsed != 7.5 {
		t.Errorf("QuotaUsed = %v, want 7.5", acc.QuotaUsed)
	}
	if acc.QuotaCap == nil || *acc.QuotaCap != 50 {
		t.Errorf("QuotaCap = %v, want 50", acc.QuotaCap)
	}
	if acc.QuotaResetsAt == nil {
		t.Error("QuotaResetsAt is nil, want non-nil timestamp")
	}
	// S5: nullable fields should be nil-able (tested when ccusage is missing in other tests).
	if acc.HostID != "test-host" {
		t.Errorf("HostID = %q, want test-host", acc.HostID)
	}
	if acc.Instances == nil {
		t.Error("Instances is nil; want non-nil empty slice")
	}
	if len(acc.Instances) != 0 {
		t.Errorf("Instances has %d entries, want 0 (v1 placeholder)", len(acc.Instances))
	}
}

// TestClaudeAccountResolver_QueryClaudeAccounts_ToolMissingError asserts the
// resolver surfaces a typed error when claude is not installed, not a panic.
func TestClaudeAccountResolver_QueryClaudeAccounts_ToolMissingError(t *testing.T) {
	fr := &fakeRunnerSeq{
		auth:    [][]byte{nil},
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

	svc := claudeaccount.NewService(p)
	resolver := claudeaccount.NewClaudeAccountResolver(svc)

	_, err := resolver.QueryClaudeAccounts(ctx, "test-host")
	if err == nil {
		t.Fatal("QueryClaudeAccounts succeeded; want error when claude missing")
	}
}
