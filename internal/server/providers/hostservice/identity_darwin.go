//go:build darwin

package hostservice

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// LocalHostID returns the macOS IOPlatformUUID — the OS-issued machine
// id used by the rest of the orchard daemon as the host_id identity.
//
// Self-contained on purpose: ws-b-hostservice ships before ws-b-host
// has merged into ws-a-scaffold, so we cannot import host.IdentityReader.
// When ws-b-host lands first, this file deletes cleanly and the daemon
// switches to host.Provider.LocalID().
func LocalHostID(ctx context.Context) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", fmt.Errorf("ioreg: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
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
