package hostservice

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestLoadServicesFromConfig_FileMissing asserts a missing config file
// returns the canonical defaults. This is the cold-boot path (the
// daemon comes up before `orchard config init` has run).
func TestLoadServicesFromConfig_FileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	got, err := LoadServicesFromConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, DefaultServices) {
		t.Errorf("got %v, want defaults %v", got, DefaultServices)
	}
}

// TestLoadServicesFromConfig_KeyMissing asserts a config file without
// the `services` key returns defaults. Mirrors the case where ws-b-config
// has written a `{"version":1, "projects":[...]}` file but the operator
// hasn't customised the watchlist.
func TestLoadServicesFromConfig_KeyMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version": 1, "projects": []}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadServicesFromConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, DefaultServices) {
		t.Errorf("got %v, want defaults %v", got, DefaultServices)
	}
}

// TestLoadServicesFromConfig_EmptyArray asserts an explicitly empty
// `services: []` falls back to defaults. Operators that want NO
// watchlist need to remove the key entirely; an empty array is treated
// as "I haven't configured this yet" rather than "watch nothing".
func TestLoadServicesFromConfig_EmptyArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"services": []}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadServicesFromConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, DefaultServices) {
		t.Errorf("got %v, want defaults %v", got, DefaultServices)
	}
}

// TestLoadServicesFromConfig_CustomList asserts a populated services
// array round-trips through the loader, and that order is preserved.
func TestLoadServicesFromConfig_CustomList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"services": ["foo", "bar", "baz"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadServicesFromConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"foo", "bar", "baz"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestLoadServicesFromConfig_DedupesAndSkipsBlank asserts duplicates
// and blank entries are collapsed. We do not warn about either — the
// daemon should be tolerant of operator typos.
func TestLoadServicesFromConfig_DedupesAndSkipsBlank(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"services": ["foo", "", "foo", "bar", "foo"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadServicesFromConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"foo", "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestLoadServicesFromConfig_OnlyBlanksFallsBackToDefaults asserts that
// a `services` array containing only blank entries is treated as empty
// (i.e. defaults apply). Same rationale as EmptyArray — operator typo
// shouldn't blank the watchlist.
func TestLoadServicesFromConfig_OnlyBlanksFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"services": ["", ""]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadServicesFromConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, DefaultServices) {
		t.Errorf("got %v, want defaults %v", got, DefaultServices)
	}
}

// TestLoadServicesFromConfig_MalformedJSONErrors asserts a corrupt file
// surfaces an error so the operator notices. We do NOT silently fall
// back to defaults on malformed JSON because that would hide bugs in
// `orchard config` writers.
func TestLoadServicesFromConfig_MalformedJSONErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{not json}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadServicesFromConfig(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}
