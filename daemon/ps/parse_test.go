package ps

import (
	"strings"
	"testing"
	"time"
)

// fixturePsHeaderAndRows mirrors the column layout of
// `ps -ax -o pid,ppid,user,tty,%cpu,rss,lstart,command` on macOS.
const fixturePsHeaderAndRows = `  PID  PPID USER             TTY       %CPU    RSS STARTED                      COMMAND
    1     0 root             ??         1.5  13088 Sun May  3 15:38:46 2026     /sbin/launchd
  262   797 alice            ??         0.0  18432 Sun May  3 16:06:57 2026     /Applications/Docker.app/Contents/MacOS/com.docker.virtualization --kernel /foo --cmdline init=/initd
17729 17726 alice            s001       0.0   1856 Tue Mar 23 10:00:00 2026     -zsh
99999     1 root             ??         0.0    100 Mon Jan 12 00:00:00 2026     /usr/libexec/UserEventAgent (System)
`

func TestParsePs_HappyPath(t *testing.T) {
	procs, err := parsePs("local", fixturePsHeaderAndRows)
	if err != nil {
		t.Fatalf("parsePs: %v", err)
	}
	if got, want := len(procs), 4; got != want {
		t.Fatalf("len(procs) = %d, want %d", got, want)
	}

	first := procs[0]
	if first.ID.Host != "local" {
		t.Errorf("ID.Host = %q, want %q", first.ID.Host, "local")
	}
	if first.ID.PID != 1 {
		t.Errorf("ID.PID = %d, want 1", first.ID.PID)
	}
	if first.PPID != 0 {
		t.Errorf("PPID = %d, want 0", first.PPID)
	}
	if first.User != "root" {
		t.Errorf("User = %q, want root", first.User)
	}
	if first.TTY != "" {
		t.Errorf(`TTY for "??" should normalise to "", got %q`, first.TTY)
	}
	if first.CPUPercent != 1.5 {
		t.Errorf("CPUPercent = %v, want 1.5", first.CPUPercent)
	}
	if first.MemBytes != 13088*1024 {
		t.Errorf("MemBytes = %d, want %d", first.MemBytes, 13088*1024)
	}
	if first.Command != "launchd" {
		t.Errorf("Command = %q, want launchd", first.Command)
	}
}

func TestParsePs_TTYNormalisation(t *testing.T) {
	procs, err := parsePs("local", fixturePsHeaderAndRows)
	if err != nil {
		t.Fatalf("parsePs: %v", err)
	}
	// Third row has TTY = s001.
	third := procs[2]
	if third.TTY != "s001" {
		t.Errorf("third process TTY = %q, want %q", third.TTY, "s001")
	}
}

func TestParsePs_LstartParsed(t *testing.T) {
	procs, err := parsePs("local", fixturePsHeaderAndRows)
	if err != nil {
		t.Fatalf("parsePs: %v", err)
	}
	first := procs[0]
	if first.StartedAt.IsZero() {
		t.Error("StartedAt should be non-zero for well-formed lstart")
	}
	if first.StartedAt.Year() != 2026 {
		t.Errorf("StartedAt.Year = %d, want 2026", first.StartedAt.Year())
	}
}

func TestParsePs_EmptyOutput(t *testing.T) {
	_, err := parsePs("local", "")
	if err == nil {
		t.Fatal("expected error for empty output, got nil")
	}
}

func TestParsePs_BadHeader(t *testing.T) {
	_, err := parsePs("local", "WRONG HEADER\n1 2 root ?? 0.0 1024 Mon Jan 1 00:00:00 2026 /bin/foo\n")
	if err == nil {
		t.Fatal("expected error for bad header, got nil")
	}
}

func TestParsePs_SingleDigitDay(t *testing.T) {
	// Single-digit day has a leading space in lstart: "Mon Jan  3 ..."
	fixture := `  PID  PPID USER             TTY       %CPU    RSS STARTED                      COMMAND
  100     1 alice            ??         0.0   1024 Mon Jan  3 10:00:00 2026     /usr/bin/foo
`
	procs, err := parsePs("local", fixture)
	if err != nil {
		t.Fatalf("parsePs: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("len(procs) = %d, want 1", len(procs))
	}
	if procs[0].StartedAt.Day() != 3 {
		t.Errorf("StartedAt.Day = %d, want 3", procs[0].StartedAt.Day())
	}
}

func TestParseArgs_HappyPath(t *testing.T) {
	raw := `  PID ARGS
    1 /sbin/launchd
  42 /usr/bin/sleep 30
`
	got, err := parseArgs(raw)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if v := got[1]; len(v) != 1 || v[0] != "/sbin/launchd" {
		t.Errorf("got[1] = %v, want [/sbin/launchd]", v)
	}
	if v := got[42]; len(v) != 2 || v[0] != "/usr/bin/sleep" {
		t.Errorf("got[42] = %v, want [/usr/bin/sleep 30]", v)
	}
}

func TestParseArgs_AlternativeHeader(t *testing.T) {
	// Linux procps-ng uses COMMAND instead of ARGS.
	raw := `  PID COMMAND
    1 /sbin/init
`
	got, err := parseArgs(raw)
	if err != nil {
		t.Fatalf("parseArgs (COMMAND header): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}

func TestValidateHeader_TTProcps(t *testing.T) {
	// procps-ng 4.x emits TT instead of TTY.
	line := "PID PPID USER TT %CPU RSS STARTED COMMAND"
	if err := validateHeader(line); err != nil {
		t.Errorf("validateHeader with TT: %v", err)
	}
}

func TestCommandBasename(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"/sbin/launchd", "launchd"},
		{"/usr/bin/sleep 30", "sleep"},
		{"claude --dangerously-skip-permissions", "claude"},
		{"/Applications/foo.app/Contents/MacOS/foo --flag", "foo"},
	}
	for _, tc := range cases {
		got := commandBasename(tc.raw)
		if got != tc.want {
			t.Errorf("commandBasename(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestSplitLines(t *testing.T) {
	input := []byte("line1\nline2\nline3")
	got := splitLines(input)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0] != "line1" || got[1] != "line2" || got[2] != "line3" {
		t.Errorf("splitLines = %v", got)
	}
}

func TestProcessID_String(t *testing.T) {
	id := ProcessID{Host: "myhost", PID: 1234}
	if got := id.String(); got != "myhost:1234" {
		t.Errorf("String() = %q, want %q", got, "myhost:1234")
	}
}

func TestParseProcessID(t *testing.T) {
	cases := []struct {
		input   string
		want    ProcessID
		wantErr bool
	}{
		{"myhost:1234", ProcessID{"myhost", 1234}, false},
		{"host.with.dots:99", ProcessID{"host.with.dots", 99}, false},
		{"malformed", ProcessID{}, true},
		{":notapid", ProcessID{}, true},
		{"host:", ProcessID{}, true},
	}
	for _, tc := range cases {
		got, err := ParseProcessID(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseProcessID(%q): expected error, got nil", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseProcessID(%q): %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseProcessID(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestProcessEqualsHotPath(t *testing.T) {
	a := Process{PPID: 1, User: "alice", TTY: "s001", Command: "sleep", StartedRaw: "Mon"}
	b := a
	if !processEqualsHotPath(a, b) {
		t.Error("identical processes should be equal")
	}
	b.CPUPercent = 99.9 // hot-path ignores CPU
	if !processEqualsHotPath(a, b) {
		t.Error("CPU change should not affect hot-path equality")
	}
	b.Command = "bash"
	if processEqualsHotPath(a, b) {
		t.Error("Command change should make processes unequal")
	}
}

// Verify time formatting in ProjectProcess.
func TestProjectProcess_Fields(t *testing.T) {
	p := Process{
		ID:         ProcessID{Host: "h", PID: 7},
		PPID:       3,
		CPUPercent: 2.5,
		MemBytes:   8192,
		StartedAt:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		StartedRaw: "raw",
		Command:    "myproc",
	}
	proj := ProjectProcess(&p, "h")
	if proj.ID != "h:7" {
		t.Errorf("ID = %q, want %q", proj.ID, "h:7")
	}
	if proj.Pid != 7 {
		t.Errorf("Pid = %d, want 7", proj.Pid)
	}
	if proj.Ppid != 3 {
		t.Errorf("Ppid = %d, want 3", proj.Ppid)
	}
	if proj.CPUPercent != 2.5 {
		t.Errorf("CPUPercent = %v, want 2.5", proj.CPUPercent)
	}
	if proj.MemBytes != 8192 {
		t.Errorf("MemBytes = %d, want 8192", proj.MemBytes)
	}
	if proj.Command != "myproc" {
		t.Errorf("Command = %q, want myproc", proj.Command)
	}
	want := p.StartedAt.Format(time.RFC3339)
	if proj.StartedAt != want {
		t.Errorf("StartedAt = %q, want RFC3339 %q", proj.StartedAt, want)
	}
	if !strings.Contains(proj.ID, "h:") {
		t.Errorf("ID should contain host prefix, got %q", proj.ID)
	}
}
