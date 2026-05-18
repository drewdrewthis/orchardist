//go:build darwin

package hostservices

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubLaunchctl feeds canned `launchctl list` output into the parser.
type stubLaunchctl struct {
	stdout map[string]string
	stderr map[string]string
	code   map[string]int
	err    map[string]error
}

func (s stubLaunchctl) Run(_ context.Context, args ...string) ([]byte, []byte, int, error) {
	if len(args) < 2 {
		return nil, nil, 1, errors.New("stubLaunchctl: need at least 2 args")
	}
	label := args[1]
	if e, ok := s.err[label]; ok && e != nil {
		return nil, nil, 0, e
	}
	return []byte(s.stdout[label]), []byte(s.stderr[label]), s.code[label], nil
}

// stubPS returns canned `ps -p <pid> -o lstart=` results keyed by pid.
type stubPS struct {
	starts map[int]time.Time
}

func (s stubPS) LStart(_ context.Context, pid int) (time.Time, error) {
	if t, ok := s.starts[pid]; ok {
		return t, nil
	}
	return time.Time{}, errors.New("stubPS: pid not found")
}

func TestMacAdapter_StateActive(t *testing.T) {
	want := time.Date(2026, 5, 4, 11, 36, 2, 0, time.UTC)
	stub := stubLaunchctl{
		stdout: map[string]string{
			"com.example.test.active": activeLaunchctlOut,
		},
		code: map[string]int{"com.example.test.active": 0},
	}
	a := macAdapter{
		commander:   stub,
		psCommander: stubPS{starts: map[int]time.Time{12345: want}},
	}

	snap, err := a.fetchOne(context.Background(), "host-id", "com.example.test.active")
	if err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if snap.State != StateActive {
		t.Errorf("State = %q, want active", snap.State)
	}
	if snap.ExitCode != nil {
		t.Errorf("ExitCode = %v, want nil for active service", *snap.ExitCode)
	}
	if snap.Since == nil {
		t.Fatal("Since is nil; want lstart-derived timestamp")
	}
	if !snap.Since.Equal(want) {
		t.Errorf("Since = %v, want %v", *snap.Since, want)
	}
}

func TestMacAdapter_StateActive_PsFailsFallsBackToNow(t *testing.T) {
	stub := stubLaunchctl{
		stdout: map[string]string{"com.example.test.active": activeLaunchctlOut},
		code:   map[string]int{"com.example.test.active": 0},
	}
	before := time.Now()
	a := macAdapter{
		commander:   stub,
		psCommander: stubPS{},
	}

	snap, err := a.fetchOne(context.Background(), "host-id", "com.example.test.active")
	if err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if snap.State != StateActive {
		t.Errorf("State = %q, want active", snap.State)
	}
	if snap.Since == nil {
		t.Fatal("Since is nil; want now-fallback when ps fails")
	}
	if snap.Since.Before(before) {
		t.Errorf("Since = %v, want >= %v (now fallback)", *snap.Since, before)
	}
}

func TestMacAdapter_StateInactive(t *testing.T) {
	stub := stubLaunchctl{
		stdout: map[string]string{"com.example.test.inactive": inactiveLaunchctlOut},
		code:   map[string]int{"com.example.test.inactive": 0},
	}
	a := macAdapter{commander: stub}

	snap, err := a.fetchOne(context.Background(), "host-id", "com.example.test.inactive")
	if err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if snap.State != StateInactive {
		t.Errorf("State = %q, want inactive", snap.State)
	}
	if snap.ExitCode == nil || *snap.ExitCode != 0 {
		got := -1
		if snap.ExitCode != nil {
			got = *snap.ExitCode
		}
		t.Errorf("ExitCode = %d, want 0 for clean stop", got)
	}
}

func TestMacAdapter_StateFailed(t *testing.T) {
	stub := stubLaunchctl{
		stdout: map[string]string{"com.example.test.failed": failedLaunchctlOut},
		code:   map[string]int{"com.example.test.failed": 0},
	}
	a := macAdapter{commander: stub}

	snap, err := a.fetchOne(context.Background(), "host-id", "com.example.test.failed")
	if err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if snap.State != StateFailed {
		t.Errorf("State = %q, want failed", snap.State)
	}
	if snap.ExitCode == nil || *snap.ExitCode != 78 {
		got := -1
		if snap.ExitCode != nil {
			got = *snap.ExitCode
		}
		t.Errorf("ExitCode = %d, want 78", got)
	}
}

func TestMacAdapter_StateNotInstalled(t *testing.T) {
	stub := stubLaunchctl{
		stderr: map[string]string{
			"com.example.test.missing": `Could not find service "com.example.test.missing" in domain for system` + "\n",
		},
		code: map[string]int{"com.example.test.missing": 113},
	}
	a := macAdapter{commander: stub}

	snap, err := a.fetchOne(context.Background(), "host-id", "com.example.test.missing")
	if err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if snap.State != StateNotInstalled {
		t.Errorf("State = %q, want not_installed", snap.State)
	}
	if snap.ExitCode != nil || snap.Since != nil || snap.LogTail != nil {
		t.Errorf("optional fields populated for not-installed: %+v", snap)
	}
}

func TestMacAdapter_StateUnknown(t *testing.T) {
	stub := stubLaunchctl{
		stderr: map[string]string{
			"com.example.test.weird": "launchctl: an unrecognised internal error occurred (-42)\n",
		},
		code: map[string]int{"com.example.test.weird": 1},
	}
	a := macAdapter{commander: stub}

	snap, err := a.fetchOne(context.Background(), "host-id", "com.example.test.weird")
	if err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if snap.State != StateUnknown {
		t.Errorf("State = %q, want unknown", snap.State)
	}
}

func TestMacAdapter_ServiceManagerMissing(t *testing.T) {
	stub := stubLaunchctl{
		err: map[string]error{"com.example.test.any": ErrServiceManagerMissing},
	}
	a := macAdapter{commander: stub}

	_, err := a.fetchOne(context.Background(), "host-id", "com.example.test.any")
	if !errors.Is(err, ErrServiceManagerMissing) {
		t.Fatalf("err = %v, want ErrServiceManagerMissing", err)
	}
}

func TestParseLaunchctlList_TableDriven(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		want     State
		pid      *int
		exitCode *int
	}{
		{name: "active with PID", input: activeLaunchctlOut, want: StateActive, pid: intp(12345), exitCode: nil},
		{name: "inactive zero exit", input: inactiveLaunchctlOut, want: StateInactive, pid: nil, exitCode: intp(0)},
		{name: "failed nonzero exit", input: failedLaunchctlOut, want: StateFailed, pid: nil, exitCode: intp(78)},
		{name: "no PID no LastExit", input: emptyLaunchctlOut, want: StateInactive, pid: nil, exitCode: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotPid, gotExit, err := parseLaunchctlList(tc.input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got != tc.want {
				t.Errorf("State = %q, want %q", got, tc.want)
			}
			assertIntPtrEq(t, "PID", gotPid, tc.pid)
			assertIntPtrEq(t, "ExitCode", gotExit, tc.exitCode)
		})
	}
}

func TestParsePsLStart_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{
			name: "single digit day",
			in:   "Mon May  4 11:36:02 2026\n",
			want: time.Date(2026, 5, 4, 11, 36, 2, 0, time.Local),
		},
		{
			name: "double digit day",
			in:   "Tue May 12 09:01:30 2026",
			want: time.Date(2026, 5, 12, 9, 1, 30, 0, time.Local),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePsLStart(tc.in)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
	if _, err := parsePsLStart(""); err == nil {
		t.Error("expected error for empty input, got nil")
	}
	if _, err := parsePsLStart("garbage"); err == nil {
		t.Error("expected error for garbage input, got nil")
	}
}

func intp(v int) *int { return &v }

func assertIntPtrEq(t *testing.T, label string, got, want *int) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %d, want nil", label, *got)
	case want != nil && got == nil:
		t.Errorf("%s = nil, want %d", label, *want)
	case want != nil && got != nil && *want != *got:
		t.Errorf("%s = %d, want %d", label, *got, *want)
	}
}

const activeLaunchctlOut = `{
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "com.example.test.active";
	"OnDemand" = false;
	"LastExitStatus" = 0;
	"PID" = 12345;
	"Program" = "/usr/local/bin/example";
};
`

const inactiveLaunchctlOut = `{
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "com.example.test.inactive";
	"OnDemand" = true;
	"LastExitStatus" = 0;
	"Program" = "/usr/local/bin/example";
};
`

const failedLaunchctlOut = `{
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "com.example.test.failed";
	"OnDemand" = true;
	"LastExitStatus" = 78;
	"Program" = "/usr/local/bin/example";
};
`

const emptyLaunchctlOut = `{
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "com.example.test.empty";
	"OnDemand" = true;
	"Program" = "/usr/local/bin/example";
};
`
