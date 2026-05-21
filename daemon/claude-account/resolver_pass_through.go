package claudeaccount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// PassThroughGuards enforces S16b L4 guards for Query.claudeCli:
//   1. Top-level only (callers must not nest inside list/object resolvers).
//   2. Per-call timeout: 30s.
//   3. Concurrency cap: 4 simultaneous invocations.
//
// Not cached, not loader-batched, not subscribable per S16b.
//
// T7: passthrough_test.go asserts timeout and concurrency cap.
type PassThroughGuards struct {
	adapter  *ShellAdapter
	timeout  time.Duration
	cap      int64
	inFlight atomic.Int64
}

// NewPassThroughGuards constructs the guard layer.
// timeout defaults to 30s; cap defaults to 4 per S16b.
func NewPassThroughGuards(adapter *ShellAdapter) *PassThroughGuards {
	return &PassThroughGuards{
		adapter: adapter,
		timeout: 30 * time.Second,
		cap:     4,
	}
}

// NewPassThroughGuardsForTest constructs guards with custom timeout and cap.
// Only for use in tests — production code calls NewPassThroughGuards.
func NewPassThroughGuardsForTest(adapter *ShellAdapter, timeout time.Duration, concurrencyCap int64) *PassThroughGuards {
	return &PassThroughGuards{
		adapter: adapter,
		timeout: timeout,
		cap:     concurrencyCap,
	}
}

// PassThroughResult is the shape returned by Query.claudeCli.
// Matches the JSON envelope callers expect: stdout, exit code, and any error string.
type PassThroughResult struct {
	Stdout   string `json:"stdout"`
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error,omitempty"`
}

// Invoke runs the named tool with args, enforcing S16b guards.
// Returns a JSON-encodable result or an error.
//
// tool must be one of the ClaudeCliTool enum values: "claude" or "ccusage".
// Callers are responsible for ensuring this is a top-level resolver only (guard 1);
// static gqlgen field placement prevents nesting.
func (g *PassThroughGuards) Invoke(ctx context.Context, tool string, args []string) (interface{}, error) {
	// Guard 2: validate tool enum (belt-and-suspenders on top of gqlgen enum).
	if tool != "claude" && tool != "ccusage" {
		return nil, fmt.Errorf("claudeCli: unknown tool %q; allowed: claude, ccusage", tool)
	}

	// Guard 3: concurrency cap.
	if g.inFlight.Add(1) > g.cap {
		g.inFlight.Add(-1)
		return nil, fmt.Errorf("claudeCli: concurrency cap %d reached; retry later", g.cap)
	}
	defer g.inFlight.Add(-1)

	// Guard 2: per-call timeout.
	tCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	out, err := g.adapter.RunPassThrough(tCtx, tool, args)

	result := PassThroughResult{
		Stdout: string(out),
	}
	if err != nil {
		var ee interface{ ExitCode() int }
		if errors.As(err, &ee) {
			result.ExitCode = ee.ExitCode()
		} else if errors.Is(err, context.DeadlineExceeded) {
			result.ExitCode = -1
		} else {
			result.ExitCode = -1
		}
		result.Error = err.Error()
	}

	// Return as map[string]interface{} so gqlgen can serialize it as JSON
	// scalar (the schema field type is JSON).
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return nil, fmt.Errorf("claudeCli: marshal result: %w", marshalErr)
	}
	var out2 interface{}
	if err := json.Unmarshal(raw, &out2); err != nil {
		return nil, fmt.Errorf("claudeCli: unmarshal result: %w", err)
	}
	return out2, nil
}
