// mutations.go — serviceStart, serviceStop, serviceRestart mutations.
//
// Per L5 every mutation execs a script in scripts/ and projects its
// --json output as the response. Daemon-side: input validation +
// script-exec wrapping only. No mutation logic re-implemented in Go.
package hostservices

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

// scriptEnvelope mirrors the L2 --json output shape.
// Scripts emit: {"ok": bool, "data"?: any, "error"?: {"code": str, "message": str}}.
type scriptEnvelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *scriptError    `json:"error,omitempty"`
}

type scriptError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ServiceLifecycleInput is the input for start/stop/restart mutations.
// Per M3 (granular mutations) and S4 (single input object).
type ServiceLifecycleInput struct {
	// Host is the machineID of the host owning the service. v1: must be
	// the local machine; federation expands this later.
	Host string
	// Name is the service name as written in config.
	Name string
}

// MutationResult is the return type for lifecycle mutations. Returns the
// affected HostService node per L8 / S8 so clients update their cache
// without a follow-up query.
type MutationResult struct {
	// Resolver holds the updated snapshot projected as a HostServiceResolver.
	Resolver *HostServiceResolver
}

// ExecuteServiceStart runs scripts/host-service-start.sh --json and
// returns the updated HostService. Per L5 the script writes to external
// truth; the daemon's next poll picks up the change.
func ExecuteServiceStart(ctx context.Context, svc ServiceReader, input ServiceLifecycleInput) (*HostServiceResolver, error) {
	return execLifecycleScript(ctx, svc, "host-service-start.sh", input)
}

// ExecuteServiceStop runs scripts/host-service-stop.sh --json.
func ExecuteServiceStop(ctx context.Context, svc ServiceReader, input ServiceLifecycleInput) (*HostServiceResolver, error) {
	return execLifecycleScript(ctx, svc, "host-service-stop.sh", input)
}

// ExecuteServiceRestart runs scripts/host-service-restart.sh --json.
func ExecuteServiceRestart(ctx context.Context, svc ServiceReader, input ServiceLifecycleInput) (*HostServiceResolver, error) {
	return execLifecycleScript(ctx, svc, "host-service-restart.sh", input)
}

// execLifecycleScript validates input, executes the named script with
// --json, parses the L2 envelope, and returns the affected node.
func execLifecycleScript(ctx context.Context, svc ServiceReader, scriptName string, input ServiceLifecycleInput) (*HostServiceResolver, error) {
	// M4: validate input before exec.
	if input.Name == "" {
		return nil, fmt.Errorf("serviceLifecycle: name must not be empty")
	}
	if input.Host == "" {
		return nil, fmt.Errorf("serviceLifecycle: host must not be empty")
	}

	scriptPath, err := resolveScriptPath(scriptName)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, scriptPath, "--json", "--host", input.Host, "--name", input.Name)
	out, err := cmd.Output()
	if err != nil {
		// Script non-zero exit — the envelope may still carry error detail.
		if exitErr, ok := err.(*exec.ExitError); ok {
			_ = exitErr // stderr is in exitErr.Stderr if needed
		}
		return nil, fmt.Errorf("%s: exec: %w", scriptName, err)
	}

	var env scriptEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("%s: parse envelope: %w", scriptName, err)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s: %s", scriptName, env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("%s: script reported ok=false with no error detail", scriptName)
	}

	// Fetch the updated snapshot from the service (the poll will have
	// run between script exec and here, or we accept a brief stale read).
	id := MakeID(input.Host, input.Name)
	snap, err := svc.ByID(ctx, id)
	if err != nil {
		// Best-effort: return a synthetic snapshot from the script output.
		snap = HostServiceSnapshot{
			MachineID: input.Host,
			Name:      input.Name,
			State:     StateUnknown,
			FetchedAt: time.Now(),
		}
	}
	return &HostServiceResolver{Snap: snap}, nil
}

// resolveScriptPath locates the script relative to the project root.
// At runtime the daemon runs from the repo root; the script path is
// scripts/<name>.
func resolveScriptPath(name string) (string, error) {
	// Locate via PATH first (daemon running from install).
	// Fall back to relative path from binary location.
	candidates := []string{
		filepath.Join("scripts", name),
	}
	// On macOS/Linux the scripts dir sits next to the binary when installed.
	if exe, err := resolveExeDir(); err == nil {
		candidates = append(candidates, filepath.Join(exe, "scripts", name))
	}
	for _, p := range candidates {
		if _, err := exec.LookPath(p); err == nil {
			return p, nil
		}
		// Try absolute path directly.
		if abs, err := filepath.Abs(p); err == nil {
			if _, err2 := exec.LookPath(abs); err2 == nil {
				return abs, nil
			}
		}
	}
	return "", fmt.Errorf("hostservices: script %q not found in candidates %v", name, candidates)
}

func resolveExeDir() (string, error) {
	exe, err := exec.LookPath("orchard")
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}
