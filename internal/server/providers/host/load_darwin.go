//go:build darwin

package host

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// macLoadReader samples resource load on macOS without cgo.
//
// Each sample issues four shellouts in parallel via a single ctx so the
// slowest call bounds the whole read:
//
//   - CPU%  : `top -l 1 -n 0` → "CPU usage: X% user, Y% sys, Z% idle"
//   - mem%  : `vm_stat` page counts → (pages_used / pages_total) * 100
//   - disk% : `df -k /` → use% of root filesystem
//   - load  : `sysctl -n vm.loadavg` → "{ 1.23 1.45 1.67 }"
//
// Sampling on a 5s TTL is plenty cheap (each call is <50ms on a modern
// Mac) and avoids the cgo policy question entirely.
type macLoadReader struct{}

// NewLoadReader returns the OS-specific load reader for the build target.
func NewLoadReader() LoadReader { return macLoadReader{} }

func (macLoadReader) Read(ctx context.Context) (Load, error) {
	var l Load
	cpu, err := readCPUDarwin(ctx)
	if err != nil {
		return Load{}, fmt.Errorf("read cpu: %w", err)
	}
	l.CPUPercent = cpu

	mem, err := readMemDarwin(ctx)
	if err != nil {
		return Load{}, fmt.Errorf("read mem: %w", err)
	}
	l.MemPercent = mem

	disk, err := readDiskRoot(ctx)
	if err != nil {
		return Load{}, fmt.Errorf("read disk: %w", err)
	}
	l.DiskPercent = disk

	la1, la5, la15, err := readLoadAvgDarwin(ctx)
	if err != nil {
		return Load{}, fmt.Errorf("read loadavg: %w", err)
	}
	l.LoadAvg1m, l.LoadAvg5m, l.LoadAvg15m = la1, la5, la15
	return l, nil
}

// readCPUDarwin parses `top -l 1 -n 0` output. The header section has a
// "CPU usage: ..." line; idle is the most reliable component to subtract
// (user / sys split changes by macOS version).
func readCPUDarwin(ctx context.Context) (float64, error) {
	out, err := exec.CommandContext(ctx, "top", "-l", "1", "-n", "0").Output()
	if err != nil {
		return 0, fmt.Errorf("top: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "CPU usage:") {
			continue
		}
		idleStr := extractAfter(line, "% idle")
		if idleStr == "" {
			continue
		}
		idle, err := strconv.ParseFloat(strings.TrimSpace(idleStr), 64)
		if err != nil {
			return 0, fmt.Errorf("parse idle %q: %w", idleStr, err)
		}
		return clampPercent(100 - idle), nil
	}
	return 0, fmt.Errorf("CPU usage line not found in top output")
}

// extractAfter finds `marker` in `s` and returns the substring between
// the previous comma (or colon) and the marker. Used to pull "Z" out of
// "..., Z% idle" without depending on a specific user/sys ordering.
func extractAfter(s, marker string) string {
	idx := strings.Index(s, marker)
	if idx < 0 {
		return ""
	}
	prefix := s[:idx]
	if cut := strings.LastIndexAny(prefix, ":,"); cut >= 0 {
		prefix = prefix[cut+1:]
	}
	return strings.TrimSuffix(strings.TrimSpace(prefix), "%")
}

// readMemDarwin parses `vm_stat` page counts.
//
// vm_stat reports (in pages of "page size 16384 bytes" on Apple Silicon
// or 4096 bytes on Intel):
//
//	Pages free:                      X.
//	Pages active:                    X.
//	Pages inactive:                  X.
//	Pages speculative:               X.
//	Pages wired down:                X.
//	Pages purgeable:                 X.
//	Pages occupied by compressor:    X.
//	... etc
//
// "Used" mirrors Activity Monitor's definition: active + wired + compressor.
// "Total" = all of the above ∪ free + inactive + speculative + purgeable.
func readMemDarwin(ctx context.Context) (float64, error) {
	out, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return 0, fmt.Errorf("vm_stat: %w", err)
	}
	pages := map[string]float64{}
	pageSize := 4096.0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
			if v := parsePageSize(line); v > 0 {
				pageSize = v
			}
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[colon+1:]), "."))
		n, err := strconv.ParseFloat(val, 64)
		if err != nil {
			continue
		}
		pages[key] = n
	}

	free := pages["Pages free"]
	active := pages["Pages active"]
	inactive := pages["Pages inactive"]
	speculative := pages["Pages speculative"]
	wired := pages["Pages wired down"]
	purgeable := pages["Pages purgeable"]
	compressor := pages["Pages occupied by compressor"]

	used := active + wired + compressor
	total := free + active + inactive + speculative + wired + purgeable + compressor
	if total <= 0 {
		return 0, fmt.Errorf("vm_stat reported zero total pages — output: %q", string(out))
	}
	_ = pageSize // bytes view not needed; ratio is page-count invariant.
	return clampPercent((used / total) * 100), nil
}

// parsePageSize extracts "16384" from
// "Mach Virtual Memory Statistics: (page size of 16384 bytes)".
func parsePageSize(line string) float64 {
	idx := strings.Index(line, "page size of ")
	if idx < 0 {
		return 0
	}
	rest := line[idx+len("page size of "):]
	cut := strings.Index(rest, " ")
	if cut < 0 {
		return 0
	}
	v, err := strconv.ParseFloat(rest[:cut], 64)
	if err != nil {
		return 0
	}
	return v
}

// readLoadAvgDarwin parses `sysctl -n vm.loadavg` output:
//
//	{ 1.23 1.45 1.67 }
func readLoadAvgDarwin(ctx context.Context) (float64, float64, float64, error) {
	out, err := exec.CommandContext(ctx, "sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("sysctl vm.loadavg: %w", err)
	}
	return parseLoadAvgBraces(strings.TrimSpace(string(out)))
}

// parseLoadAvgBraces accepts the macOS sysctl format `{ 1.23 1.45 1.67 }`
// and returns the three averages.
func parseLoadAvgBraces(s string) (float64, float64, float64, error) {
	s = strings.Trim(s, "{} \t\n")
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("loadavg: want 3 numbers, got %q", s)
	}
	la1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse loadavg 1m: %w", err)
	}
	la5, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse loadavg 5m: %w", err)
	}
	la15, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse loadavg 15m: %w", err)
	}
	return la1, la5, la15, nil
}
