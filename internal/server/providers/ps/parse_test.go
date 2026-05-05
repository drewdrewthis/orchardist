package ps

import (
	"strings"
	"testing"
	"time"
)

// fixture mirrors the column layout of macOS `ps -ax -o pid,ppid,user,tty,%cpu,rss,lstart,command`.
// Synthesised from the documented column shape so the parser is exercised
// against the real header/whitespace contract; usernames and pids are
// generic placeholders.
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

	// Spot-check the first row — short, well-known.
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
		t.Errorf("Command basename = %q, want launchd", first.Command)
	}
	if !strings.HasPrefix(first.CommandRaw, "/sbin/launchd") {
		t.Errorf("CommandRaw = %q, want /sbin/launchd…", first.CommandRaw)
	}
	wantStart := time.Date(2026, time.May, 3, 15, 38, 46, 0, time.Local)
	if !first.StartedAt.Equal(wantStart) {
		t.Errorf("StartedAt = %v, want %v", first.StartedAt, wantStart)
	}

	// Verify the long-argv row keeps the full COMMAND tail in CommandRaw.
	docker := procs[1]
	if docker.Command != "com.docker.virtualization" {
		t.Errorf("Command = %q, want com.docker.virtualization", docker.Command)
	}
	if !strings.Contains(docker.CommandRaw, "--cmdline init=/initd") {
		t.Errorf("CommandRaw should preserve argv, got %q", docker.CommandRaw)
	}

	// Verify a real TTY survives.
	zsh := procs[2]
	if zsh.TTY != "s001" {
		t.Errorf("TTY = %q, want s001", zsh.TTY)
	}
	if zsh.Command != "-zsh" {
		t.Errorf("Command = %q, want -zsh (login shell argv0 is preserved verbatim)", zsh.Command)
	}

	// Verify a row with parenthesised "agent name" — UserEventAgent (System).
	agent := procs[3]
	if agent.Command != "UserEventAgent" {
		t.Errorf("Command = %q, want UserEventAgent", agent.Command)
	}
	if !strings.Contains(agent.CommandRaw, "(System)") {
		t.Errorf("CommandRaw should keep agent label, got %q", agent.CommandRaw)
	}
}

// fixturePsHeaderTT mirrors procps-ng 4.x output (Debian bookworm-class
// boxes): identical column data to the macOS layout but the 4th header
// token spells `TT` instead of `TTY`. The parser must accept both.
const fixturePsHeaderTT = `  PID  PPID USER             TT        %CPU    RSS STARTED                      COMMAND
    1     0 root             ??         1.5  13088 Sun May  3 15:38:46 2026     /sbin/launchd
  262   797 alice            ??         0.0  18432 Sun May  3 16:06:57 2026     /Applications/Docker.app/Contents/MacOS/com.docker.virtualization --kernel /foo --cmdline init=/initd
17729 17726 alice            s001       0.0   1856 Tue Mar 23 10:00:00 2026     -zsh
99999     1 root             ??         0.0    100 Mon Jan 12 00:00:00 2026     /usr/libexec/UserEventAgent (System)
`

// TestParsePs_AcceptsLinuxTTHeader is the AC-1 regression: the daemon
// must boot on procps-ng 4.x even though `-o tty` produces a `TT`
// column header. Identical row count and field values to the macOS
// fixture — only the header label differs.
func TestParsePs_AcceptsLinuxTTHeader(t *testing.T) {
	procsTTY, err := parsePs("local", fixturePsHeaderAndRows)
	if err != nil {
		t.Fatalf("parsePs (TTY header): %v", err)
	}
	procsTT, err := parsePs("local", fixturePsHeaderTT)
	if err != nil {
		t.Fatalf("parsePs (TT header): %v", err)
	}
	if got, want := len(procsTT), len(procsTTY); got != want {
		t.Fatalf("len(TT) = %d, len(TTY) = %d — must match", got, want)
	}
	for i := range procsTT {
		if procsTT[i].ID != procsTTY[i].ID {
			t.Errorf("row %d: ID mismatch TT=%v TTY=%v", i, procsTT[i].ID, procsTTY[i].ID)
		}
		if procsTT[i].User != procsTTY[i].User {
			t.Errorf("row %d: User mismatch TT=%q TTY=%q", i, procsTT[i].User, procsTTY[i].User)
		}
		if procsTT[i].TTY != procsTTY[i].TTY {
			t.Errorf("row %d: TTY mismatch TT=%q TTY=%q", i, procsTT[i].TTY, procsTTY[i].TTY)
		}
		if procsTT[i].Command != procsTTY[i].Command {
			t.Errorf("row %d: Command mismatch TT=%q TTY=%q", i, procsTT[i].Command, procsTTY[i].Command)
		}
		if procsTT[i].CommandRaw != procsTTY[i].CommandRaw {
			t.Errorf("row %d: CommandRaw mismatch TT=%q TTY=%q", i, procsTT[i].CommandRaw, procsTTY[i].CommandRaw)
		}
	}
}

func TestParsePs_RejectsUnknownHeader(t *testing.T) {
	bad := "PID NAME\n1 launchd\n"
	if _, err := parsePs("local", bad); err == nil {
		t.Fatal("expected error on unknown header, got nil")
	}
}

func TestParsePs_SkipsMalformedLines(t *testing.T) {
	// One bad line in the middle should not stop the parser; the good
	// rows must still come through.
	mixed := `  PID  PPID USER             TTY       %CPU    RSS STARTED                      COMMAND
    1     0 root             ??         1.5  13088 Sun May  3 15:38:46 2026     /sbin/launchd
not a process line at all
    2     0 root             ??         0.5  10000 Sun May  3 15:38:47 2026     /sbin/foo
`
	procs, err := parsePs("local", mixed)
	if err != nil {
		t.Fatalf("parsePs: %v", err)
	}
	if got := len(procs); got != 2 {
		t.Fatalf("len(procs) = %d, want 2 (one malformed row skipped)", got)
	}
}

func TestParsePs_EmptyInput(t *testing.T) {
	if _, err := parsePs("local", ""); err == nil {
		t.Fatal("expected error on empty input")
	}
}

func TestParseArgs_HappyPath(t *testing.T) {
	fixture := `  PID ARGS
    1 /sbin/launchd
  262 /Applications/Docker.app/Contents/MacOS/com.docker.virtualization --kernel /foo
17729 -zsh
`
	got, err := parseArgs(fixture)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if want := []string{"/sbin/launchd"}; !equalStrings(got[1], want) {
		t.Errorf("argv[1] = %v, want %v", got[1], want)
	}
	if got[262][0] != "/Applications/Docker.app/Contents/MacOS/com.docker.virtualization" {
		t.Errorf("argv[262][0] = %q, want docker path", got[262][0])
	}
	if got[262][1] != "--kernel" || got[262][2] != "/foo" {
		t.Errorf("argv[262] tail = %v, want [--kernel /foo]", got[262][1:])
	}
	if want := []string{"-zsh"}; !equalStrings(got[17729], want) {
		t.Errorf("argv[17729] = %v, want %v", got[17729], want)
	}
}

func TestProcessID_RoundTrip(t *testing.T) {
	id := ProcessID{Host: "local", PID: 17729}
	parsed, err := ParseProcessID(id.String())
	if err != nil {
		t.Fatalf("ParseProcessID: %v", err)
	}
	if parsed != id {
		t.Errorf("round-trip: got %+v, want %+v", parsed, id)
	}
}

func TestProcessID_RejectsMalformed(t *testing.T) {
	cases := []string{"", "no-colon", "host:not-a-number", ":42", "host:"}
	for _, c := range cases {
		if _, err := ParseProcessID(c); err == nil {
			t.Errorf("ParseProcessID(%q): expected error, got nil", c)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
