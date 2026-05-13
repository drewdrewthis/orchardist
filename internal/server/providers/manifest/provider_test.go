package manifest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleYAML = `schema_version: 1
last_update: "2026-05-13"
hosts:
  - name: drudrukungfu
    role: local_orchardist
    address: local
    purpose: "Drew's Mac."
    owner_orchardist: local_orchardist
    decommission_signal: never
    last_verified: "2026-05-13"
  - name: orchard.boxd.sh
    role: boxd_orchardist
    address: "boxd@orchard.boxd.sh"
    purpose: "Always-on Hetzner VM."
    owner_orchardist: boxd_orchardist
    decommission_signal: never
    last_verified: "2026-05-13"
  - name: issue3201
    role: fork_per_issue
    address: "boxd@issue3201.boxd.sh"
    purpose: "Dedicated VM for lw#3201."
    owner_orchardist: boxd_orchardist
    decommission_signal: "lw#3201 closed AND PR merged"
    last_verified: unknown
`

// writeManifest drops sampleYAML at a temp path and returns it. Lets
// tests share fixture content without copy/pasting.
func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet-manifest.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestParseManifest_HappyPath(t *testing.T) {
	entries, err := parseManifest([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if got, want := len(entries), 3; got != want {
		t.Fatalf("got %d entries, want %d", got, want)
	}
	if entries[0].Name != "drudrukungfu" || entries[0].Role != "local_orchardist" {
		t.Fatalf("first entry projected wrong: %+v", entries[0])
	}
	if entries[2].LastVerified != "unknown" {
		t.Fatalf("bareword last_verified should round-trip as a string, got %q", entries[2].LastVerified)
	}
}

func TestParseManifest_DropsBlankAndDuplicateNames(t *testing.T) {
	body := `hosts:
  - name: "  "
    role: federation_worker
  - name: lw-fed-c
    role: federation_worker
  - name: lw-fed-c
    role: dedicated_grinder
`
	entries, err := parseManifest([]byte(body))
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if got, want := len(entries), 1; got != want {
		t.Fatalf("got %d entries, want %d (blank dropped + duplicate dropped)", got, want)
	}
	if entries[0].Role != "federation_worker" {
		t.Fatalf("first occurrence should win, got role %q", entries[0].Role)
	}
}

func TestParseManifest_EmptyBytes(t *testing.T) {
	got, err := parseManifest(nil)
	if err != nil || got != nil {
		t.Fatalf("parseManifest(nil) = %v, %v; want nil, nil", got, err)
	}
}

func TestParseManifest_RejectsBrokenYAML(t *testing.T) {
	// `{not: closed` parses as an unterminated flow map.
	_, err := parseManifest([]byte("{not: closed"))
	if err == nil {
		t.Fatal("expected parse error on broken yaml, got nil")
	}
	if !strings.Contains(err.Error(), "manifest yaml") {
		t.Fatalf("error should mention manifest yaml, got %q", err.Error())
	}
}

func TestProvider_Start_LoadsAndExposes(t *testing.T) {
	path := writeManifest(t, sampleYAML)
	p := New(path, WithInterval(time.Hour))
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	st := p.Status()
	if !st.Loaded {
		t.Fatalf("expected loaded=true, got %+v", st)
	}
	if st.Error != "" {
		t.Fatalf("expected no error, got %q", st.Error)
	}
	if st.HostCount != 3 {
		t.Fatalf("expected 3 entries, got %d", st.HostCount)
	}
	if st.Path != path {
		t.Fatalf("status.Path = %q, want %q", st.Path, path)
	}

	if _, ok := p.LookupByName("orchard.boxd.sh"); !ok {
		t.Fatal("LookupByName should find orchard.boxd.sh")
	}
	if _, ok := p.LookupByName("does-not-exist"); ok {
		t.Fatal("LookupByName should not find phantom hosts")
	}

	snap := p.Snapshot()
	snap[0].Name = "mutated"
	if again := p.Snapshot(); again[0].Name == "mutated" {
		t.Fatal("Snapshot must return a copy — external mutation leaked into provider state")
	}
}

func TestProvider_RefreshPicksUpEdits(t *testing.T) {
	path := writeManifest(t, sampleYAML)
	p := New(path, WithInterval(10*time.Millisecond))
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	// Overwrite the file with a smaller manifest.
	if err := os.WriteFile(path, []byte("hosts:\n  - name: solo\n    role: external\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	// Wait up to 2s for the refresh loop to observe the edit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.Status().HostCount == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := p.Status().HostCount; got != 1 {
		t.Fatalf("expected refresh to observe 1 host, still see %d after 2s", got)
	}
}

func TestProvider_MissingFile_IsNotAnError(t *testing.T) {
	dir := t.TempDir()
	p := New(filepath.Join(dir, "absent.yaml"), WithInterval(time.Hour))
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	st := p.Status()
	if !st.Loaded {
		t.Fatalf("missing file should still produce loaded=true (empty manifest), got %+v", st)
	}
	if st.HostCount != 0 {
		t.Fatalf("missing file should produce 0 entries, got %d", st.HostCount)
	}
	if st.Error != "" {
		t.Fatalf("missing file must not produce an error, got %q", st.Error)
	}
}

func TestProvider_BrokenManifest_KeepsLastGoodSnapshot(t *testing.T) {
	path := writeManifest(t, sampleYAML)
	p := New(path, WithInterval(10*time.Millisecond))
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()
	if got := p.Status().HostCount; got != 3 {
		t.Fatalf("warm-up expected 3 entries, got %d", got)
	}

	if err := os.WriteFile(path, []byte("{not: closed"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.Status().Error != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	st := p.Status()
	if st.Error == "" {
		t.Fatal("expected a parse error in status after broken write")
	}
	if st.HostCount != 3 {
		t.Fatalf("expected snapshot to remain at 3 entries on parse error, got %d", st.HostCount)
	}
}

func TestProvider_EmptyPath_ReportsConfigError(t *testing.T) {
	p := New("", WithInterval(time.Hour))
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()
	st := p.Status()
	if st.Loaded {
		t.Fatal("expected loaded=false when no path configured")
	}
	if st.Error == "" {
		t.Fatal("expected an error explaining the missing path")
	}
}

func TestDefaultPath_Env(t *testing.T) {
	t.Setenv(EnvVar, "/custom/path.yaml")
	if got, want := DefaultPath(), "/custom/path.yaml"; got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestProvider_StartIsIdempotent(t *testing.T) {
	path := writeManifest(t, sampleYAML)
	p := New(path, WithInterval(time.Hour))
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	// Second Start must not panic or spawn another loop. We can't easily
	// observe loop count, but we can at least see the call succeed.
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Stop again — must be safe.
	if err := p.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}
