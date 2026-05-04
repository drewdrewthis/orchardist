package host

import "context"

// Load is a snapshot of resource utilisation. All percentages are 0..100.
// Load averages mirror the kernel's 1/5/15-minute moving averages.
type Load struct {
	CPUPercent  float64
	MemPercent  float64
	DiskPercent float64
	LoadAvg1m   float64
	LoadAvg5m   float64
	LoadAvg15m  float64
}

// LoadReader samples the local machine's resource utilisation.
// Implementations are OS-specific and selected at compile time via
// build tags (load_darwin.go, load_linux.go).
//
// Read on a 5-second TTL by the Provider's poll loop. Implementations
// must respect ctx for shellouts that could otherwise hang.
type LoadReader interface {
	Read(ctx context.Context) (Load, error)
}
