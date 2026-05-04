//go:build linux

package hostservice

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// LocalHostID returns the Linux machine id from /etc/machine-id.
//
// Self-contained on purpose: ws-b-hostservice ships before ws-b-host
// has merged into ws-a-scaffold, so we cannot import host.IdentityReader.
// When ws-b-host lands first, this file deletes cleanly and the daemon
// switches to host.Provider.LocalID().
//
// ctx is accepted for API parity with the macOS reader (which shells
// `ioreg`); the Linux read is a single file read with no I/O budget
// to honour.
func LocalHostID(_ context.Context) (string, error) {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return "", fmt.Errorf("read /etc/machine-id: %w", err)
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", fmt.Errorf("/etc/machine-id is empty")
	}
	return id, nil
}
