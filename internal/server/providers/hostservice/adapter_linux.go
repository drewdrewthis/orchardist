//go:build linux

package hostservice

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// linuxAdapter shells systemd-user CLI tools per watched name.
//
//   - state    : `systemctl --user is-active <name>` →
//     active   → StateActive
//     inactive → StateInactive
//     failed   → StateFailed
//     any non-zero exit + "Unit not loaded" stderr → StateNotInstalled
//     any non-zero exit + unrecognised stderr      → StateUnknown
//   - since    : `systemctl --user show -p ActiveEnterTimestamp <name>`
//   - exitCode : `systemctl --user show -p ExecMainStatus <name>`
//   - logTail  : `journalctl --user -u <name> --no-pager -n 20`
//
// systemd accepts both bare names ("orchard") and full unit names
// ("orchard.service"). We pass the bare name through — systemctl is
// lenient and the operator's config-spelling wins.
type linuxAdapter struct {
	systemctl  systemctlCommander
	journalctl journalctlCommander
}

// systemctlCommander indirects systemd I/O so tests stub fake binaries.
type systemctlCommander interface {
	Run(ctx context.Context, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// journalctlCommander same purpose for journal log-tail reads.
type journalctlCommander interface {
	Run(ctx context.Context, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// NewAdapter returns the Linux Adapter wired to PATH-resolved systemctl
// and journalctl. Tests construct linuxAdapter{...} with stub commanders.
func NewAdapter() Adapter {
	return linuxAdapter{
		systemctl:  execCommanderLinux{bin: "systemctl"},
		journalctl: execCommanderLinux{bin: "journalctl"},
	}
}

// execCommanderLinux is the on-PATH commander for systemctl/journalctl.
// Returns ErrServiceManagerMissing when systemctl is absent so the
// resolver can surface the configured per-field error. journalctl
// missing is non-fatal (logTail just stays nil).
type execCommanderLinux struct{ bin string }

func (e execCommanderLinux) Run(ctx context.Context, args ...string) ([]byte, []byte, int, error) {
	if _, err := exec.LookPath(e.bin); err != nil {
		if e.bin == "systemctl" {
			return nil, nil, 0, ErrServiceManagerMissing
		}
		return nil, nil, 127, nil
	}
	cmd := exec.CommandContext(ctx, e.bin, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			err = nil
		}
	}
	return []byte(outBuf.String()), []byte(errBuf.String()), exitCode, err
}

// FetchOne runs the three systemd reads + the journal tail. is-active is
// the gate: an unknown unit short-circuits everything else and returns
// state=unknown.
func (l linuxAdapter) FetchOne(ctx context.Context, hostID, name string) (Snapshot, error) {
	stdout, stderr, _, err := l.systemctl.Run(ctx, "--user", "is-active", name)
	if errors.Is(err, ErrServiceManagerMissing) {
		return Snapshot{}, ErrServiceManagerMissing
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("systemctl --user is-active %s: %w", name, err)
	}

	rawState := strings.TrimSpace(string(stdout))

	// `is-active` exits non-zero for inactive AND failed AND unknown.
	// Disambiguate by stdout — systemd writes the state name even when
	// the exit code is non-zero.
	state, knownUnit := mapIsActive(rawState)
	if !knownUnit {
		// stderr contains "Unit X not loaded" or "Failed to get
		// properties" when the unit isn't installed. Disambiguate from
		// genuinely uninterpretable output — `not_installed` for the
		// former, `unknown` for the latter.
		if isUnknownUnitMessage(string(stderr)) {
			return Snapshot{
				HostID: hostID,
				Name:   name,
				State:  StateNotInstalled,
			}, nil
		}
		// Empty stdout + non-zero exit + unrecognised stderr: surface
		// as state=unknown so peers keep resolving and the operator
		// notices something unexpected.
		return Snapshot{
			HostID: hostID,
			Name:   name,
			State:  StateUnknown,
		}, nil
	}

	snap := Snapshot{HostID: hostID, Name: name, State: state}

	// `show` is best-effort — failures here keep the snapshot but leave
	// the optional fields nil.
	if since, exitCode, err := l.readShow(ctx, name); err == nil {
		snap.Since = since
		snap.ExitCode = exitCode
	}

	if tail, err := l.readJournalTail(ctx, name); err == nil && tail != "" {
		snap.LogTail = &tail
	}

	return snap, nil
}

// mapIsActive lifts a systemd is-active stdout token to (State, knownUnit).
// Returns (_, false) when the token doesn't match any of the v1 states —
// caller treats that as state=unknown if stderr corroborates.
func mapIsActive(token string) (State, bool) {
	switch token {
	case "active", "activating", "reloading":
		return StateActive, true
	case "inactive", "deactivating":
		return StateInactive, true
	case "failed":
		return StateFailed, true
	default:
		return "", false
	}
}

// readShow runs `systemctl --user show -p ActiveEnterTimestamp -p
// ExecMainStatus <name>` and parses the key=value output. systemd
// always emits both keys even when empty, so format is stable.
func (l linuxAdapter) readShow(ctx context.Context, name string) (*time.Time, *int, error) {
	stdout, _, _, err := l.systemctl.Run(ctx, "--user", "show",
		"-p", "ActiveEnterTimestamp",
		"-p", "ExecMainStatus",
		"--no-pager",
		name,
	)
	if err != nil {
		return nil, nil, err
	}
	var since *time.Time
	var exitCode *int
	for _, line := range strings.Split(string(stdout), "\n") {
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "ActiveEnterTimestamp":
			if val == "" || val == "n/a" {
				continue
			}
			t, perr := parseSystemdTimestamp(val)
			if perr == nil {
				since = &t
			}
		case "ExecMainStatus":
			if val == "" {
				continue
			}
			n, perr := strconv.Atoi(val)
			if perr == nil {
				exitCode = &n
			}
		}
	}
	return since, exitCode, nil
}

// parseSystemdTimestamp parses systemd's wall-clock format: e.g.
// "Mon 2026-05-04 12:34:56 UTC". Multiple zoneless / zoned variants
// exist; we try the most common formats and fall back to RFC 3339.
func parseSystemdTimestamp(s string) (time.Time, error) {
	layouts := []string{
		"Mon 2006-01-02 15:04:05 MST",
		"Mon 2006-01-02 15:04:05",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised systemd timestamp %q", s)
}

// readJournalTail returns the last 20 lines of the unit's journal,
// truncating any trailing newline. Empty result + no error is treated
// as "no entries available" by the caller.
func (l linuxAdapter) readJournalTail(ctx context.Context, name string) (string, error) {
	stdout, _, code, err := l.journalctl.Run(ctx, "--user", "-u", name, "--no-pager", "-n", "20")
	if err != nil {
		return "", err
	}
	if code != 0 {
		// journalctl returns non-zero when the journal is unavailable
		// (no permissions, no entries). Caller treats as "no tail".
		return "", nil
	}
	return strings.TrimRight(string(stdout), "\n"), nil
}

// isUnknownUnitMessage detects the systemd-stderr surface for an
// unknown unit. "Unit X not loaded" / "Failed to get properties" /
// "Failed to issue method call" — match loosely on the stable parts.
func isUnknownUnitMessage(stderr string) bool {
	s := strings.ToLower(stderr)
	if strings.Contains(s, "not loaded") {
		return true
	}
	if strings.Contains(s, "could not be found") {
		return true
	}
	if strings.Contains(s, "no such file or directory") {
		return true
	}
	if strings.Contains(s, "no such unit") {
		return true
	}
	return false
}
