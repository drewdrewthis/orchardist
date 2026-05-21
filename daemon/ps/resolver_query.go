package ps

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

const (
	// passthroughTimeout is the per-call timeout for Query.ps (S16b guard 2).
	passthroughTimeout = 30 * time.Second
	// passthroughConcurrencyCap limits simultaneous Query.ps executions
	// (S16b guard 2). 4 is the constitution default.
	passthroughConcurrencyCap = 4
)

// PassthroughResult is the opaque JSON shape returned by Query.ps.
type PassthroughResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
	TimedOut bool   `json:"timedOut"`
}

// QueryResolver implements Query.ps — the pass-through escape hatch.
//
// S16b guards enforced:
//  1. TOP-LEVEL only — gqlgen enforces this statically (can't nest in a
//     list/object resolver or subscription).
//  2. 30-second timeout per call.
//  3. Concurrency cap: 4 simultaneous pass-throughs.
//  4. NOT cached, NOT subscribable, NOT loader-batched.
type QueryResolver struct {
	// sem is a buffered channel used as a semaphore. A caller acquires a
	// slot by receiving; releases by sending. Capacity = cap (R12 channel
	// direction: public API exposes receive-only where applicable).
	sem chan struct{}
}

// NewQueryResolver constructs a QueryResolver with the concurrency cap
// pre-initialised.
func NewQueryResolver() *QueryResolver {
	sem := make(chan struct{}, passthroughConcurrencyCap)
	for i := 0; i < passthroughConcurrencyCap; i++ {
		sem <- struct{}{}
	}
	return &QueryResolver{sem: sem}
}

// InFlight returns the number of pass-through calls currently executing.
// Exposed for tests (T7).
func (r *QueryResolver) InFlight() int {
	return passthroughConcurrencyCap - len(r.sem)
}

// Ps implements Query.ps(tool: PsTool!, args: [String!]!): JSON.
//
// Guards:
//  1. Acquires a slot from the concurrency semaphore — returns
//     ErrConcurrencyCapExceeded immediately when all slots are taken.
//  2. Wraps the execution in a 30-second context deadline.
//  3. Executes the named tool with the given args.
//  4. Returns stdout/stderr/exitCode as opaque JSON. Never cached.
func (r *QueryResolver) Ps(ctx context.Context, tool PsTool, args []string) (*PassthroughResult, error) {
	// Guard: concurrency cap (S16b, T7).
	select {
	case <-r.sem:
		// acquired a slot
	default:
		return nil, ErrConcurrencyCapExceeded
	}
	defer func() { r.sem <- struct{}{} }()

	// Guard: 30-second deadline (S16b, T7).
	callCtx, cancel := context.WithTimeout(ctx, passthroughTimeout)
	defer cancel()

	toolName := toolToCommand(tool)
	cmd := exec.CommandContext(callCtx, toolName, args...)

	var result PassthroughResult
	out, err := cmd.Output()
	if err != nil {
		if callCtx.Err() == context.DeadlineExceeded {
			result.TimedOut = true
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			result.Stderr = string(exitErr.Stderr)
			result.Stdout = string(out)
		}
	} else {
		result.Stdout = string(out)
	}

	return &result, nil
}

// PsTool is the pass-through tool enum. Mirrors the GraphQL enum PsTool.
type PsTool string

const (
	// PsToolPs invokes the `ps` command.
	PsToolPs PsTool = "ps"
	// PsToolLsof invokes the `lsof` command.
	PsToolLsof PsTool = "lsof"
)

// ErrConcurrencyCapExceeded is returned when the pass-through concurrency
// cap is exhausted (S16b, T7).
var ErrConcurrencyCapExceeded = fmt.Errorf("ps: pass-through concurrency cap exceeded (max %d)", passthroughConcurrencyCap)

// toolToCommand maps the PsTool enum to the executable name.
func toolToCommand(t PsTool) string {
	switch t {
	case PsToolLsof:
		return "lsof"
	default:
		return "ps"
	}
}
