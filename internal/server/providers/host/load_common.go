package host

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// readDiskRoot returns the used percentage of `/` via `df -k /`.
//
// Both macOS and Linux ship POSIX-ish df with a "Use%" column; the
// number is the same regardless of platform, so this helper is shared.
//
// `df -k` output (header + one data row):
//
//	Filesystem 1024-blocks Used Available Capacity Mounted on
//	/dev/disk3s1 ... ... ... 78% /
//
// Some Linux distros report "Use%" instead of "Capacity"; both end with
// a "%"-suffixed token, so we hunt for that.
func readDiskRoot(ctx context.Context) (float64, error) {
	out, err := exec.CommandContext(ctx, "df", "-k", "/").Output()
	if err != nil {
		return 0, fmt.Errorf("df: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("df: expected header+data, got %q", string(out))
	}
	for _, field := range strings.Fields(lines[len(lines)-1]) {
		if !strings.HasSuffix(field, "%") {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSuffix(field, "%"), 64)
		if err != nil {
			continue
		}
		return clampPercent(n), nil
	}
	return 0, fmt.Errorf("df: no %% column in %q", lines[len(lines)-1])
}

// clampPercent keeps a percentage in 0..100 even when the underlying
// shellout reports something noisy (e.g. ever-so-slightly negative idle
// from rounding).
func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
