package claudeprojects

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeJSONL writes lines as newline-terminated JSON to a temp file.
func writeJSONL(t *testing.T, lines []string) (string, int64) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return path, info.Size()
}

func TestReadLatestRecap_ReturnsNilWhenAbsent(t *testing.T) {
	path, size := writeJSONL(t, []string{
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"hi"}}`,
	})
	got, err := readLatestRecap(path, size)
	if err != nil {
		t.Fatalf("readLatestRecap: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil recap; got %q", *got)
	}
}

func TestReadLatestRecap_ExtractsLocalCommandStdout(t *testing.T) {
	path, size := writeJSONL(t, []string{
		`{"type":"user","message":{"role":"user","content":"<command-name>/recap</command-name>\n<command-message>recap</command-message>"}}`,
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout>We decided X. Next: Y.</local-command-stdout>"}`,
	})
	got, err := readLatestRecap(path, size)
	if err != nil {
		t.Fatalf("readLatestRecap: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil recap")
	}
	const want = "We decided X. Next: Y."
	if *got != want {
		t.Errorf("recap = %q; want %q", *got, want)
	}
}

func TestReadLatestRecap_LastInvocationWins(t *testing.T) {
	path, size := writeJSONL(t, []string{
		`{"type":"user","message":{"role":"user","content":"<command-name>/recap</command-name>"}}`,
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout>FIRST</local-command-stdout>"}`,
		`{"type":"user","message":{"role":"user","content":"some other prompt"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"reply"}}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/recap</command-name>"}}`,
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout>LATEST</local-command-stdout>"}`,
	})
	got, err := readLatestRecap(path, size)
	if err != nil {
		t.Fatalf("readLatestRecap: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil recap")
	}
	if *got != "LATEST" {
		t.Errorf("recap = %q; want LATEST", *got)
	}
}

func TestReadLatestRecap_IgnoresUnrelatedSlashCommands(t *testing.T) {
	path, size := writeJSONL(t, []string{
		`{"type":"user","message":{"role":"user","content":"<command-name>/status</command-name>"}}`,
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout>status output</local-command-stdout>"}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/memory</command-name>"}}`,
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout>memory dump</local-command-stdout>"}`,
	})
	got, err := readLatestRecap(path, size)
	if err != nil {
		t.Fatalf("readLatestRecap: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil; got %q", *got)
	}
}

func TestReadLatestRecap_HandlesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readLatestRecap(path, 0)
	if err != nil {
		t.Fatalf("readLatestRecap: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil on empty file; got %q", *got)
	}
}

func TestReadLatestRecap_RecapBetweenUnrelatedRecords(t *testing.T) {
	// Real-world shape: many turns precede the recap, and the recap is
	// followed by more turns. The walk-backwards-from-tail logic must
	// not mis-pair with later records.
	path, size := writeJSONL(t, []string{
		`{"type":"user","message":{"role":"user","content":"first prompt"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"first reply"}}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/recap</command-name>\n<command-args></command-args>"}}`,
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout>The recap text spans multiple words.</local-command-stdout>"}`,
		`{"type":"user","message":{"role":"user","content":"keep going"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"sure"}}`,
	})
	got, err := readLatestRecap(path, size)
	if err != nil {
		t.Fatalf("readLatestRecap: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil recap")
	}
	if *got != "The recap text spans multiple words." {
		t.Errorf("recap = %q", *got)
	}
}
