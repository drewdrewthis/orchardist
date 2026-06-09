package hostidentity_test

import (
	"context"
	"testing"
	"time"

	hostidentity "github.com/drewdrewthis/orchardist/daemon/host-identity"
)

// staticIdentityReader returns a fixed Identity for test determinism.
type staticIdentityReader struct {
	id hostidentity.Identity
}

func (s *staticIdentityReader) Read(_ context.Context) (hostidentity.Identity, error) {
	return s.id, nil
}

// staticLoadReader returns a fixed Load for test determinism.
type staticLoadReader struct {
	load hostidentity.Load
}

func (s *staticLoadReader) Read(_ context.Context) (hostidentity.Load, error) {
	return s.load, nil
}

// TestProvider_Start_PopulatesIdentity asserts that Start reads identity and
// sets LocalID correctly.
func TestProvider_Start_PopulatesIdentity(t *testing.T) {
	idReader := &staticIdentityReader{id: hostidentity.Identity{
		MachineID: "TEST-MACHINE-ID",
		Hostname:  "test.local",
		OS:        "darwin",
		Kernel:    "Darwin 25.4.0",
	}}
	loadReader := &staticLoadReader{load: hostidentity.Load{
		CPUPercent:  10.0,
		MemPercent:  20.0,
		DiskPercent: 30.0,
		LoadAvg1m:   1.0,
		LoadAvg5m:   1.1,
		LoadAvg15m:  1.2,
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := hostidentity.NewProviderWith(idReader, loadReader, time.Now, nil)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	localID := p.LocalID()
	if string(localID) != "TEST-MACHINE-ID" {
		t.Errorf("LocalID = %q, want TEST-MACHINE-ID", localID)
	}
}

// TestProvider_Get_Returns asserts Get returns a snapshot after Start.
func TestProvider_Get_Returns(t *testing.T) {
	idReader := &staticIdentityReader{id: hostidentity.Identity{
		MachineID: "TEST-MACHINE-ID",
		Hostname:  "test.local",
		OS:        "darwin",
	}}
	loadReader := &staticLoadReader{load: hostidentity.Load{
		CPUPercent:  55.5,
		MemPercent:  44.4,
		DiskPercent: 33.3,
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := hostidentity.NewProviderWith(idReader, loadReader, time.Now, nil)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	snap, fresh, err := p.Get(ctx, hostidentity.HostID("TEST-MACHINE-ID"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if snap == nil {
		t.Fatal("Get returned nil snapshot")
	}
	if snap.Identity.MachineID != "TEST-MACHINE-ID" {
		t.Errorf("snap.Identity.MachineID = %q, want TEST-MACHINE-ID", snap.Identity.MachineID)
	}
	if !snap.LoadKnown {
		t.Error("LoadKnown = false after Start (which takes initial sample)")
	}
	if snap.Load.CPUPercent != 55.5 {
		t.Errorf("CPUPercent = %v, want 55.5", snap.Load.CPUPercent)
	}
	_ = fresh // Freshness correctness covered by provider; not the focus here.
}

// TestProvider_Get_UnknownKey asserts Get returns an error for unknown keys.
func TestProvider_Get_UnknownKey(t *testing.T) {
	idReader := &staticIdentityReader{id: hostidentity.Identity{
		MachineID: "TEST-MACHINE-ID",
		Hostname:  "test.local",
		OS:        "darwin",
	}}
	loadReader := &staticLoadReader{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := hostidentity.NewProviderWith(idReader, loadReader, time.Now, nil)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, _, err := p.Get(ctx, hostidentity.HostID("UNKNOWN-KEY"))
	if err == nil {
		t.Error("Get with unknown key: expected error, got nil")
	}
}

// TestProvider_Subscribe_EmitsAfterLoad asserts that the subscribe channel
// receives an event after a load refresh, verifying R16 (emit after write).
func TestProvider_Subscribe_EmitsAfterLoad(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }

	idReader := &staticIdentityReader{id: hostidentity.Identity{
		MachineID: "TEST-MACHINE-ID",
		Hostname:  "test.local",
		OS:        "darwin",
	}}

	var readCount int
	loadReader := &callCountLoadReader{
		load:     hostidentity.Load{CPUPercent: 10.0},
		onRead:   func() { readCount++ },
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := hostidentity.NewProviderWith(idReader, loadReader, clock, nil)
	ch := p.Subscribe(ctx)

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Start takes an initial load sample — expect one event.
	select {
	case ev := <-ch:
		if string(ev.Key) != "TEST-MACHINE-ID" {
			t.Errorf("event key = %q, want TEST-MACHINE-ID", ev.Key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subscription event after Start")
	}
}

// callCountLoadReader calls onRead on each Read invocation.
type callCountLoadReader struct {
	load   hostidentity.Load
	onRead func()
}

func (c *callCountLoadReader) Read(_ context.Context) (hostidentity.Load, error) {
	if c.onRead != nil {
		c.onRead()
	}
	return c.load, nil
}

// TestService_ProjectHost asserts projectHost produces correct GraphQL output.
// Exercises the projection via the full Service.Host path.
func TestService_ProjectHost(t *testing.T) {
	idReader := &staticIdentityReader{id: hostidentity.Identity{
		MachineID: "PROJ-TEST",
		Hostname:  "proj.local",
		OS:        "linux",
		Kernel:    "Linux 6.5.0",
	}}
	loadReader := &staticLoadReader{load: hostidentity.Load{
		CPUPercent:  25.0,
		MemPercent:  50.0,
		DiskPercent: 75.0,
		LoadAvg1m:   2.0,
		LoadAvg5m:   2.5,
		LoadAvg15m:  3.0,
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := hostidentity.NewProviderWith(idReader, loadReader, time.Now, nil)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	svc := hostidentity.NewService(p)
	h, err := svc.Host(ctx, hostidentity.HostID("PROJ-TEST"))
	if err != nil {
		t.Fatalf("Host(): %v", err)
	}
	if h.ID != "Host:PROJ-TEST" {
		t.Errorf("id = %q, want Host:PROJ-TEST", h.ID)
	}
	if h.Os != "linux" {
		t.Errorf("os = %q, want linux", h.Os)
	}
	if h.Kernel == nil || *h.Kernel != "Linux 6.5.0" {
		t.Errorf("kernel = %v, want Linux 6.5.0", h.Kernel)
	}
	if !h.Reachable {
		t.Error("reachable = false, want true")
	}
	if h.ResourceLoad == nil {
		t.Fatal("resourceLoad is nil")
	}
	if h.ResourceLoad.CPUPercent != 25.0 {
		t.Errorf("cpuPercent = %v, want 25.0", h.ResourceLoad.CPUPercent)
	}
	if h.ResourceLoad.LoadAvg1m != 2.0 {
		t.Errorf("loadAvg1m = %v, want 2.0", h.ResourceLoad.LoadAvg1m)
	}
}
