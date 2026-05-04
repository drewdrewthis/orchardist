//go:build linux

package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// linuxIdentityReader reads identity from Linux-native sources:
//   - machineId : /etc/machine-id (systemd-issued).
//   - hostname  : os.Hostname().
//   - os        : runtime.GOOS ("linux").
//   - kernel    : `uname -sr` ("Linux 6.5.0-...").
type linuxIdentityReader struct{}

// NewIdentityReader returns the OS-specific reader for the build target.
// Selected at compile time via build tags; callers receive a single
// platform implementation.
func NewIdentityReader() IdentityReader { return linuxIdentityReader{} }

func (linuxIdentityReader) Read(ctx context.Context) (Identity, error) {
	id := Identity{OS: runtime.GOOS}

	mid, err := readMachineID()
	if err != nil {
		return Identity{}, fmt.Errorf("read /etc/machine-id: %w", err)
	}
	id.MachineID = mid

	hn, err := os.Hostname()
	if err != nil {
		return Identity{}, fmt.Errorf("read hostname: %w", err)
	}
	id.Hostname = hn

	id.Kernel = readUnameSR(ctx)
	return id, nil
}

func readMachineID() (string, error) {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", fmt.Errorf("/etc/machine-id is empty")
	}
	return id, nil
}

func readUnameSR(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "uname", "-sr").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
