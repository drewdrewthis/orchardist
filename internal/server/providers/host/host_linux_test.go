//go:build linux

package host

import (
	"context"
	"testing"
	"time"
)

// TestIdentityReader_Linux asserts the Linux identity readers (machine-id,
// hostname, uname) return parseable values from real OS state. No mocks.
func TestIdentityReader_Linux(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	id, err := NewIdentityReader().Read(ctx)
	if err != nil {
		t.Fatalf("read identity: %v", err)
	}
	if id.MachineID == "" {
		t.Error("MachineID empty — /etc/machine-id parsing failed")
	}
	if id.Hostname == "" {
		t.Error("Hostname empty")
	}
	if id.OS != "linux" {
		t.Errorf("OS = %q, want linux", id.OS)
	}
}

// TestLoadReader_Linux asserts each Linux load source (/proc/stat,
// /proc/meminfo, df, /proc/loadavg) parses to a number in the expected
// range. Real reads.
func TestLoadReader_Linux(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	load, err := NewLoadReader().Read(ctx)
	if err != nil {
		t.Fatalf("read load: %v", err)
	}
	mustPercent(t, "CPUPercent", load.CPUPercent)
	mustPercent(t, "MemPercent", load.MemPercent)
	mustPercent(t, "DiskPercent", load.DiskPercent)
	if load.LoadAvg1m < 0 || load.LoadAvg5m < 0 || load.LoadAvg15m < 0 {
		t.Errorf("loadavg negative: 1m=%f 5m=%f 15m=%f", load.LoadAvg1m, load.LoadAvg5m, load.LoadAvg15m)
	}
}

func mustPercent(t *testing.T, name string, v float64) {
	t.Helper()
	if v < 0 || v > 100 {
		t.Errorf("%s = %v, want 0..100", name, v)
	}
}
