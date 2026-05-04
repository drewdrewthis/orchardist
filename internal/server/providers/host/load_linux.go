//go:build linux

package host

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// linuxLoadReader samples resource load on Linux via direct file reads.
//
//   - CPU%  : two reads of /proc/stat 100ms apart, diff the cpu line.
//   - mem%  : /proc/meminfo (MemTotal - MemAvailable) / MemTotal.
//   - disk% : `df -k /` (shared with darwin).
//   - load  : /proc/loadavg ("0.34 0.45 0.56 1/238 12345").
type linuxLoadReader struct{}

// NewLoadReader returns the OS-specific load reader for the build target.
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

// cpuTimes is the aggregate "cpu" row from /proc/stat. All fields are
// in jiffies; only their ratio matters here.
type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal float64
}

func (c cpuTimes) total() float64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

func (c cpuTimes) idleTotal() float64 { return c.idle + c.iowait }

// readCPULinux returns the CPU usage % over a 100ms sample window. Two
// /proc/stat reads + a sleep are the canonical idiom; instantaneous %
// is meaningless without a delta.
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

// readMemLinux parses /proc/meminfo using the kernel's preferred metric
// (MemAvailable, available since 3.14) for "free-ish" memory.
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
		val := strings.TrimSpace(line[colon+1:])
		val = strings.TrimSuffix(val, " kB")
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
//
// Format: "0.34 0.45 0.56 1/238 12345" — three floats then runnable/total + last pid.
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
