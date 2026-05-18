//go:build linux

package hostidentity

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// linuxIdentityReader reads identity from Linux-native sources:
//   - machineId : /etc/machine-id (systemd-issued).
//   - hostname  : os.Hostname().
//   - os        : runtime.GOOS ("linux").
//   - kernel    : `uname -sr` ("Linux 6.5.0-...").
type linuxIdentityReader struct{}

// NewIdentityReader returns the Linux-specific IdentityReader.
// Selected at compile time via build tags.
func NewIdentityReader() IdentityReader { return linuxIdentityReader{} }

func (linuxIdentityReader) Read(ctx context.Context) (Identity, error) {
	id := Identity{OS: runtime.GOOS}

	mid, err := readMachineID()
	if err != nil {
		return Identity{}, fmt.Errorf("read /etc/machine-id: %w", err)
	}
	id.MachineID = mid

	hn, err := os.Hostname()
	if err != nil {
		return Identity{}, fmt.Errorf("read hostname: %w", err)
	}
	id.Hostname = hn

	id.Kernel = readUnameSR(ctx)
	return id, nil
}

func readMachineID() (string, error) {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", fmt.Errorf("/etc/machine-id is empty")
	}
	return id, nil
}

func readUnameSR(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "uname", "-sr").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// linuxLoadReader samples resource load on Linux via direct file reads.
//
//   - CPU%  : two reads of /proc/stat 100ms apart, diff the cpu line.
//   - mem%  : /proc/meminfo (MemTotal - MemAvailable) / MemTotal.
//   - disk% : `df -k /` (same as Darwin).
//   - load  : /proc/loadavg ("0.34 0.45 0.56 1/238 12345").
type linuxLoadReader struct{}

// NewLoadReader returns the Linux-specific LoadReader.
func NewLoadReader() LoadReader { return linuxLoadReader{} }

func (linuxLoadReader) Read(ctx context.Context) (Load, error) {
	var l Load

	cpu, err := readCPULinux(ctx)
	if err != nil {
		return Load{}, fmt.Errorf("read cpu: %w", err)
	}
	l.CPUPercent = cpu

	mem, err := readMemLinux()
	if err != nil {
		return Load{}, fmt.Errorf("read mem: %w", err)
	}
	l.MemPercent = mem

	disk, err := readDiskRoot(ctx)
	if err != nil {
		return Load{}, fmt.Errorf("read disk: %w", err)
	}
	l.DiskPercent = disk

	la1, la5, la15, err := readLoadAvgLinux()
	if err != nil {
		return Load{}, fmt.Errorf("read loadavg: %w", err)
	}
	l.LoadAvg1m, l.LoadAvg5m, l.LoadAvg15m = la1, la5, la15
	return l, nil
}

// cpuTimes is the aggregate "cpu" row from /proc/stat. All fields are jiffies.
type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal float64
}

func (c cpuTimes) total() float64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

func (c cpuTimes) idleTotal() float64 { return c.idle + c.iowait }

// readCPULinux returns CPU usage % over a 100ms sample window.
// Two /proc/stat reads + a sleep are the canonical Linux idiom.
func readCPULinux(ctx context.Context) (float64, error) {
	a, err := readProcStatCPU()
	if err != nil {
		return 0, err
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	b, err := readProcStatCPU()
	if err != nil {
		return 0, err
	}
	totalDelta := b.total() - a.total()
	idleDelta := b.idleTotal() - a.idleTotal()
	if totalDelta <= 0 {
		return 0, nil
	}
	return clampPercent((1 - idleDelta/totalDelta) * 100), nil
}

func readProcStatCPU() (cpuTimes, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, fmt.Errorf("/proc/stat: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		// fields[0] = "cpu", then user nice system idle iowait irq softirq steal ...
		if len(fields) < 9 {
			return cpuTimes{}, fmt.Errorf("/proc/stat cpu line has %d fields", len(fields))
		}
		nums := make([]float64, 8)
		for i := 0; i < 8; i++ {
			n, err := strconv.ParseFloat(fields[i+1], 64)
			if err != nil {
				return cpuTimes{}, fmt.Errorf("parse field %d: %w", i+1, err)
			}
			nums[i] = n
		}
		return cpuTimes{
			user:    nums[0],
			nice:    nums[1],
			system:  nums[2],
			idle:    nums[3],
			iowait:  nums[4],
			irq:     nums[5],
			softirq: nums[6],
			steal:   nums[7],
		}, nil
	}
	return cpuTimes{}, fmt.Errorf("/proc/stat: aggregate cpu line not found")
}

// readMemLinux parses /proc/meminfo using MemAvailable (Linux 3.14+).
func readMemLinux() (float64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("/proc/meminfo: %w", err)
	}
	mem := map[string]float64{}
	for _, line := range strings.Split(string(data), "\n") {
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSuffix(strings.TrimSpace(line[colon+1:]), " kB")
		n, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil {
			continue
		}
		mem[key] = n
	}
	total := mem["MemTotal"]
	avail := mem["MemAvailable"]
	if total <= 0 {
		return 0, fmt.Errorf("/proc/meminfo: MemTotal not present or zero")
	}
	if avail == 0 {
		// Pre-3.14 kernels: fall back to free + buffers + cached.
		avail = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
	}
	used := total - avail
	return clampPercent((used / total) * 100), nil
}

// readLoadAvgLinux reads /proc/loadavg.
// Format: "0.34 0.45 0.56 1/238 12345"
func readLoadAvgLinux() (float64, float64, float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("/proc/loadavg: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("/proc/loadavg: want 3+ fields, got %q", string(data))
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
