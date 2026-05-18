package hostservices

import (
	"context"
	"testing"
)

// TestResolveHostHostServices_ReturnsServicesForMachineID asserts that
// ResolveHostHostServices returns the correct services for a given host.
// Uses the LoaderByMachineID against a stub service (T1 — no production
// network/disk).
func TestResolveHostHostServices_ReturnsServicesForMachineID(t *testing.T) {
	svc := &stubServiceReader{
		snaps: []HostServiceSnapshot{
			{MachineID: "m1", Name: "svc-a", State: StateActive},
			{MachineID: "m1", Name: "svc-b", State: StateInactive},
			{MachineID: "m2", Name: "svc-c", State: StateFailed},
		},
	}

	loader := NewLoaderByMachineID(svc)
	ctx := context.Background()

	resolvers, err := ResolveHostHostServices(ctx, loader, "m1")
	if err != nil {
		t.Fatalf("ResolveHostHostServices: %v", err)
	}
	if len(resolvers) != 2 {
		t.Fatalf("len = %d, want 2 (m1 has 2 services)", len(resolvers))
	}

	names := make(map[string]bool)
	for _, r := range resolvers {
		names[r.Snap.Name] = true
	}
	if !names["svc-a"] || !names["svc-b"] {
		t.Errorf("expected svc-a and svc-b, got %v", names)
	}
}

// TestResolveHostHostServices_EmptyForUnknownMachine asserts that an
// unknown machineID returns an empty slice rather than an error.
func TestResolveHostHostServices_EmptyForUnknownMachine(t *testing.T) {
	svc := &stubServiceReader{
		snaps: []HostServiceSnapshot{
			{MachineID: "m1", Name: "svc", State: StateActive},
		},
	}

	loader := NewLoaderByMachineID(svc)
	resolvers, err := ResolveHostHostServices(context.Background(), loader, "no-such-machine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolvers) != 0 {
		t.Errorf("len = %d, want 0", len(resolvers))
	}
}

// TestResolveHostHostServices_IDFormat asserts each resolver projects the
// id as "HostService:<machineID>:<name>" (S2 — globally unique id).
func TestResolveHostHostServices_IDFormat(t *testing.T) {
	svc := &stubServiceReader{
		snaps: []HostServiceSnapshot{
			{MachineID: "machine-42", Name: "orchard", State: StateActive},
		},
	}

	loader := NewLoaderByMachineID(svc)
	resolvers, err := ResolveHostHostServices(context.Background(), loader, "machine-42")
	if err != nil {
		t.Fatalf("ResolveHostHostServices: %v", err)
	}
	if len(resolvers) == 0 {
		t.Fatal("no resolvers returned")
	}
	want := "HostService:machine-42:orchard"
	if got := resolvers[0].ID(); got != want {
		t.Errorf("ID() = %q, want %q", got, want)
	}
}
