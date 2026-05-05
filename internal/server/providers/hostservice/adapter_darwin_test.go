//go:build darwin

package hostservice

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubLaunchctl is a deterministic launchctlCommander used to feed canned
// `launchctl list <Label>` output into the parser. Key by Label so each
// case can register its own response.
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
// A test that does not register a pid gets a zero time and an error,
// which the adapter treats as "fall back to now".
type stubPS struct {
	starts map[int]time.Time
}

func (s stubPS) LStart(_ context.Context, pid int) (time.Time, error) {
	if t, ok := s.starts[pid]; ok {
		return t, nil
	}
	return time.Time{}, errors.New("stubPS: pid not found")
}

// TestMacAdapter_StateActive uses canned `launchctl list` output (PID
// present, LastExitStatus 0) and asserts state=active. The stubbed
// psRunner supplies a deterministic lstart so the test can assert
// `since` matches the expected wall-clock time.
//
// PII guard: every Label / output line uses generic
// `com.example.test.<n>` names. No real bundle ids.
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

	snap, err := a.FetchOne(context.Background(), "host-id", "com.example.test.active")
	if err != nil {
		t.Fatalf("FetchOne: %v", err)
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

// TestMacAdapter_StateActive_PsFailsFallsBackToNow asserts that when ps
// can't be reached (no PATH binary, pid already reaped, etc.) the
// adapter still surfaces a non-null `since` so the GraphQL contract
// for active units is honoured.
func TestMacAdapter_StateActive_PsFailsFallsBackToNow(t *testing.T) {
	stub := stubLaunchctl{
		stdout: map[string]string{"com.example.test.active": activeLaunchctlOut},
		code:   map[string]int{"com.example.test.active": 0},
	}
	before := time.Now()
	a := macAdapter{
		commander:   stub,
		psCommander: stubPS{}, // no entry for pid 12345 → returns error
	}

	snap, err := a.FetchOne(context.Background(), "host-id", "com.example.test.active")
	if err != nil {
		t.Fatalf("FetchOne: %v", err)
	}
	if snap.State != StateActive {
		t.Errorf("State = %q, want active", snap.State)
	}
	if snap.Since == nil {
		t.Fatal("Since is nil; want now-fallback when ps fails")
	}
	if snap.Since.Before(before) {
		t.Errorf("Since = %v, want >= %v (the now fallback)", *snap.Since, before)
	}
}

// TestMacAdapter_StateInactive — PID absent + LastExitStatus 0.
func TestMacAdapter_StateInactive(t *testing.T) {
	stub := stubLaunchctl{
		stdout: map[string]string{
			"com.example.test.inactive": inactiveLaunchctlOut,
		},
		code: map[string]int{"com.example.test.inactive": 0},
	}
	a := macAdapter{commander: stub}

	snap, err := a.FetchOne(context.Background(), "host-id", "com.example.test.inactive")
	if err != nil {
		t.Fatalf("FetchOne: %v", err)
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

// TestMacAdapter_StateFailed — PID absent + LastExitStatus != 0.
func TestMacAdapter_StateFailed(t *testing.T) {
	stub := stubLaunchctl{
		stdout: map[string]string{
			"com.example.test.failed": failedLaunchctlOut,
		},
		code: map[string]int{"com.example.test.failed": 0},
	}
	a := macAdapter{commander: stub}

	snap, err := a.FetchOne(context.Background(), "host-id", "com.example.test.failed")
	if err != nil {
		t.Fatalf("FetchOne: %v", err)
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

// TestMacAdapter_StateNotInstalled — launchctl exits non-zero with
// "Could not find service ..." stderr; the adapter maps to
// state=not_installed (ADR-011 §5.1: `unknown` is reserved for
// uninterpretable output).
func TestMacAdapter_StateNotInstalled(t *testing.T) {
	stub := stubLaunchctl{
		stderr: map[string]string{
			"com.example.test.missing": "Could not find service \"com.example.test.missing\" in domain for system\n",
		},
		code: map[string]int{"com.example.test.missing": 113},
	}
	a := macAdapter{commander: stub}

	snap, err := a.FetchOne(context.Background(), "host-id", "com.example.test.missing")
	if err != nil {
		t.Fatalf("FetchOne: %v", err)
	}
	if snap.State != StateNotInstalled {
		t.Errorf("State = %q, want not_installed", snap.State)
	}
	if snap.ExitCode != nil {
		t.Errorf("ExitCode = %v, want nil for not-installed service", *snap.ExitCode)
	}
	if snap.Since != nil || snap.LogTail != nil {
		t.Errorf("optional fields populated for not-installed service: since=%v logTail=%v", snap.Since, snap.LogTail)
	}
}

// TestMacAdapter_StateUnknown — launchctl exits non-zero with stderr
// the adapter does NOT recognise as "unit not loaded". Surfaces as
// state=unknown so the operator notices something unexpected without
// the daemon erroring the field. Distinguishes this case from
// state=not_installed (the explicit "unit absent" case).
func TestMacAdapter_StateUnknown(t *testing.T) {
	stub := stubLaunchctl{
		stderr: map[string]string{
			"com.example.test.weird": "launchctl: an unrecognised internal error occurred (-42)\n",
		},
		code: map[string]int{"com.example.test.weird": 1},
	}
	a := macAdapter{commander: stub}

	snap, err := a.FetchOne(context.Background(), "host-id", "com.example.test.weird")
	if err != nil {
		t.Fatalf("FetchOne: %v", err)
	}
	if snap.State != StateUnknown {
		t.Errorf("State = %q, want unknown", snap.State)
	}
	if snap.ExitCode != nil || snap.Since != nil || snap.LogTail != nil {
		t.Errorf("optional fields populated for unknown stderr: %+v", snap)
	}
}

// TestMacAdapter_ServiceManagerMissingSurfaces — when launchctl is
// absent (e.g. inside a stripped sandbox), the adapter returns the
// typed sentinel so the resolver can surface a per-field GraphQL error.
func TestMacAdapter_ServiceManagerMissingSurfaces(t *testing.T) {
	stub := stubLaunchctl{
		err: map[string]error{
			"com.example.test.any": ErrServiceManagerMissing,
		},
	}
	a := macAdapter{commander: stub}

	_, err := a.FetchOne(context.Background(), "host-id", "com.example.test.any")
	if !errors.Is(err, ErrServiceManagerMissing) {
		t.Fatalf("err = %v, want ErrServiceManagerMissing", err)
	}
}

// TestParseLaunchctlList_TableDriven covers every state mapping in one
// go. Pins the parser against future tweaks.
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

// TestParsePsLStart_TableDriven pins the BSD-ps lstart format we accept.
// Single-digit days have a double-space which strings.Fields collapses.
func TestParsePsLStart_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want time.Time
	}{
		{
			name: "single digit day collapsed",
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

// activeLaunchctlOut is a `launchctl list <Label>` snapshot for a
// running unit. PID = 12345, LastExitStatus = 0. Generic Label keeps
// the fixture PII-free per worker standards.
const activeLaunchctlOut = `{
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "com.example.test.active";
	"OnDemand" = false;
	"LastExitStatus" = 0;
	"PID" = 12345;
	"Program" = "/usr/local/bin/example";
};
`

// inactiveLaunchctlOut — clean stop. PID absent, LastExitStatus = 0.
const inactiveLaunchctlOut = `{
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "com.example.test.inactive";
	"OnDemand" = true;
	"LastExitStatus" = 0;
	"Program" = "/usr/local/bin/example";
};
`

// failedLaunchctlOut — crashed. PID absent, LastExitStatus = 78.
// (78 = EX_CONFIG, popular among launchd-failure stories.)
const failedLaunchctlOut = `{
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "com.example.test.failed";
	"OnDemand" = true;
	"LastExitStatus" = 78;
	"Program" = "/usr/local/bin/example";
};
`

// emptyLaunchctlOut — never run. No PID, no LastExitStatus.
const emptyLaunchctlOut = `{
	"LimitLoadToSessionType" = "Aqua";
	"Label" = "com.example.test.empty";
	"OnDemand" = true;
	"Program" = "/usr/local/bin/example";
};
`
