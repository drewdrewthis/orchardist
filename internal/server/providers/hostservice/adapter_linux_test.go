//go:build linux

package hostservice

import (
	"context"
	"errors"
	"testing"
)

// stubSystemctl returns canned output keyed by the unit name (the last
// argument of every systemctl invocation we make).
type stubSystemctl struct {
	isActiveStdout map[string]string
	isActiveStderr map[string]string
	isActiveCode   map[string]int
	showStdout     map[string]string
	err            map[string]error
}

func (s stubSystemctl) Run(_ context.Context, args ...string) ([]byte, []byte, int, error) {
	if len(args) == 0 {
		return nil, nil, 1, errors.New("stubSystemctl: empty args")
	}
	verb := args[1]
	name := args[len(args)-1]
	if e, ok := s.err[name]; ok && e != nil {
		return nil, nil, 0, e
	}
	switch verb {
	case "is-active":
		return []byte(s.isActiveStdout[name]), []byte(s.isActiveStderr[name]), s.isActiveCode[name], nil
	case "show":
		return []byte(s.showStdout[name]), nil, 0, nil
	default:
		return nil, nil, 1, errors.New("stubSystemctl: unhandled verb " + verb)
	}
}

// stubJournalctl returns a canned tail.
type stubJournalctl struct {
	stdout map[string]string
	code   map[string]int
}

func (s stubJournalctl) Run(_ context.Context, args ...string) ([]byte, []byte, int, error) {
	name := nameFromArgs(args)
	return []byte(s.stdout[name]), nil, s.code[name], nil
}

// nameFromArgs picks the unit name from `--user -u <name> --no-pager -n
// 20`. Returns "" if not present.
func nameFromArgs(args []string) string {
	for i, a := range args {
		if a == "-u" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// TestLinuxAdapter_StateActive — systemctl is-active says "active",
// show emits ExecMainStatus=0 + ActiveEnterTimestamp.
//
// PII guard: every unit name uses generic
// `example-test-<n>.service`. No real services.
func TestLinuxAdapter_StateActive(t *testing.T) {
	a := linuxAdapter{
		systemctl: stubSystemctl{
			isActiveStdout: map[string]string{"example-test-active": "active\n"},
			isActiveCode:   map[string]int{"example-test-active": 0},
			showStdout: map[string]string{
				"example-test-active": "ActiveEnterTimestamp=Mon 2026-05-04 12:34:56 UTC\nExecMainStatus=0\n",
			},
		},
		journalctl: stubJournalctl{stdout: map[string]string{"example-test-active": "Jan 01 00:00:00 host example: started\n"}},
	}

	snap, err := a.FetchOne(context.Background(), "host-id", "example-test-active")
	if err != nil {
		t.Fatalf("FetchOne: %v", err)
	}
	if snap.State != StateActive {
		t.Errorf("State = %q, want active", snap.State)
	}
	if snap.Since == nil {
		t.Error("Since is nil; want parsed timestamp")
	}
	if snap.LogTail == nil {
		t.Error("LogTail is nil; want journalctl content")
	}
}

// TestLinuxAdapter_StateInactive — systemctl is-active says "inactive"
// (exit code 3 in real systemd, but the stdout is the disambiguator).
func TestLinuxAdapter_StateInactive(t *testing.T) {
	a := linuxAdapter{
		systemctl: stubSystemctl{
			isActiveStdout: map[string]string{"example-test-inactive": "inactive\n"},
			isActiveCode:   map[string]int{"example-test-inactive": 3},
			showStdout:     map[string]string{"example-test-inactive": "ActiveEnterTimestamp=\nExecMainStatus=0\n"},
		},
		journalctl: stubJournalctl{},
	}

	snap, err := a.FetchOne(context.Background(), "host-id", "example-test-inactive")
	if err != nil {
		t.Fatalf("FetchOne: %v", err)
	}
	if snap.State != StateInactive {
		t.Errorf("State = %q, want inactive", snap.State)
	}
}

// TestLinuxAdapter_StateFailed — systemctl is-active says "failed",
// ExecMainStatus surfaces the non-zero exit code.
func TestLinuxAdapter_StateFailed(t *testing.T) {
	a := linuxAdapter{
		systemctl: stubSystemctl{
			isActiveStdout: map[string]string{"example-test-failed": "failed\n"},
			isActiveCode:   map[string]int{"example-test-failed": 3},
			showStdout:     map[string]string{"example-test-failed": "ActiveEnterTimestamp=Mon 2026-05-04 12:00:00 UTC\nExecMainStatus=42\n"},
		},
		journalctl: stubJournalctl{stdout: map[string]string{"example-test-failed": "Jan 01 00:00:00 host example: exit 42\n"}},
	}

	snap, err := a.FetchOne(context.Background(), "host-id", "example-test-failed")
	if err != nil {
		t.Fatalf("FetchOne: %v", err)
	}
	if snap.State != StateFailed {
		t.Errorf("State = %q, want failed", snap.State)
	}
	if snap.ExitCode == nil || *snap.ExitCode != 42 {
		got := -1
		if snap.ExitCode != nil {
			got = *snap.ExitCode
		}
		t.Errorf("ExitCode = %d, want 42", got)
	}
}

// TestLinuxAdapter_StateUnknown — systemctl exits non-zero with
// stderr like "Unit example-test-missing.service not loaded."
func TestLinuxAdapter_StateUnknown(t *testing.T) {
	a := linuxAdapter{
		systemctl: stubSystemctl{
			isActiveStderr: map[string]string{
				"example-test-missing": "Unit example-test-missing.service not loaded.\n",
			},
			isActiveCode: map[string]int{"example-test-missing": 4},
		},
	}

	snap, err := a.FetchOne(context.Background(), "host-id", "example-test-missing")
	if err != nil {
		t.Fatalf("FetchOne: %v", err)
	}
	if snap.State != StateUnknown {
		t.Errorf("State = %q, want unknown", snap.State)
	}
	if snap.Since != nil || snap.ExitCode != nil || snap.LogTail != nil {
		t.Errorf("optional fields populated for unknown unit: since=%v exitCode=%v logTail=%v", snap.Since, snap.ExitCode, snap.LogTail)
	}
}

// TestLinuxAdapter_ServiceManagerMissingSurfaces — when systemctl is
// absent from PATH, the adapter returns the typed sentinel so the
// resolver can surface a per-field GraphQL error.
func TestLinuxAdapter_ServiceManagerMissingSurfaces(t *testing.T) {
	a := linuxAdapter{
		systemctl: stubSystemctl{err: map[string]error{"any": ErrServiceManagerMissing}},
	}

	_, err := a.FetchOne(context.Background(), "host-id", "any")
	if !errors.Is(err, ErrServiceManagerMissing) {
		t.Fatalf("err = %v, want ErrServiceManagerMissing", err)
	}
}

// TestMapIsActive_TableDriven pins the systemd-state-name → State
// mapping so a future tweak can't silently re-map a state.
func TestMapIsActive_TableDriven(t *testing.T) {
	cases := []struct {
		token string
		want  State
		known bool
	}{
		{"active", StateActive, true},
		{"activating", StateActive, true},
		{"reloading", StateActive, true},
		{"inactive", StateInactive, true},
		{"deactivating", StateInactive, true},
		{"failed", StateFailed, true},
		{"unknown-token", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.token, func(t *testing.T) {
			got, known := mapIsActive(tc.token)
			if got != tc.want {
				t.Errorf("State = %q, want %q", got, tc.want)
			}
			if known != tc.known {
				t.Errorf("knownUnit = %t, want %t", known, tc.known)
			}
		})
	}
}

// TestParseSystemdTimestamp covers the layouts we support so a feed
// from a real systemd installation parses cleanly.
func TestParseSystemdTimestamp(t *testing.T) {
	cases := []string{
		"Mon 2026-05-04 12:34:56 UTC",
		"2026-05-04T12:34:56Z",
		"2026-05-04 12:34:56 UTC",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if _, err := parseSystemdTimestamp(in); err != nil {
				t.Errorf("parse %q: %v", in, err)
			}
		})
	}
	if _, err := parseSystemdTimestamp("garbage"); err == nil {
		t.Error("expected error for garbage timestamp, got nil")
	}
}
