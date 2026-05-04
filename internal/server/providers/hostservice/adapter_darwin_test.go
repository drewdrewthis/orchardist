//go:build darwin

package hostservice

import (
	"context"
	"errors"
	"testing"
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

// TestMacAdapter_StateActive uses canned `launchctl list` output (PID
// present, LastExitStatus 0) and asserts state=active.
//
// PII guard: every Label / output line uses generic
// `com.example.test.<n>` names. No real bundle ids.
func TestMacAdapter_StateActive(t *testing.T) {
	stub := stubLaunchctl{
		stdout: map[string]string{
			"com.example.test.active": activeLaunchctlOut,
		},
		code: map[string]int{"com.example.test.active": 0},
	}
	a := macAdapter{commander: stub}

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

// TestMacAdapter_StateUnknown — launchctl exits non-zero with "Could
// not find service ..." stderr; the adapter maps to state=unknown.
func TestMacAdapter_StateUnknown(t *testing.T) {
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
	if snap.State != StateUnknown {
		t.Errorf("State = %q, want unknown", snap.State)
	}
	if snap.ExitCode != nil {
		t.Errorf("ExitCode = %v, want nil for unknown service", *snap.ExitCode)
	}
	if snap.Since != nil || snap.LogTail != nil {
		t.Errorf("optional fields populated for unknown service: since=%v logTail=%v", snap.Since, snap.LogTail)
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
		exitCode *int
	}{
		{name: "active with PID", input: activeLaunchctlOut, want: StateActive, exitCode: nil},
		{name: "inactive zero exit", input: inactiveLaunchctlOut, want: StateInactive, exitCode: intp(0)},
		{name: "failed nonzero exit", input: failedLaunchctlOut, want: StateFailed, exitCode: intp(78)},
		{name: "no PID no LastExit", input: emptyLaunchctlOut, want: StateInactive, exitCode: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotExit, err := parseLaunchctlList(tc.input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got != tc.want {
				t.Errorf("State = %q, want %q", got, tc.want)
			}
			switch {
			case tc.exitCode == nil && gotExit != nil:
				t.Errorf("ExitCode = %d, want nil", *gotExit)
			case tc.exitCode != nil && gotExit == nil:
				t.Errorf("ExitCode = nil, want %d", *tc.exitCode)
			case tc.exitCode != nil && gotExit != nil && *tc.exitCode != *gotExit:
				t.Errorf("ExitCode = %d, want %d", *gotExit, *tc.exitCode)
			}
		})
	}
}

func intp(v int) *int { return &v }

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
