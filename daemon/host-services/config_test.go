package hostservices

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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

func TestLoadServicesFromConfig_KeyMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version": 1}`), 0o600); err != nil {
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

func TestLoadServicesFromConfig_MalformedJSON(t *testing.T) {
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
