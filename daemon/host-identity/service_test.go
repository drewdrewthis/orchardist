package hostidentity_test

import (
	"context"
	"strings"
	"testing"
	"time"

	graphql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"

	hostidentity "github.com/drewdrewthis/git-orchard-rs/daemon/host-identity"
)

// stubService is a minimal Service implementation for unit tests.
// It satisfies T1: resolvers are tested against a stubbed service, not
// real OS shellouts.
type stubService struct {
	id   hostidentity.HostID
	host *graphql.Host
}

func (s *stubService) LocalID() hostidentity.HostID { return s.id }

func (s *stubService) Host(_ context.Context, key hostidentity.HostID) (*graphql.Host, error) {
	if key == s.id {
		return s.host, nil
	}
	return nil, nil //nolint:nilnil // unknown key → absent (same as loader stub behaviour)
}

func (s *stubService) Hosts(ctx context.Context) ([]*graphql.Host, error) {
	h, err := s.Host(ctx, s.id)
	if err != nil || h == nil {
		return []*graphql.Host{}, err
	}
	return []*graphql.Host{h}, nil
}

// newStubService builds a minimal stub with the given machine id, hostname,
// and OS — enough to drive every Host field in T1 assertions.
func newStubService(machineID, hostname, os string) *stubService {
	id := hostidentity.HostID(machineID)
	kernel := "Darwin 25.4.0"
	purpose := "test-box"
	rl := &graphql.ResourceLoad{
		CPUPercent:  12.5,
		MemPercent:  34.0,
		DiskPercent: 56.7,
		LoadAvg1m:   0.5,
		LoadAvg5m:   0.6,
		LoadAvg15m:  0.7,
	}
	h := &graphql.Host{
		ID:           "Host:" + machineID,
		MachineID:    machineID,
		Hostname:     hostname,
		Os:           os,
		Kernel:       &kernel,
		Reachable:    true,
		LastSeenAt:   time.Now().UTC().Format(time.RFC3339Nano),
		ResourceLoad: rl,
		Peers:        []*graphql.Host{},
		Purpose:      &purpose,
	}
	return &stubService{id: id, host: h}
}

// --- T1: every typed field resolver asserted against stub service ---

// TestQueryResolver_Host_ID asserts that Host.id is "Host:<machineId>".
func TestQueryResolver_Host_ID(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	loaders := hostidentity.NewLoaders(svc)
	qr := hostidentity.NewQueryResolver(svc, loaders)

	h, err := qr.Host(context.Background())
	if err != nil {
		t.Fatalf("Host(): %v", err)
	}
	if h == nil {
		t.Fatal("Host() returned nil")
	}
	if !strings.HasPrefix(h.ID, "Host:") {
		t.Errorf("Host.id = %q, want Host:<machineId>", h.ID)
	}
	if h.ID != "Host:ABCD-1234" {
		t.Errorf("Host.id = %q, want Host:ABCD-1234", h.ID)
	}
}

// TestQueryResolver_Host_MachineID asserts machineId is populated.
func TestQueryResolver_Host_MachineID(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	qr := hostidentity.NewQueryResolver(svc, nil)

	h, err := qr.Host(context.Background())
	if err != nil {
		t.Fatalf("Host(): %v", err)
	}
	if h.MachineID != "ABCD-1234" {
		t.Errorf("MachineID = %q, want ABCD-1234", h.MachineID)
	}
}

// TestQueryResolver_Host_Hostname asserts hostname is populated.
func TestQueryResolver_Host_Hostname(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	qr := hostidentity.NewQueryResolver(svc, nil)

	h, err := qr.Host(context.Background())
	if err != nil {
		t.Fatalf("Host(): %v", err)
	}
	if h.Hostname != "test-host.local" {
		t.Errorf("Hostname = %q, want test-host.local", h.Hostname)
	}
}

// TestQueryResolver_Host_OS asserts the os field is populated.
func TestQueryResolver_Host_OS(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	qr := hostidentity.NewQueryResolver(svc, nil)

	h, err := qr.Host(context.Background())
	if err != nil {
		t.Fatalf("Host(): %v", err)
	}
	if h.Os != "darwin" {
		t.Errorf("Os = %q, want darwin", h.Os)
	}
}

// TestQueryResolver_Host_Kernel asserts kernel is non-nil and non-empty when set.
func TestQueryResolver_Host_Kernel(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	qr := hostidentity.NewQueryResolver(svc, nil)

	h, err := qr.Host(context.Background())
	if err != nil {
		t.Fatalf("Host(): %v", err)
	}
	if h.Kernel == nil || *h.Kernel == "" {
		t.Error("Kernel nil or empty, want non-empty string")
	}
}

// TestQueryResolver_Host_Reachable asserts local host is always reachable.
func TestQueryResolver_Host_Reachable(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	qr := hostidentity.NewQueryResolver(svc, nil)

	h, err := qr.Host(context.Background())
	if err != nil {
		t.Fatalf("Host(): %v", err)
	}
	if !h.Reachable {
		t.Error("Reachable = false, want true for local host")
	}
}

// TestQueryResolver_Host_Peers asserts peers is empty for v1.
func TestQueryResolver_Host_Peers(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	qr := hostidentity.NewQueryResolver(svc, nil)

	h, err := qr.Host(context.Background())
	if err != nil {
		t.Fatalf("Host(): %v", err)
	}
	if len(h.Peers) != 0 {
		t.Errorf("Peers has %d entries, want 0 for v1", len(h.Peers))
	}
}

// TestQueryResolver_Host_LastSeenAt asserts lastSeenAt is RFC3339.
func TestQueryResolver_Host_LastSeenAt(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	qr := hostidentity.NewQueryResolver(svc, nil)

	h, err := qr.Host(context.Background())
	if err != nil {
		t.Fatalf("Host(): %v", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, h.LastSeenAt); err != nil {
		t.Errorf("LastSeenAt %q is not RFC3339: %v", h.LastSeenAt, err)
	}
}

// TestQueryResolver_Host_ResourceLoad asserts resourceLoad fields are in range.
func TestQueryResolver_Host_ResourceLoad(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	qr := hostidentity.NewQueryResolver(svc, nil)

	h, err := qr.Host(context.Background())
	if err != nil {
		t.Fatalf("Host(): %v", err)
	}
	if h.ResourceLoad == nil {
		t.Fatal("ResourceLoad is nil")
	}
	rl := h.ResourceLoad
	mustPercent(t, "cpuPercent", rl.CPUPercent)
	mustPercent(t, "memPercent", rl.MemPercent)
	mustPercent(t, "diskPercent", rl.DiskPercent)
	if rl.LoadAvg1m < 0 || rl.LoadAvg5m < 0 || rl.LoadAvg15m < 0 {
		t.Errorf("loadavg negative: 1m=%f 5m=%f 15m=%f", rl.LoadAvg1m, rl.LoadAvg5m, rl.LoadAvg15m)
	}
}

// TestQueryResolver_Hosts_SingleElement asserts hosts returns exactly one entry (v1).
func TestQueryResolver_Hosts_SingleElement(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	qr := hostidentity.NewQueryResolver(svc, nil)

	hosts, err := qr.Hosts(context.Background())
	if err != nil {
		t.Fatalf("Hosts(): %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("Hosts has %d entries, want 1 for v1", len(hosts))
	}
}

// TestQueryResolver_Peers_Empty asserts peers is empty for v1.
func TestQueryResolver_Peers_Empty(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	qr := hostidentity.NewQueryResolver(svc, nil)

	peers, err := qr.Peers(context.Background())
	if err != nil {
		t.Fatalf("Peers(): %v", err)
	}
	if len(peers) != 0 {
		t.Errorf("Peers has %d entries, want 0 for v1", len(peers))
	}
}

// TestHostResolver_Peers_Empty asserts Host.peers resolver returns [].
func TestHostResolver_Peers_Empty(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	hr := hostidentity.NewHostResolver(svc)

	peers, err := hr.Peers(context.Background(), &graphql.Host{ID: "Host:ABCD-1234"})
	if err != nil {
		t.Fatalf("Peers(): %v", err)
	}
	if len(peers) != 0 {
		t.Errorf("Peers has %d entries, want 0", len(peers))
	}
}

// TestHostResolver_Version_Nil asserts Host.version returns nil from this resolver.
func TestHostResolver_Version_Nil(t *testing.T) {
	svc := newStubService("ABCD-1234", "test-host.local", "darwin")
	hr := hostidentity.NewHostResolver(svc)

	v, err := hr.Version(context.Background(), &graphql.Host{ID: "Host:ABCD-1234"})
	if err != nil {
		t.Fatalf("Version(): %v", err)
	}
	if v != nil {
		t.Errorf("Version = %q, want nil (owned by daemon-self)", *v)
	}
}

// mustPercent fails the test if v is outside 0..100.
func mustPercent(t *testing.T, name string, v float64) {
	t.Helper()
	if v < 0 || v > 100 {
		t.Errorf("%s = %v, want 0..100", name, v)
	}
}
