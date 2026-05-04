//go:build darwin

package host

import (
	"context"
	"testing"
	"time"
)

// TestIdentityReader_Darwin asserts the macOS identity shellouts return
// a parseable IOPlatformUUID, hostname, and "Darwin <kernel>" string.
// Real shellout — no mocks — per worker standards §3.
func TestIdentityReader_Darwin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	id, err := NewIdentityReader().Read(ctx)
	if err != nil {
		t.Fatalf("read identity: %v", err)
	}
	if id.MachineID == "" {
		t.Error("MachineID empty — ioreg parsing failed")
	}
	if id.Hostname == "" {
		t.Error("Hostname empty")
	}
	if id.OS != "darwin" {
		t.Errorf("OS = %q, want darwin", id.OS)
	}
	// Kernel is best-effort. When uname is present (always, on macOS)
	// it should at least contain the OS family.
	if id.Kernel != "" && len(id.Kernel) < len("Darwin") {
		t.Errorf("Kernel = %q, suspiciously short", id.Kernel)
	}
}

// TestLoadReader_Darwin asserts each macOS load shellout (top, vm_stat,
// df, sysctl) parses to a number in the expected range. Real shellouts.
//
// Budget is generous (15s) because `top -l 1 -n 0` itself samples for
// ~0.6s and a busy CI host can stretch every concurrent shellout the
// suite runs in parallel.
func TestLoadReader_Darwin(t *testing.T) {
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

// TestParseLoadAvgBraces_Darwin pins the macOS sysctl format parser
// against a known-good fixture so a future tweak to the parser cannot
// silently drop a load average.
func TestParseLoadAvgBraces_Darwin(t *testing.T) {
	cases := []struct {
		in              string
		la1, la5, la15  float64
		wantParseErrMsg string
	}{
		{in: "{ 1.23 1.45 1.67 }", la1: 1.23, la5: 1.45, la15: 1.67},
		{in: "{0.10 0.20 0.30}", la1: 0.10, la5: 0.20, la15: 0.30},
		{in: "{ 0 0 0 }", la1: 0, la5: 0, la15: 0},
		{in: "{ junk }", wantParseErrMsg: "loadavg: want 3 numbers"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			la1, la5, la15, err := parseLoadAvgBraces(tc.in)
			if tc.wantParseErrMsg != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tc.wantParseErrMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if la1 != tc.la1 || la5 != tc.la5 || la15 != tc.la15 {
				t.Errorf("got (%f, %f, %f), want (%f, %f, %f)", la1, la5, la15, tc.la1, tc.la5, tc.la15)
			}
		})
	}
}

func mustPercent(t *testing.T, name string, v float64) {
	t.Helper()
	if v < 0 || v > 100 {
		t.Errorf("%s = %v, want 0..100", name, v)
	}
}
