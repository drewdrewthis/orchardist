package ps

import (
	"bufio"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// psHeader is the canonical column set the adapter requests; the parser
// validates the header to catch silent format drifts (e.g. an ancient ps).
const psHeader = "PID PPID USER TTY %CPU RSS STARTED COMMAND"

// lstartLayout matches BSD/Darwin `lstart` output and the equivalent
// Linux ps STARTED column when invoked with the same -o lstart flag.
// `_2` consumes a leading space for single-digit days.
const lstartLayout = "Mon Jan _2 15:04:05 2006"

// parsePs converts the raw stdout of `ps -ax -o pid,ppid,user,tty,%cpu,rss,lstart,command`
// into a slice of Process values keyed for the given host. Lines that
// don't parse are skipped with no error — ps occasionally emits transient
// noise (a process exiting mid-format) that is not worth aborting over.
func parsePs(host, raw string) ([]Process, error) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	if !scanner.Scan() {
		return nil, fmt.Errorf("ps: empty output")
	}
	if err := validateHeader(scanner.Text()); err != nil {
		return nil, err
	}

	var out []Process
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		p, ok := parseLine(host, line)
		if ok {
			out = append(out, p)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ps: scan: %w", err)
	}
	return out, nil
}

// validateHeader normalises the ps header (collapsing repeated spaces)
// and compares against the expected column ordering. A mismatch means
// the adapter was invoked with a different -o flag, or ps has been
// upgraded to a format the parser doesn't understand.
func validateHeader(line string) error {
	got := strings.Join(strings.Fields(line), " ")
	if got != psHeader {
		return fmt.Errorf("ps: unexpected header %q (want %q)", got, psHeader)
	}
	return nil
}

// parseLine consumes one ps data line. Returns ok=false on malformed
// lines so the scanner loop can skip them rather than abort.
func parseLine(host, line string) (Process, bool) {
	// First six tokens: pid, ppid, user, tty, %cpu, rss.
	cursor, pidStr, ok := nextField(line, 0)
	if !ok {
		return Process{}, false
	}
	cursor, ppidStr, ok := nextField(line, cursor)
	if !ok {
		return Process{}, false
	}
	cursor, user, ok := nextField(line, cursor)
	if !ok {
		return Process{}, false
	}
	cursor, ttyRaw, ok := nextField(line, cursor)
	if !ok {
		return Process{}, false
	}
	cursor, cpuStr, ok := nextField(line, cursor)
	if !ok {
		return Process{}, false
	}
	cursor, rssStr, ok := nextField(line, cursor)
	if !ok {
		return Process{}, false
	}

	// Next five tokens make up the lstart timestamp ("Mon Jan _2 HH:MM:SS YYYY").
	// We consume them by tracking the cursor and joining the spans verbatim,
	// because the embedded double-space (single-digit day) matters to time.Parse.
	startStart := skipSpaces(line, cursor)
	cursor = startStart
	for i := 0; i < 5; i++ {
		var ok2 bool
		cursor, _, ok2 = nextField(line, cursor)
		if !ok2 {
			return Process{}, false
		}
	}
	startEnd := cursor
	startedRaw := strings.TrimSpace(line[startStart:startEnd])

	// Everything after the lstart block (skipping the separator whitespace)
	// is the COMMAND tail. We keep the raw form (with argv) for slow-path
	// matching but compute a basename for the cheap `command` field.
	commandRaw := strings.TrimRight(strings.TrimLeft(line[startEnd:], " \t"), " \t")
	if commandRaw == "" {
		return Process{}, false
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return Process{}, false
	}
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil {
		return Process{}, false
	}
	cpu, err := strconv.ParseFloat(cpuStr, 64)
	if err != nil {
		return Process{}, false
	}
	rssKB, err := strconv.ParseInt(rssStr, 10, 64)
	if err != nil {
		return Process{}, false
	}

	tty := ttyRaw
	if tty == "??" || tty == "?" {
		tty = ""
	}

	startedAt, err := time.ParseInLocation(lstartLayout, startedRaw, time.Local)
	if err != nil {
		// Keep going with a zero StartedAt — startedRaw still surfaces in StartedRaw.
		startedAt = time.Time{}
	}

	return Process{
		ID:         ProcessID{Host: host, PID: pid},
		PPID:       ppid,
		User:       user,
		TTY:        tty,
		CPUPercent: cpu,
		MemBytes:   rssKB * 1024,
		StartedAt:  startedAt,
		StartedRaw: startedRaw,
		Command:    commandBasename(commandRaw),
		CommandRaw: commandRaw,
	}, true
}

// commandBasename pulls the executable basename from the COMMAND tail.
// COMMAND on macOS ps may include the full path plus argv; we want the
// short program name so `command` filters work on `claude`, `sleep`, etc.
func commandBasename(raw string) string {
	// Cut at the first whitespace — anything after is argv.
	progPath := raw
	if idx := strings.IndexAny(progPath, " \t"); idx >= 0 {
		progPath = progPath[:idx]
	}
	return filepath.Base(progPath)
}

// nextField returns the cursor after the next whitespace-delimited token
// in s starting at offset, plus the token itself. ok=false when offset
// is past the end of the line or no token is found.
func nextField(s string, offset int) (int, string, bool) {
	start := skipSpaces(s, offset)
	if start >= len(s) {
		return offset, "", false
	}
	end := start
	for end < len(s) && s[end] != ' ' && s[end] != '\t' {
		end++
	}
	if end == start {
		return offset, "", false
	}
	return end, s[start:end], true
}

// skipSpaces advances offset past spaces and tabs in s.
func skipSpaces(s string, offset int) int {
	for offset < len(s) && (s[offset] == ' ' || s[offset] == '\t') {
		offset++
	}
	return offset
}

// parseArgs converts the raw output of `ps -wwax -o pid,args` into a
// pid → argv map. The first line is the header `  PID ARGS`. Each
// subsequent line is `<pid> <space-joined-argv>`.
func parseArgs(raw string) (map[int][]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	if !scanner.Scan() {
		return nil, fmt.Errorf("ps args: empty output")
	}
	header := strings.Join(strings.Fields(scanner.Text()), " ")
	if header != "PID ARGS" && header != "PID COMMAND" {
		return nil, fmt.Errorf("ps args: unexpected header %q", header)
	}
	out := make(map[int][]string)
	for scanner.Scan() {
		line := scanner.Text()
		cursor, pidStr, ok := nextField(line, 0)
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		// Everything past the pid token (after its trailing whitespace) is argv.
		argsStart := skipSpaces(line, cursor)
		if argsStart >= len(line) {
			out[pid] = nil
			continue
		}
		argv := strings.Fields(line[argsStart:])
		out[pid] = argv
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ps args: scan: %w", err)
	}
	return out, nil
}
