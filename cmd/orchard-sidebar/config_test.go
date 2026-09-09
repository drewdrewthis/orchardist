package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// envMap adapts a map to the getenv signature resolveEndpoints takes, so a
// case states exactly the two variables and nothing leaks from the process
// environment.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// AC1 + AC2: the default is today's daemon URLs unchanged, an explicit backend
// resolves its documented default URLs, and an explicit override derives its WS
// URL by a general scheme swap — not a match against the two known constants.
func TestResolveEndpoints(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantHTTP string
		wantWS   string
		wantErr  bool
		errFrag  string // substring the error must name, when wantErr
	}{
		{
			name:     "unset defaults to daemon, byte-for-byte today",
			env:      map[string]string{},
			wantHTTP: "http://127.0.0.1:7777/graphql",
			wantWS:   "ws://127.0.0.1:7777/graphql",
		},
		{
			name:     "explicit daemon",
			env:      map[string]string{"ORCHARD_SIDEBAR_BACKEND": "daemon"},
			wantHTTP: "http://127.0.0.1:7777/graphql",
			wantWS:   "ws://127.0.0.1:7777/graphql",
		},
		{
			name:     "explicit supergraph",
			env:      map[string]string{"ORCHARD_SIDEBAR_BACKEND": "supergraph"},
			wantHTTP: "http://127.0.0.1:7788/graphql",
			wantWS:   "ws://127.0.0.1:7788/graphql",
		},
		{
			name: "override on a non-default host/port/path derives WS by scheme swap",
			env: map[string]string{
				"ORCHARD_SIDEBAR_BACKEND":     "supergraph",
				"ORCHARD_SIDEBAR_GRAPHQL_URL": "http://10.0.0.5:9000/graphql",
			},
			wantHTTP: "http://10.0.0.5:9000/graphql",
			wantWS:   "ws://10.0.0.5:9000/graphql",
		},
		{
			name: "https override derives wss, preserving host and path",
			env: map[string]string{
				"ORCHARD_SIDEBAR_GRAPHQL_URL": "https://supergraph.example.com/graphql",
			},
			wantHTTP: "https://supergraph.example.com/graphql",
			wantWS:   "wss://supergraph.example.com/graphql",
		},
		{
			name:    "unknown backend is a hard error naming the value and both options",
			env:     map[string]string{"ORCHARD_SIDEBAR_BACKEND": "bogus"},
			wantErr: true,
			errFrag: "bogus",
		},
		{
			name:    "malformed URL override is a hard error naming the value",
			env:     map[string]string{"ORCHARD_SIDEBAR_GRAPHQL_URL": "not-a-url"},
			wantErr: true,
			errFrag: "not-a-url",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveEndpoints(envMap(c.env))
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error naming %q, got endpoints %+v", c.errFrag, got)
				}
				if !strings.Contains(err.Error(), c.errFrag) {
					t.Errorf("error %q does not name %q", err.Error(), c.errFrag)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.httpURL != c.wantHTTP {
				t.Errorf("httpURL = %q, want %q", got.httpURL, c.wantHTTP)
			}
			if got.wsURL != c.wantWS {
				t.Errorf("wsURL = %q, want %q", got.wsURL, c.wantWS)
			}
		})
	}
}

// The unknown-backend error names BOTH valid options, so the message tells the
// user what to type — not just that they were wrong.
func TestUnknownBackendErrorNamesBothOptions(t *testing.T) {
	_, err := resolveEndpoints(envMap(map[string]string{"ORCHARD_SIDEBAR_BACKEND": "bogus"}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"bogus", "daemon", "supergraph"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err.Error(), want)
		}
	}
}

// buildSidebar compiles the binary once for the exit-code cases. Building is the
// only way to observe os.Exit; resolveEndpoints' pure error is asserted above.
func buildSidebar(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "orchard-sidebar")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build sidebar: %v", err)
	}
	return bin
}

// AC3: an unknown backend value and a malformed URL override each exit the
// process non-zero with a stderr message naming the bad value — before any
// tmux or network I/O (the binary exits well inside the timeout, having never
// reached tea.NewProgram or a dial).
func TestStartupFailsLoudly(t *testing.T) {
	bin := buildSidebar(t)
	cases := []struct {
		name    string
		env     []string
		wantOut string
	}{
		{"unknown backend", []string{"ORCHARD_SIDEBAR_BACKEND=bogus"}, "bogus"},
		{"malformed URL override", []string{"ORCHARD_SIDEBAR_GRAPHQL_URL=not-a-url"}, "not-a-url"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(bin)
			cmd.Env = append(os.Environ(), c.env...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			done := make(chan error, 1)
			if err := cmd.Start(); err != nil {
				t.Fatalf("start: %v", err)
			}
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("want non-zero exit, got success; stderr=%q", stderr.String())
				}
				if !strings.Contains(stderr.String(), c.wantOut) {
					t.Errorf("stderr %q does not name %q", stderr.String(), c.wantOut)
				}
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatal("binary did not exit — it should fail before any I/O")
			}
		})
	}
}
