package contracts

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestProvider_StartAndGet verifies that Provider.Start hydrates the cache from
// disk and Provider.Get returns the folded contract. T1: resolver reads through
// service against a stub (here a real temp dir with known content).
func TestProvider_StartAndGet(t *testing.T) {
	dir := t.TempDir()
	t0 := mustTime(t, "2026-05-04T12:00:00Z")

	writeJSONL(t, filepath.Join(dir, "C-svc-001.jsonl"),
		creationLine(t, "C-svc-001", "test contract", "agent-a", "session-a", t0),
	)

	p := NewWithPath(dir, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() { _ = p.Stop() }()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	c, err := p.Get(ctx, "C-svc-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if c == nil {
		t.Fatal("Get returned nil for known contract")
	}
	if c.Statement != "test contract" {
		t.Errorf("Statement = %q, want %q", c.Statement, "test contract")
	}
	if c.Status != StatusOpen {
		t.Errorf("Status = %q, want %q", c.Status, StatusOpen)
	}
}

// TestProvider_GetMany verifies batch fetch returns multiple contracts.
func TestProvider_GetMany(t *testing.T) {
	dir := t.TempDir()
	t0 := mustTime(t, "2026-05-04T12:00:00Z")

	writeJSONL(t, filepath.Join(dir, "C-many-001.jsonl"),
		creationLine(t, "C-many-001", "alpha", "agent-a", "session-a", t0),
	)
	writeJSONL(t, filepath.Join(dir, "C-many-002.jsonl"),
		creationLine(t, "C-many-002", "beta", "agent-b", "session-b", t0),
	)

	p := NewWithPath(dir, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() { _ = p.Stop() }()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	results, err := p.GetMany(ctx, []ContractID{"C-many-001", "C-many-002", "C-missing"})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if got, want := len(results), 2; got != want {
		t.Errorf("GetMany returned %d results, want %d", got, want)
	}
	if _, ok := results["C-missing"]; ok {
		t.Error("GetMany returned entry for unknown key C-missing")
	}
}

// TestProvider_List verifies that List applies the filter correctly.
func TestProvider_List(t *testing.T) {
	dir := t.TempDir()
	t0 := mustTime(t, "2026-05-04T12:00:00Z")
	t1 := t0.Add(time.Minute)

	writeJSONL(t, filepath.Join(dir, "C-list-001.jsonl"),
		creationLine(t, "C-list-001", "open contract", "agent-a", "session-a", t0),
	)
	writeJSONL(t, filepath.Join(dir, "C-list-002.jsonl"),
		creationLine(t, "C-list-002", "satisfied contract", "agent-b", "session-b", t0),
		statusChangeLine(t, "C-list-002", t1, "open", "satisfied", "drew_approve"),
	)

	p := NewWithPath(dir, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() { _ = p.Stop() }()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	all, err := p.List(ctx, nil)
	if err != nil {
		t.Fatalf("List(nil): %v", err)
	}
	if got, want := len(all), 2; got != want {
		t.Fatalf("List returned %d, want %d", got, want)
	}

	filtered, err := p.List(ctx, &ContractFilter{Statuses: []ContractStatus{StatusOpen}})
	if err != nil {
		t.Fatalf("List(open): %v", err)
	}
	if got, want := len(filtered), 1; got != want {
		t.Errorf("List(open) returned %d, want %d", got, want)
	}
	if filtered[0].ID != "C-list-001" {
		t.Errorf("List(open)[0].ID = %q, want C-list-001", filtered[0].ID)
	}
}

// TestProvider_GetUnknown verifies that Get returns nil, nil for an unknown id.
func TestProvider_GetUnknown(t *testing.T) {
	dir := t.TempDir()
	p := NewWithPath(dir, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() { _ = p.Stop() }()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	c, err := p.Get(ctx, "C-does-not-exist")
	if err != nil {
		t.Fatalf("Get unknown: unexpected error %v", err)
	}
	if c != nil {
		t.Errorf("Get unknown returned non-nil contract: %+v", c)
	}
}
