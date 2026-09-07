// Tests for reset-aware GraphQL rate-limit cooldown (issue #768, AC6-AC11).
//
// Before #768: EnrichPullRequest funnels every c.GraphQL error — including a
// header 403 that already carries a typed *ErrRateLimitedT{ResetAt} — into
// serveStale without ever checking IsRateLimited, so a header-403 serves
// stale WITHOUT arming the cooldown at all (graphql_enrich.go:104-106).
// BatchEnrichPullRequests does call IsRateLimited and arms the cooldown, but
// always for the fixed 5-minute rateLimitCooldown constant
// (graphql_enrich_batch.go:131-134), ignoring ResetAt. logging.go:186-197's
// enterRateLimitCooldown has no reset-epoch parameter at all. These tests
// pin the fixed contract: both enrich paths arm the cooldown until
// time.Unix(ResetAt,0) when ResetAt is present and in the future, and fall
// back to the existing 5-minute constant when it is absent or already past.
//
// Reuses the newEnrichProvider/fakeClock/enrichResponse scaffolding from
// graphql_enrich_test.go (same gh_test package) and the provider's
// injectable clock func() time.Time (provider.go's NewWith).
package gh_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/providers/gh"
)

// cooldownLogMessage mirrors the unexported constant of the same name in
// logging.go — kept as a literal here (not an export shim) since the log
// message text is itself the observable contract operators grep for.
const cooldownLogMessage = "gh: rate-limit cooldown engaged"

// cannedResponse is one scripted /graphql response.
type cannedResponse struct {
	status  int
	headers map[string]string
	body    []byte
}

// rateLimitedResponse builds a 403 + X-RateLimit-Remaining:0 response. When
// resetEpoch is 0, no X-RateLimit-Reset header is sent at all (the
// "unparseable/absent" case — AC8).
func rateLimitedResponse(resetEpoch int64) cannedResponse {
	headers := map[string]string{"X-RateLimit-Remaining": "0"}
	if resetEpoch != 0 {
		headers["X-RateLimit-Reset"] = strconv.FormatInt(resetEpoch, 10)
	}
	return cannedResponse{status: http.StatusForbidden, headers: headers, body: []byte(`{}`)}
}

// graphqlBodyRateLimitResponse builds a 200 response whose errors[] carries a
// "rate limit" message and no data — the AC11 regression path, which has no
// rate-limit headers at all.
func graphqlBodyRateLimitResponse() cannedResponse {
	body, _ := json.Marshal(map[string]any{
		"data":   nil,
		"errors": []any{map[string]any{"message": "API rate limit exceeded"}},
	})
	return cannedResponse{status: http.StatusOK, body: body}
}

// sequencedGraphQLServer serves responses[n] on the (n+1)th request to
// /graphql; once responses is exhausted, the last entry repeats. hitCount
// tracks every request actually reaching the handler, which is how tests
// assert a cooldown suppressed (or a clock advance resumed) real network
// calls.
func sequencedGraphQLServer(t *testing.T, hitCount *atomic.Int32, responses ...cannedResponse) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		n := hitCount.Add(1)
		idx := int(n) - 1
		if idx >= len(responses) {
			idx = len(responses) - 1
		}
		resp := responses[idx]
		for k, v := range resp.headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = w.Write(resp.body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
		http.NotFound(w, r)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newCooldownProvider wires a gh.Provider against srv with a logger writing
// JSON records to a buffer, so tests can assert on the emitted
// cooldownLogMessage and its structured attributes directly instead of
// substring-matching a text-handler line.
func newCooldownProvider(t *testing.T, srv *httptest.Server, clock func() time.Time) (*gh.Provider, *bytes.Buffer) {
	t.Helper()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	auth := &gh.StaticAuthSource{TokenValue: "test-token-fixture"}
	p := gh.NewWith(logger, srv.URL, auth, clock)
	if err := p.Start(context.Background()); err != nil {
		t.Logf("provider start (non-fatal): %v", err)
	}
	gh.SetHTTPClientForTest(p, srv.Client())
	return p, &logBuf
}

// cooldownLogRecord is the subset of a JSON slog line this file cares about.
type cooldownLogRecord struct {
	Msg   string `json:"msg"`
	Until string `json:"until"`
	Site  string `json:"site"`
}

// lastCooldownLog scans buf for JSON-encoded slog lines and returns the last
// one whose msg is cooldownLogMessage. ok is false when none was emitted.
func lastCooldownLog(t *testing.T, buf *bytes.Buffer) (rec cooldownLogRecord, ok bool) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for {
		var r cooldownLogRecord
		if err := dec.Decode(&r); err != nil {
			break
		}
		if r.Msg == cooldownLogMessage {
			rec, ok = r, true
		}
	}
	return rec, ok
}

// countCooldownLogs reports how many cooldownLogMessage lines were emitted —
// used to assert "exactly once" where the AC calls for it.
func countCooldownLogs(t *testing.T, buf *bytes.Buffer) int {
	t.Helper()
	n := 0
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for {
		var r cooldownLogRecord
		if err := dec.Decode(&r); err != nil {
			break
		}
		if r.Msg == cooldownLogMessage {
			n++
		}
	}
	return n
}

// TestEnrichPullRequest_HeaderRateLimit_ArmsCooldownAndSuppressesNextCall is
// AC6: a header 403 must arm the cooldown (it does not today — the error is
// funneled straight into serveStale without an IsRateLimited check) and a
// second call before the reset epoch must not reach the server again.
func TestEnrichPullRequest_HeaderRateLimit_ArmsCooldownAndSuppressesNextCall(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	resetEpoch := now.Add(42 * time.Minute).Unix()

	var hits atomic.Int32
	srv := sequencedGraphQLServer(t, &hits, rateLimitedResponse(resetEpoch))
	p, logBuf := newCooldownProvider(t, srv, clock)

	key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 1}

	if _, err := p.EnrichPullRequest(context.Background(), key); err == nil {
		t.Fatal("EnrichPullRequest on a 403 with nothing cached returned nil error, want the rate-limit error")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hit count after first call = %d, want 1", got)
	}

	if _, ok := lastCooldownLog(t, logBuf); !ok {
		t.Errorf("no %q Warn line emitted after a header-403; got log: %s", cooldownLogMessage, logBuf.String())
	}

	// Second call, still before the reset epoch: must not reach the server.
	_, _ = p.EnrichPullRequest(context.Background(), key)
	if got := hits.Load(); got != 1 {
		t.Errorf("hit count after second call (within cooldown) = %d, want still 1 (cooldown should suppress the network call)", got)
	}
}

// TestEnrichAndBatchEnrich_CooldownArmsUntilResetEpoch is AC7: both enrich
// paths must arm the cooldown until time.Unix(ResetAt,0), not clock()+5min,
// when the response carries a future ResetAt.
func TestEnrichAndBatchEnrich_CooldownArmsUntilResetEpoch(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	resetEpoch := now.Add(42 * time.Minute).Unix()
	wantUntil := time.Unix(resetEpoch, 0).Format(time.RFC3339)

	t.Run("EnrichPullRequest", func(t *testing.T) {
		t.Parallel()
		var hits atomic.Int32
		srv := sequencedGraphQLServer(t, &hits, rateLimitedResponse(resetEpoch))
		p, logBuf := newCooldownProvider(t, srv, clock)

		key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 2}
		_, _ = p.EnrichPullRequest(context.Background(), key)

		rec, ok := lastCooldownLog(t, logBuf)
		if !ok {
			t.Fatalf("no %q Warn line emitted; log: %s", cooldownLogMessage, logBuf.String())
		}
		if rec.Until != wantUntil {
			t.Errorf("until = %q, want %q (now+42min, not now+5min)", rec.Until, wantUntil)
		}
	})

	t.Run("BatchEnrichPullRequests", func(t *testing.T) {
		t.Parallel()
		var hits atomic.Int32
		srv := sequencedGraphQLServer(t, &hits, rateLimitedResponse(resetEpoch))
		p, logBuf := newCooldownProvider(t, srv, clock)

		key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 3}
		_, _ = p.BatchEnrichPullRequests(context.Background(), []gh.PullRequestKey{key})

		rec, ok := lastCooldownLog(t, logBuf)
		if !ok {
			t.Fatalf("no %q Warn line emitted; log: %s", cooldownLogMessage, logBuf.String())
		}
		if rec.Until != wantUntil {
			t.Errorf("until = %q, want %q (now+42min, not now+5min)", rec.Until, wantUntil)
		}
	})
}

// TestEnrichPullRequest_NoResetHeader_FallsBackToFiveMinutes is AC8: an
// absent X-RateLimit-Reset header must fall back to the existing 5-minute
// constant.
func TestEnrichPullRequest_NoResetHeader_FallsBackToFiveMinutes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	wantUntil := now.Add(5 * time.Minute).Format(time.RFC3339)

	var hits atomic.Int32
	srv := sequencedGraphQLServer(t, &hits, rateLimitedResponse(0))
	p, logBuf := newCooldownProvider(t, srv, clock)

	key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 4}
	_, _ = p.EnrichPullRequest(context.Background(), key)

	rec, ok := lastCooldownLog(t, logBuf)
	if !ok {
		t.Fatalf("no %q Warn line emitted; log: %s", cooldownLogMessage, logBuf.String())
	}
	if rec.Until != wantUntil {
		t.Errorf("until = %q, want %q (5-minute fallback)", rec.Until, wantUntil)
	}
}

// TestEnrichPullRequest_PastResetEpoch_FloorsToFiveMinutes is AC9: a
// ResetAt that is already in the past (skewed/stale header) must floor to
// the 5-minute constant instead of arming a zero/negative window, and the
// cooldown it arms must still suppress the next call.
func TestEnrichPullRequest_PastResetEpoch_FloorsToFiveMinutes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	pastEpoch := now.Add(-10 * time.Minute).Unix()
	wantUntil := now.Add(5 * time.Minute).Format(time.RFC3339)

	var hits atomic.Int32
	srv := sequencedGraphQLServer(t, &hits, rateLimitedResponse(pastEpoch))
	p, logBuf := newCooldownProvider(t, srv, clock)

	key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 5}
	_, _ = p.EnrichPullRequest(context.Background(), key)

	rec, ok := lastCooldownLog(t, logBuf)
	if !ok {
		t.Fatalf("no %q Warn line emitted; log: %s", cooldownLogMessage, logBuf.String())
	}
	if rec.Until != wantUntil {
		t.Errorf("until = %q, want %q (floored to 5-minute constant, not a zero/negative window)", rec.Until, wantUntil)
	}

	if got := hits.Load(); got != 1 {
		t.Fatalf("hit count after first call = %d, want 1", got)
	}
	_, _ = p.EnrichPullRequest(context.Background(), key)
	if got := hits.Load(); got != 1 {
		t.Errorf("hit count after second call within the floored 5-minute window = %d, want still 1", got)
	}
}

// TestEnrichPullRequest_CooldownExpiresAtResetEpoch is AC10: once the clock
// passes the armed reset epoch, the next call must issue a real GraphQL
// request again.
func TestEnrichPullRequest_CooldownExpiresAtResetEpoch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	current := now
	clock := func() time.Time { return current }
	resetEpoch := now.Add(42 * time.Minute).Unix()

	successBody := enrichResponse("MERGEABLE", "CLEAN", nil, "SUCCESS", "bug")

	var hits atomic.Int32
	srv := sequencedGraphQLServer(t, &hits,
		rateLimitedResponse(resetEpoch),
		cannedResponse{status: http.StatusOK, body: successBody},
	)
	p, _ := newCooldownProvider(t, srv, clock)

	key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 6}

	// First call: arms the cooldown.
	_, _ = p.EnrichPullRequest(context.Background(), key)
	if got := hits.Load(); got != 1 {
		t.Fatalf("hit count after arming call = %d, want 1", got)
	}

	// Advance the clock past the reset epoch.
	current = time.Unix(resetEpoch, 0).Add(1 * time.Minute)

	pr, err := p.EnrichPullRequest(context.Background(), key)
	if err != nil {
		t.Fatalf("EnrichPullRequest after cooldown expiry: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("hit count after clock passed the reset epoch = %d, want 2 (a fresh GraphQL request)", got)
	}
	if pr.Mergeable != gh.MergeableStateMergeable {
		t.Errorf("Mergeable = %q after the fresh fetch, want MERGEABLE", pr.Mergeable)
	}
}

// TestEnrichPullRequest_GraphQLBodyRateLimit_UsesFiveMinuteFallback is
// AC11: the pre-existing 200-with-errors[]-body "rate limit" string path
// (noteGraphQLErrors) has no reset header available and must keep using the
// 5-minute fallback — a regression guard against #768's header-403 fix
// disturbing this already-working path.
func TestEnrichPullRequest_GraphQLBodyRateLimit_UsesFiveMinuteFallback(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	wantUntil := now.Add(5 * time.Minute).Format(time.RFC3339)

	var hits atomic.Int32
	srv := sequencedGraphQLServer(t, &hits, graphqlBodyRateLimitResponse())
	p, logBuf := newCooldownProvider(t, srv, clock)

	key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 7}
	_, err := p.EnrichPullRequest(context.Background(), key)
	if err == nil {
		t.Fatal("EnrichPullRequest on a GraphQL-body rate-limit error with nothing cached returned nil error")
	}

	if n := countCooldownLogs(t, logBuf); n != 1 {
		t.Fatalf("%q Warn line emitted %d times, want exactly 1; log: %s", cooldownLogMessage, n, logBuf.String())
	}
	rec, _ := lastCooldownLog(t, logBuf)
	if rec.Until != wantUntil {
		t.Errorf("until = %q, want %q (5-minute fallback, unchanged by #768)", rec.Until, wantUntil)
	}
}

// TestParseRateLimitReset is the unit test for the shared header-parse
// helper extracted (per the issue's sequencing step 1) out of the duplicated
// logic in client.go:127-135 and graphql.go:102-110.
func TestParseRateLimitReset(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hdr  http.Header
		want int64
	}{
		{
			name: "valid numeric header",
			hdr:  http.Header{"X-Ratelimit-Reset": []string{"1750000000"}},
			want: 1750000000,
		},
		{
			name: "absent header returns zero",
			hdr:  http.Header{},
			want: 0,
		},
		{
			name: "non-numeric header returns zero",
			hdr:  http.Header{"X-Ratelimit-Reset": []string{"not-a-number"}},
			want: 0,
		},
		{
			name: "empty header value returns zero",
			hdr:  http.Header{"X-Ratelimit-Reset": []string{""}},
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := gh.ExportParseRateLimitReset(tc.hdr)
			if got != tc.want {
				t.Errorf("parseRateLimitReset(%v) = %d, want %d", tc.hdr, got, tc.want)
			}
		})
	}
}
