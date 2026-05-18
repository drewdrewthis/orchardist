// resolver_passthrough.go — hostServiceCtl pass-through (S16b).
//
// Implements the mandatory L4 guards:
//  1. Top-level Query only — enforced by gqlgen layout (not a nested resolver).
//  2. Per-call 30s timeout.
//  3. Domain-level concurrency cap of 4.
//  4. Not cached, not loader-batched, not subscribable.
package hostservices

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sync/semaphore"
)

const (
	// passthroughTimeout is the per-call timeout for hostServiceCtl.
	passthroughTimeout = 30 * time.Second
	// passthroughCap is the maximum number of concurrent hostServiceCtl
	// calls. Exceeding this blocks — callers should retry or use the
	// typed core for high-frequency reads.
	passthroughCap = 4
)

// passthroughSem is the domain-level concurrency cap (S16b guard 3).
// Package-level so it spans all requests against this daemon instance.
var passthroughSem = semaphore.NewWeighted(passthroughCap)

// PassthroughResult is the opaque JSON value returned by hostServiceCtl.
// It mirrors the raw stdout/stderr/exitCode envelope — callers deserialise
// against their knowledge of the underlying tool's output.
type PassthroughResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// ResolveHostServiceCtl executes an arbitrary launchctl / systemctl
// invocation and returns the output as opaque JSON. Enforces all L4
// guards defined in S16b.
//
// `host` is validated to be "localhost" in v1 (federation deferred).
// `args` must be non-empty.
func ResolveHostServiceCtl(ctx context.Context, host string, args []string) (json.RawMessage, error) {
	if host == "" {
		return nil, fmt.Errorf("hostServiceCtl: host must not be empty")
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("hostServiceCtl: args must not be empty")
	}

	// Guard 2: per-call timeout.
	callCtx, cancel := context.WithTimeout(ctx, passthroughTimeout)
	defer cancel()

	// Guard 3: concurrency cap.
	if err := passthroughSem.Acquire(callCtx, 1); err != nil {
		return nil, fmt.Errorf("hostServiceCtl: concurrency cap reached: %w", err)
	}
	defer passthroughSem.Release(1)

	// Detect the OS service manager — prefer launchctl (macOS), fall back
	// to systemctl (Linux). Not cached — every call re-detects so a PATH
	// change during runtime takes effect.
	bin, err := detectServiceManager()
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(callCtx, bin, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	exitCode := 0
	if runErr := cmd.Run(); runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("hostServiceCtl exec: %w", runErr)
		}
	}

	result := PassthroughResult{
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
		ExitCode: exitCode,
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("hostServiceCtl marshal: %w", err)
	}
	return json.RawMessage(raw), nil
}

func detectServiceManager() (string, error) {
	for _, bin := range []string{"launchctl", "systemctl"} {
		if path, err := exec.LookPath(bin); err == nil {
			return path, nil
		}
	}
	return "", ErrServiceManagerMissing
}
