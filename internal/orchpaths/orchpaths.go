// Package orchpaths centralises orchard's filesystem layout.
//
// Two roots:
//   - Config: ~/.orchard  (dotdir convention; XDG_CONFIG_HOME is ignored)
//   - State:  ~/.local/state/orchard  (XDG_STATE_HOME honoured for state only)
//
// Anything that touches paths (config init, daemon pidfile, query CLI
// looking up the daemon address) goes through these helpers so behaviour
// stays consistent and testable via env-var override.
package orchpaths

import (
	"os"
	"path/filepath"
)

// ConfigDir returns the orchard config directory (~/.orchard).
// XDG_CONFIG_HOME is intentionally ignored — orchard follows the dotdir
// convention (~/.aws, ~/.kube, ~/.ssh, ~/.cargo, ~/.claude). It does not
// create the directory.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".orchard"), nil
}

// StateDir returns the orchard state directory, respecting
// XDG_STATE_HOME. It does not create the directory.
func StateDir() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "orchard"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "orchard"), nil
}

// ConfigFile returns the absolute path to ~/.orchard/config.json (does not create).
func ConfigFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// PidFile returns the absolute path to the running daemon's pidfile.
// Written by `daemon start`, read by `daemon stop` and `daemon status`.
func PidFile() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "orchard.pid"), nil
}
