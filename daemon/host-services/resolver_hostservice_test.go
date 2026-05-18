package hostservices

import (
	"context"
	"testing"
	"time"
)

// stubServiceReader is a minimal ServiceReader for resolver tests. All
// methods return canned data; no real provider/adapter involved (T1).
type stubServiceReader struct {
	snaps []HostServiceSnapshot
}

func (s *stubServiceReader) Snapshots(_ context.Context) ([]HostServiceSnapshot, error) {
	return s.snaps, nil
}

func (s *stubServiceReader) ByID(_ context.Context, id HostServiceID) (HostServiceSnapshot, error) {
	for _, snap := range s.snaps {
		if MakeID(snap.MachineID, snap.Name) == id {
			return snap, nil
		}
	}
	return HostServiceSnapshot{}, nil
}

func (s *stubServiceReader) ByMachineID(_ context.Context, machineID string) ([]HostServiceSnapshot, error) {
	var out []HostServiceSnapshot
	for _, snap := range s.snaps {
		if snap.MachineID == machineID {
			out = append(out, snap)
		}
	}
	return out, nil
}

func (s *stubServiceReader) Subscribe(_ context.Context) <-chan InvalidationEvent {
	ch := make(chan InvalidationEvent)
	close(ch)
	return ch
}

func (s *stubServiceReader) MachineID() string {
	if len(s.snaps) > 0 {
		return s.snaps[0].MachineID
	}
	return ""
}

// TestHostServiceResolver_ID asserts the id field is "HostService:<mid>:<name>" (S2).
func TestHostServiceResolver_ID(t *testing.T) {
	r := &HostServiceResolver{
		Snap: HostServiceSnapshot{MachineID: "m1", Name: "orchard"},
	}
	want := "HostService:m1:orchard"
	if got := r.ID(); got != want {
		t.Errorf("ID() = %q, want %q", got, want)
	}
}

// TestHostServiceResolver_Name asserts the name field is the config name.
func TestHostServiceResolver_Name(t *testing.T) {
	r := &HostServiceResolver{Snap: HostServiceSnapshot{Name: "orchard"}}
	if got := r.Name(); got != "orchard" {
		t.Errorf("Name() = %q, want orchard", got)
	}
}

// TestHostServiceResolver_State asserts each state constant maps correctly.
func TestHostServiceResolver_State(t *testing.T) {
	cases := []struct {
		state State
		want  string
	}{
		{StateActive, "active"},
		{StateInactive, "inactive"},
		{StateFailed, "failed"},
		{StateNotInstalled, "not_installed"},
		{StateUnknown, "unknown"},
	}
	for _, tc := range cases {
		r := &HostServiceResolver{Snap: HostServiceSnapshot{State: tc.state}}
		if got := r.StateName(); got != tc.want {
			t.Errorf("StateName() for %q = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// TestHostServiceResolver_Since_Nil asserts Since() returns nil when
// the snapshot has no since timestamp.
func TestHostServiceResolver_Since_Nil(t *testing.T) {
	r := &HostServiceResolver{Snap: HostServiceSnapshot{Since: nil}}
	if got := r.Since(); got != nil {
		t.Errorf("Since() = %q, want nil", *got)
	}
}

// TestHostServiceResolver_Since_NonNil asserts Since() returns an RFC 3339
// timestamp when the snapshot has one.
func TestHostServiceResolver_Since_NonNil(t *testing.T) {
	ts := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	r := &HostServiceResolver{Snap: HostServiceSnapshot{Since: &ts}}
	got := r.Since()
	if got == nil {
		t.Fatal("Since() is nil, want a timestamp string")
	}
	// Verify it parses back to the same instant.
	parsed, err := time.Parse("2006-01-02T15:04:05Z07:00", *got)
	if err != nil {
		t.Fatalf("Since() parse: %v", err)
	}
	if !parsed.Equal(ts) {
		t.Errorf("Since() = %q, parsed to %v, want %v", *got, parsed, ts)
	}
}

// TestHostServiceResolver_ExitCode asserts the exitCode field passes through.
func TestHostServiceResolver_ExitCode(t *testing.T) {
	code := 42
	r := &HostServiceResolver{Snap: HostServiceSnapshot{ExitCode: &code}}
	got := r.ExitCode()
	if got == nil {
		t.Fatal("ExitCode() is nil, want 42")
	}
	if *got != 42 {
		t.Errorf("ExitCode() = %d, want 42", *got)
	}

	// Nil path.
	r2 := &HostServiceResolver{Snap: HostServiceSnapshot{ExitCode: nil}}
	if r2.ExitCode() != nil {
		t.Error("ExitCode() should be nil when snapshot has no exitCode")
	}
}

// TestHostServiceResolver_LogTail asserts the logTail field passes through.
func TestHostServiceResolver_LogTail(t *testing.T) {
	tail := "line1\nline2"
	r := &HostServiceResolver{Snap: HostServiceSnapshot{LogTail: &tail}}
	got := r.LogTail()
	if got == nil {
		t.Fatal("LogTail() is nil, want a string")
	}
	if *got != tail {
		t.Errorf("LogTail() = %q, want %q", *got, tail)
	}
}

// TestResolveHostServices_NoFilter asserts all snapshots are returned
// when no filter is set.
func TestResolveHostServices_NoFilter(t *testing.T) {
	svc := &stubServiceReader{
		snaps: []HostServiceSnapshot{
			{MachineID: "m1", Name: "svc1", State: StateActive},
			{MachineID: "m1", Name: "svc2", State: StateInactive},
		},
	}
	resolvers, err := ResolveHostServices(context.Background(), svc, QueryHostServicesArgs{})
	if err != nil {
		t.Fatalf("ResolveHostServices: %v", err)
	}
	if len(resolvers) != 2 {
		t.Errorf("len = %d, want 2", len(resolvers))
	}
}

// TestResolveHostServices_FilterByHost asserts filtering by host machineID.
func TestResolveHostServices_FilterByHost(t *testing.T) {
	svc := &stubServiceReader{
		snaps: []HostServiceSnapshot{
			{MachineID: "m1", Name: "svc1", State: StateActive},
			{MachineID: "m2", Name: "svc2", State: StateInactive},
		},
	}
	host := "m1"
	resolvers, err := ResolveHostServices(context.Background(), svc, QueryHostServicesArgs{
		Filter: &HostServiceFilterInput{Host: &host},
	})
	if err != nil {
		t.Fatalf("ResolveHostServices: %v", err)
	}
	if len(resolvers) != 1 {
		t.Fatalf("len = %d, want 1 (filtered to m1)", len(resolvers))
	}
	if resolvers[0].Snap.MachineID != "m1" {
		t.Errorf("snap machineID = %q, want m1", resolvers[0].Snap.MachineID)
	}
}

// TestResolveHostServices_FilterByState asserts filtering by lifecycle state.
func TestResolveHostServices_FilterByState(t *testing.T) {
	svc := &stubServiceReader{
		snaps: []HostServiceSnapshot{
			{MachineID: "m1", Name: "active-svc", State: StateActive},
			{MachineID: "m1", Name: "failed-svc", State: StateFailed},
			{MachineID: "m1", Name: "inactive-svc", State: StateInactive},
		},
	}
	st := "failed"
	resolvers, err := ResolveHostServices(context.Background(), svc, QueryHostServicesArgs{
		Filter: &HostServiceFilterInput{State: &st},
	})
	if err != nil {
		t.Fatalf("ResolveHostServices: %v", err)
	}
	if len(resolvers) != 1 {
		t.Fatalf("len = %d, want 1 failed", len(resolvers))
	}
	if resolvers[0].Snap.State != StateFailed {
		t.Errorf("state = %q, want failed", resolvers[0].Snap.State)
	}
}

// TestResolveHostServices_FilterByName asserts filtering by exact name.
func TestResolveHostServices_FilterByName(t *testing.T) {
	svc := &stubServiceReader{
		snaps: []HostServiceSnapshot{
			{MachineID: "m1", Name: "orchard", State: StateActive},
			{MachineID: "m1", Name: "orchardist", State: StateActive},
		},
	}
	name := "orchard"
	resolvers, err := ResolveHostServices(context.Background(), svc, QueryHostServicesArgs{
		Filter: &HostServiceFilterInput{Name: &name},
	})
	if err != nil {
		t.Fatalf("ResolveHostServices: %v", err)
	}
	if len(resolvers) != 1 {
		t.Fatalf("len = %d, want 1 (exact name match)", len(resolvers))
	}
	if resolvers[0].Snap.Name != "orchard" {
		t.Errorf("name = %q, want orchard", resolvers[0].Snap.Name)
	}
}
