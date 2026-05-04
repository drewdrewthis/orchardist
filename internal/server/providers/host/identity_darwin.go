//go:build darwin

package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// macIdentityReader reads identity from macOS-native sources:
//   - machineId : `ioreg -rd1 -c IOPlatformExpertDevice` → IOPlatformUUID line.
//   - hostname  : os.Hostname() (libc gethostname under the hood).
//   - os        : runtime.GOOS ("darwin").
//   - kernel    : `uname -sr` ("Darwin 25.4.0"); blank if uname fails.
type macIdentityReader struct{}

// NewIdentityReader returns the OS-specific reader for the build target.
// Selected at compile time via build tags; callers receive a single
// platform implementation.
func NewIdentityReader() IdentityReader { return macIdentityReader{} }

func (macIdentityReader) Read(ctx context.Context) (Identity, error) {
	id := Identity{OS: runtime.GOOS}

	uuid, err := readIOPlatformUUID(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("read IOPlatformUUID: %w", err)
	}
	id.MachineID = uuid

	hn, err := os.Hostname()
	if err != nil {
		return Identity{}, fmt.Errorf("read hostname: %w", err)
	}
	id.Hostname = hn

	// Kernel is best-effort — `uname` should always be present on macOS,
	// but a missing or unexpected output should not collapse identity.
	id.Kernel = readUnameSR(ctx)
	return id, nil
}

// readIOPlatformUUID shells `ioreg -rd1 -c IOPlatformExpertDevice` and
// parses the IOPlatformUUID line. Sample line:
//
//	"IOPlatformUUID" = "ABCD1234-..."
func readIOPlatformUUID(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", fmt.Errorf("ioreg: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		// Format: "IOPlatformUUID" = "<uuid>"
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"`)
		if val == "" {
			continue
		}
		return val, nil
	}
	return "", fmt.Errorf("IOPlatformUUID not found in ioreg output")
}

// readUnameSR returns "Darwin 25.4.0" or "" if uname fails.
func readUnameSR(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "uname", "-sr").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
