package host

import "context"

// Identity is the OS-issued identity of a machine. It does not change
// across the daemon's lifetime (the machine id survives reboots; hostname
// can in theory change but we treat it as boot-stable for v1).
type Identity struct {
	MachineID string
	Hostname  string
	OS        string
	Kernel    string
}

// IdentityReader reads the local machine's identity. Implementations
// are OS-specific (identity_darwin.go, identity_linux.go) and selected
// at compile time via build tags.
//
// Read once at startup; the Provider caches the result for the
// lifetime of the daemon.
type IdentityReader interface {
	Read(ctx context.Context) (Identity, error)
}
