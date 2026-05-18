//go:build linux

package hostservices

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
//   - state    : `systemctl --user is-active <name>`
//   - since    : `systemctl --user show -p ActiveEnterTimestamp <name>`
//   - exitCode : `systemctl --user show -p ExecMainStatus <name>`
//   - logTail  : `journalctl --user -u <name> --no-pager -n 20`
type linuxAdapter struct {
	systemctl  systemctlCommander
	journalctl journalctlCommander
}

// systemctlCommander indirects systemd I/O so tests can stub fake binaries.
type systemctlCommander interface {
	Run(ctx context.Context, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// journalctlCommander same purpose for journal log-tail reads.
type journalctlCommander interface {
	Run(ctx context.Context, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// newAdapter returns the Linux adapter wired to PATH-resolved binaries.
func newAdapter() adapter {
	return linuxAdapter{
		systemctl:  execCommanderLinux{bin: "systemctl"},
		journalctl: execCommanderLinux{bin: "journalctl"},
	}
}

// execCommanderLinux is the on-PATH commander for systemctl/journalctl.
// Returns ErrServiceManagerMissing when systemctl is absent; journalctl
// missing is non-fatal (logTail stays nil).
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

// fetchOne runs the three systemd reads + the journal tail. Implements
// the adapter interface.
func (l linuxAdapter) fetchOne(ctx context.Context, machineID, name string) (HostServiceSnapshot, error) {
	stdout, stderr, _, err := l.systemctl.Run(ctx, "--user", "is-active", name)
	if errors.Is(err, ErrServiceManagerMissing) {
		return HostServiceSnapshot{}, ErrServiceManagerMissing
	}
	if err != nil {
		return HostServiceSnapshot{}, fmt.Errorf("systemctl --user is-active %s: %w", name, err)
	}

	rawState := strings.TrimSpace(string(stdout))
	state, knownUnit := mapIsActive(rawState)
	if !knownUnit {
		if isUnknownUnitMessage(string(stderr)) {
			return HostServiceSnapshot{
				MachineID: machineID,
				Name:      name,
				State:     StateNotInstalled,
			}, nil
		}
		return HostServiceSnapshot{
			MachineID: machineID,
			Name:      name,
			State:     StateUnknown,
		}, nil
	}

	snap := HostServiceSnapshot{MachineID: machineID, Name: name, State: state}

	if since, exitCode, err := l.readShow(ctx, name); err == nil {
		snap.Since = since
		snap.ExitCode = exitCode
	}

	if tail, err := l.readJournalTail(ctx, name); err == nil && tail != "" {
		snap.LogTail = &tail
	}

	return snap, nil
}

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

func (l linuxAdapter) readJournalTail(ctx context.Context, name string) (string, error) {
	stdout, _, code, err := l.journalctl.Run(ctx, "--user", "-u", name, "--no-pager", "-n", "20")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", nil
	}
	return strings.TrimRight(string(stdout), "\n"), nil
}

func isUnknownUnitMessage(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "not loaded") ||
		strings.Contains(s, "could not be found") ||
		strings.Contains(s, "no such file or directory") ||
		strings.Contains(s, "no such unit")
}
