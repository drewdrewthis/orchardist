package server

import (
	"net/http"
	"testing"
)

func TestCheckGUIOrigin(t *testing.T) {
	t.Setenv("ORCHARD_ALLOWED_ORIGINS", "")

	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{"missing origin (native client)", "", true},
		{"tauri scheme", "tauri://localhost", true},
		{"http localhost no port", "http://localhost", true},
		{"http localhost with port", "http://localhost:5173", true},
		{"https localhost with port", "https://localhost:5173", true},
		{"http 127.0.0.1 with port", "http://127.0.0.1:7777", true},
		{"https 127.0.0.1 with port", "https://127.0.0.1:8000", true},
		{"hostile external origin", "https://evil.example.com", false},
		{"non-loopback tailnet web", "https://orchard.example.trycloudflare.com", false},
		{"sneaky prefix match attempt", "http://localhost.evil.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{Header: http.Header{}}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			got := checkGUIOrigin(r)
			if got != tc.want {
				t.Errorf("checkGUIOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

func TestCheckGUIOrigin_ExtendedAllowlist(t *testing.T) {
	t.Setenv("ORCHARD_ALLOWED_ORIGINS", "https://a.trycloudflare.com, https://b.ngrok-free.app")

	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{"first extended", "https://a.trycloudflare.com", true},
		{"second extended", "https://b.ngrok-free.app", true},
		{"not in extended list", "https://c.trycloudflare.com", false},
		{"still allows localhost", "http://localhost:5173", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{Header: http.Header{}}
			r.Header.Set("Origin", tc.origin)
			got := checkGUIOrigin(r)
			if got != tc.want {
				t.Errorf("checkGUIOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

func TestSplitAndTrim(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}},
		{",,a,,", []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := splitAndTrim(tc.in, ',')
			if len(got) != len(tc.want) {
				t.Fatalf("len(splitAndTrim(%q)) = %d, want %d (got %v)", tc.in, len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("splitAndTrim(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}
