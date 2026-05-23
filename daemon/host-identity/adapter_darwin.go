//go:build darwin

package hostidentity

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// macIdentityReader reads identity from macOS-native sources:
//   - machineId : `ioreg -rd1 -c IOPlatformExpertDevice` → IOPlatformUUID line.
//   - hostname  : os.Hostname() (libc gethostname under the hood).
//   - os        : runtime.GOOS ("darwin").
//   - kernel    : `uname -sr` ("Darwin 25.4.0"); blank if uname fails.
type macIdentityReader struct{}

// NewIdentityReader returns the macOS-specific IdentityReader.
// Selected at compile time via build tags.
func NewIdentityReader() IdentityReader { return macIdentityReader{} }

func (macIdentityReader) Read(ctx context.Context) (Identity, error) {
	id := Identity{OS: runtime.GOOS}

	uuid, err := readIOPlatformUUID(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("read IOPlatformUUID: %w", err)
	}
	id.MachineID = uuid

	hn, err := os.Hostname()
	if err != nil {
		return Identity{}, fmt.Errorf("read hostname: %w", err)
	}
	id.Hostname = hn

	// Kernel is best-effort — uname should always be present on macOS, but
	// a missing or unexpected output must not collapse identity.
	id.Kernel = readUnameSR(ctx)
	return id, nil
}

// readIOPlatformUUID shells `ioreg -rd1 -c IOPlatformExpertDevice` and
// parses the IOPlatformUUID line. Sample line:
//
//	"IOPlatformUUID" = "ABCD1234-..."
func readIOPlatformUUID(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", fmt.Errorf("ioreg: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"`)
		if val == "" {
			continue
		}
		return val, nil
	}
	return "", fmt.Errorf("IOPlatformUUID not found in ioreg output")
}

// readUnameSR returns "Darwin 25.4.0" or "" if uname fails.
func readUnameSR(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "uname", "-sr").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// macLoadReader samples resource load on macOS without cgo.
//
// Each sample issues shellouts in sequence:
//
//   - CPU%  : `top -l 1 -n 0` → "CPU usage: X% user, Y% sys, Z% idle"
//   - mem%  : `vm_stat` page counts → (pages_used / pages_total) × 100
//   - disk% : `df -k /` → use% of root filesystem
//   - load  : `sysctl -n vm.loadavg` → "{ 1.23 1.45 1.67 }"
//
// Sampling on a 5s TTL is cheap (<50ms each on a modern Mac).
type macLoadReader struct{}

// NewLoadReader returns the macOS-specific LoadReader.
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
// (user/sys split changes by macOS version).
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
		idleStr := extractAfterMarker(line, "% idle")
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

// extractAfterMarker finds `marker` in `s` and returns the substring between
// the previous comma (or colon) and the marker. Used to pull "Z" out of
// "..., Z% idle" without depending on a specific user/sys ordering.
func extractAfterMarker(s, marker string) string {
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
// "Used" mirrors Activity Monitor: active + wired + compressor.
// "Total" = all pages including free + inactive + speculative + purgeable.
func readMemDarwin(ctx context.Context) (float64, error) {
	out, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return 0, fmt.Errorf("vm_stat: %w", err)
	}
	pages := map[string]float64{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
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
	return clampPercent((used / total) * 100), nil
}

// readDiskRoot returns the used percentage of `/` via `df -k /`.
// Shared between Darwin and Linux via the same format output.
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
	return 0, fmt.Errorf("df: no %%-column in %q", lines[len(lines)-1])
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

// parseLoadAvgBraces accepts the macOS sysctl format `{ 1.23 1.45 1.67 }`.
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
