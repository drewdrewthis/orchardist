package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stubPathLookup is a minimal PathLookup for tests.
type stubPathLookup struct {
	m map[string]string // sessionUUID → path
}

func (s *stubPathLookup) PathForSessionUUID(_ context.Context, uuid string) (string, bool) {
	p, ok := s.m[uuid]
	return p, ok
}

// makeFixture writes content to a temp file and returns its path plus a
// stub lookup pointing uuid at it.
func makeFixture(t *testing.T, uuid string, content []byte) (string, *stubPathLookup) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, uuid+".jsonl")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	lookup := &stubPathLookup{m: map[string]string{uuid: path}}
	return path, lookup
}

// newTestServer spins up an httptest.Server with the handler mounted at
// /v1/conversations/. It returns the server and its base URL.
func newTestServer(t *testing.T, lookup PathLookup) (*httptest.Server, string) {
	t.Helper()
	h := NewConversationsJSONLHandler(lookup, "/unused-root-for-this-task", slog.Default())
	mux := http.NewServeMux()
	mux.Handle("/v1/conversations/", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.URL
}

// =============================================================================
// AC2 — full GET (no Range) returns 200 + full body + correct headers
// =============================================================================

// TestConversationsJSONL_FullGet asserts: 200, byte-identical body, correct
// Content-Type, ETag present, Last-Modified present.
//
// Feature: "GET /v1/conversations/:sessionUuid/jsonl with no Range returns
//
//	200 + full body + headers"
func TestConversationsJSONL_FullGet(t *testing.T) {
	const uuid = "9f8e-uuid-1"
	// Build 4321-byte fixture (AC2 specifies exactly 4321 bytes on disk).
	content := bytes.Repeat([]byte("x"), 4321)
	_, lookup := makeFixture(t, uuid, content)
	_, baseURL := newTestServer(t, lookup)

	url := fmt.Sprintf("%s/v1/conversations/%s/jsonl", baseURL, uuid)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Status
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// Body byte-identical to disk
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, content) {
		t.Errorf("body length = %d, want %d; content mismatch", len(body), len(content))
	}

	// Content-Type must be exactly application/x-ndjson (not sniffed)
	ct := resp.Header.Get("Content-Type")
	if ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/x-ndjson")
	}

	// ETag must be present
	if etag := resp.Header.Get("ETag"); etag == "" {
		t.Error("ETag header missing")
	}

	// Last-Modified must be present
	if lm := resp.Header.Get("Last-Modified"); lm == "" {
		t.Error("Last-Modified header missing")
	}
}

// TestConversationsJSONL_ContentTypeNotSniffed asserts that the Content-Type
// is exactly "application/x-ndjson" regardless of file content, not sniffed
// to "application/json" or "text/plain".
func TestConversationsJSONL_ContentTypeNotSniffed(t *testing.T) {
	const uuid = "ct-sniff-test"
	// JSON-looking content that stdlib might sniff as application/json.
	content := []byte(`{"type":"user","message":"hello"}` + "\n")
	_, lookup := makeFixture(t, uuid, content)
	_, baseURL := newTestServer(t, lookup)

	resp, err := http.Get(fmt.Sprintf("%s/v1/conversations/%s/jsonl", baseURL, uuid)) //nolint:noctx
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	ct := resp.Header.Get("Content-Type")
	if ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want %q (must not be sniffed)", ct, "application/x-ndjson")
	}
}

// =============================================================================
// AC2 — ETag stability and change (unit-level: httptest.NewRecorder)
// =============================================================================

// TestConversationsJSONL_ETagStable asserts that two back-to-back GETs
// against an unchanged file carry identical ETags.
//
// Feature: "ETag is stable across reads when the file is unchanged"
func TestConversationsJSONL_ETagStable(t *testing.T) {
	const uuid = "etag-stable-uuid"
	content := bytes.Repeat([]byte("line\n"), 100)
	_, lookup := makeFixture(t, uuid, content)

	h := NewConversationsJSONLHandler(lookup, "/unused", slog.Default())

	firstETag := etagForRequest(t, h, uuid)
	secondETag := etagForRequest(t, h, uuid)

	if firstETag == "" {
		t.Fatal("first ETag is empty")
	}
	if firstETag != secondETag {
		t.Errorf("ETag changed between reads: first=%q second=%q", firstETag, secondETag)
	}
}

// TestConversationsJSONL_ETagChanges asserts that after the file is
// appended to (size/mtime change), a subsequent GET carries a different ETag.
//
// Feature: "ETag changes when the underlying file changes"
func TestConversationsJSONL_ETagChanges(t *testing.T) {
	const uuid = "etag-change-uuid"
	content := bytes.Repeat([]byte("line\n"), 100)
	path, lookup := makeFixture(t, uuid, content)

	h := NewConversationsJSONLHandler(lookup, "/unused", slog.Default())

	etagBefore := etagForRequest(t, h, uuid)
	if etagBefore == "" {
		t.Fatal("first ETag is empty")
	}

	// Append to the file so size changes, then bump mtime so the ETag
	// formula (size + mtime-ns) definitely differs.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	_, _ = f.WriteString("extra line\n")
	_ = f.Close()

	// Ensure mtime advances at least 1 nanosecond. On filesystems with
	// coarse mtime resolution (1-second), also touch the mtime forward
	// to guarantee a change. We set it one second in the future.
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	etagAfter := etagForRequest(t, h, uuid)
	if etagAfter == "" {
		t.Fatal("second ETag is empty")
	}
	if etagBefore == etagAfter {
		t.Errorf("ETag did not change after file modification: %q", etagBefore)
	}
}

// etagForRequest issues a GET via httptest.NewRecorder and returns the ETag
// from the response header. Fails the test on any HTTP error.
func etagForRequest(t *testing.T, h http.Handler, uuid string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/conversations/%s/jsonl", uuid), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	return rr.Header().Get("ETag")
}

// =============================================================================
// AC3 — Range support
// =============================================================================

// TestConversationsJSONL_RangeFromN asserts 206 + correct bytes for
// Range: bytes=2000-
//
// Feature: "GET with Range bytes=N- returns 206 Partial Content with
//
//	bytes from N to EOF"
func TestConversationsJSONL_RangeFromN(t *testing.T) {
	const uuid = "range-from-n"
	content := makeCountedContent(4321)
	_, lookup := makeFixture(t, uuid, content)
	_, baseURL := newTestServer(t, lookup)

	req, _ := http.NewRequest(http.MethodGet, //nolint:noctx
		fmt.Sprintf("%s/v1/conversations/%s/jsonl", baseURL, uuid), nil)
	req.Header.Set("Range", "bytes=2000-")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with Range: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("status = %d, want 206", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// Body must be bytes [2000, 4321) of the fixture.
	wantBody := content[2000:]
	if !bytes.Equal(body, wantBody) {
		t.Errorf("body length = %d, want %d; content mismatch", len(body), len(wantBody))
	}

	// Content-Range: bytes 2000-4320/4321
	wantCR := "bytes 2000-4320/4321"
	if cr := resp.Header.Get("Content-Range"); cr != wantCR {
		t.Errorf("Content-Range = %q, want %q", cr, wantCR)
	}

	// Content-Length: 2321
	if cl := resp.ContentLength; cl != 2321 {
		t.Errorf("Content-Length = %d, want 2321", cl)
	}
}

// TestConversationsJSONL_RangeAB asserts 206 + correct bytes for
// Range: bytes=100-199
//
// Feature: "GET with Range bytes=A-B returns 206 with the closed-interval slice"
func TestConversationsJSONL_RangeAB(t *testing.T) {
	const uuid = "range-a-b"
	content := makeCountedContent(4321)
	_, lookup := makeFixture(t, uuid, content)
	_, baseURL := newTestServer(t, lookup)

	req, _ := http.NewRequest(http.MethodGet, //nolint:noctx
		fmt.Sprintf("%s/v1/conversations/%s/jsonl", baseURL, uuid), nil)
	req.Header.Set("Range", "bytes=100-199")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with Range: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("status = %d, want 206", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// bytes 100-199 is a closed interval → 100 bytes ([100, 200))
	wantBody := content[100:200]
	if !bytes.Equal(body, wantBody) {
		t.Errorf("body length = %d, want 100; content mismatch", len(body))
	}

	// Content-Range: bytes 100-199/4321
	wantCR := "bytes 100-199/4321"
	if cr := resp.Header.Get("Content-Range"); cr != wantCR {
		t.Errorf("Content-Range = %q, want %q", cr, wantCR)
	}
}

// TestConversationsJSONL_RangeOutOfRange asserts 416 Range Not Satisfiable
// when the Range start is beyond EOF.
//
// Feature: "GET with an out-of-range Range returns 416 Range Not Satisfiable"
func TestConversationsJSONL_RangeOutOfRange(t *testing.T) {
	const uuid = "range-oor"
	content := makeCountedContent(4321)
	_, lookup := makeFixture(t, uuid, content)
	_, baseURL := newTestServer(t, lookup)

	req, _ := http.NewRequest(http.MethodGet, //nolint:noctx
		fmt.Sprintf("%s/v1/conversations/%s/jsonl", baseURL, uuid), nil)
	req.Header.Set("Range", "bytes=99999-")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with out-of-range Range: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("status = %d, want 416", resp.StatusCode)
	}

	// Content-Range: bytes */4321
	wantCR := "bytes */4321"
	if cr := resp.Header.Get("Content-Range"); cr != wantCR {
		t.Errorf("Content-Range = %q, want %q", cr, wantCR)
	}
}

// =============================================================================
// AC4 — Conditional GET: If-None-Match
// =============================================================================

// TestConversationsJSONL_IfNoneMatch_Match asserts 304 Not Modified when
// If-None-Match matches the current ETag.
//
// Feature: "GET with If-None-Match matching the current ETag returns
//
//	304 Not Modified"
func TestConversationsJSONL_IfNoneMatch_Match(t *testing.T) {
	const uuid = "inm-match"
	content := bytes.Repeat([]byte("line\n"), 200)
	_, lookup := makeFixture(t, uuid, content)
	_, baseURL := newTestServer(t, lookup)

	// First request — capture the ETag.
	firstURL := fmt.Sprintf("%s/v1/conversations/%s/jsonl", baseURL, uuid)
	resp1, err := http.Get(firstURL) //nolint:noctx
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp1.Body)
	_ = resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first GET status = %d, want 200", resp1.StatusCode)
	}
	etag := resp1.Header.Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing from first response")
	}

	// Second request — send If-None-Match with the captured ETag.
	req, _ := http.NewRequest(http.MethodGet, firstURL, nil) //nolint:noctx
	req.Header.Set("If-None-Match", etag)

	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()

	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("status = %d, want 304", resp2.StatusCode)
	}
	if len(body2) != 0 {
		t.Errorf("304 response body must be empty, got %d bytes", len(body2))
	}
}

// TestConversationsJSONL_IfNoneMatch_NoMatch asserts 200 + full body when
// If-None-Match sends a stale ETag.
//
// Feature: "GET with If-None-Match that no longer matches returns 200
//
//	with the full body"
func TestConversationsJSONL_IfNoneMatch_NoMatch(t *testing.T) {
	const uuid = "inm-no-match"
	content := bytes.Repeat([]byte("line\n"), 200)
	path, lookup := makeFixture(t, uuid, content)
	_, baseURL := newTestServer(t, lookup)

	url := fmt.Sprintf("%s/v1/conversations/%s/jsonl", baseURL, uuid)

	// First request — capture original ETag.
	resp1, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp1.Body)
	_ = resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first GET status = %d, want 200", resp1.StatusCode)
	}
	oldETag := resp1.Header.Get("ETag")
	if oldETag == "" {
		t.Fatal("ETag missing from first response")
	}

	// Mutate the file so the ETag changes.
	extra := []byte("\nextra content to change mtime+size\n")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	_, _ = f.Write(extra)
	_ = f.Close()

	// Bump mtime to guarantee ETag change even on coarse-grained filesystems.
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Second request with the old (now stale) ETag.
	req, _ := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx
	req.Header.Set("If-None-Match", oldETag)

	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp2.StatusCode)
	}

	wantLen := len(content) + len(extra)
	if len(body2) != wantLen {
		t.Errorf("body length = %d, want %d", len(body2), wantLen)
	}
}

// =============================================================================
// Method-not-allowed check
// =============================================================================

func TestConversationsJSONL_MethodNotAllowed(t *testing.T) {
	const uuid = "method-test"
	content := []byte("hello\n")
	_, lookup := makeFixture(t, uuid, content)
	_, baseURL := newTestServer(t, lookup)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		method := method
		t.Run(method, func(t *testing.T) {
			req, _ := http.NewRequest(method, //nolint:noctx
				fmt.Sprintf("%s/v1/conversations/%s/jsonl", baseURL, uuid), nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s status = %d, want 405", method, resp.StatusCode)
			}
		})
	}
}

// =============================================================================
// 404 for unknown UUID (minimal AC5 check — full polish is task #3)
// =============================================================================

func TestConversationsJSONL_UnknownUUID_404(t *testing.T) {
	lookup := &stubPathLookup{m: map[string]string{}}
	_, baseURL := newTestServer(t, lookup)

	resp, err := http.Get( //nolint:noctx
		fmt.Sprintf("%s/v1/conversations/does-not-exist/jsonl", baseURL))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// =============================================================================
// Unit: parseSessionUUID
// =============================================================================

func TestParseSessionUUID(t *testing.T) {
	cases := []struct {
		path   string
		want   string
		wantOK bool
	}{
		{"/v1/conversations/abc-123/jsonl", "abc-123", true},
		{"/v1/conversations/9f8e-uuid-1/jsonl", "9f8e-uuid-1", true},
		// Missing /jsonl suffix
		{"/v1/conversations/abc-123", "", false},
		// Missing prefix
		{"/api/conversations/abc-123/jsonl", "", false},
		// Empty uuid
		{"/v1/conversations//jsonl", "", false},
		// Root path
		{"/v1/conversations/", "", false},
		// Decoded path traversal (what r.URL.Path contains when the request
		// carried ..%2F..%2Fetc%2Fpasswd). The uuid is parsed as the opaque
		// key "../../etc/passwd"; the provider lookup will miss it → 404.
		// parseSessionUUID itself is not the guard — the lookup layer is.
		{"/v1/conversations/../../etc/passwd/jsonl", "../../etc/passwd", true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			got, ok := parseSessionUUID(tc.path)
			if ok != tc.wantOK {
				t.Errorf("parseSessionUUID(%q) ok = %v, want %v", tc.path, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("parseSessionUUID(%q) uuid = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// =============================================================================
// Unit: PathForSessionUUID on the Provider
// =============================================================================

// TestPathForSessionUUID is in the claudeprojects package — tested there
// via provider_test.go. We include a cross-package smoke test here that
// the interface is satisfied.
func TestPathLookupInterfaceSatisfied(t *testing.T) {
	// Compile-time check: *stubPathLookup satisfies PathLookup.
	var _ PathLookup = (*stubPathLookup)(nil)
}

// =============================================================================
// Helpers
// =============================================================================

// makeCountedContent produces a byte slice of exactly n bytes where each
// byte value cycles 0–255. This gives us deterministic content we can
// assert exact sub-slices against.
func makeCountedContent(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 256)
	}
	return b
}

