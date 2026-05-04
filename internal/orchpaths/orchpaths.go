// Package orchpaths centralises orchard's filesystem layout.
//
// Two roots, both XDG-style:
//   - Config: $XDG_CONFIG_HOME/orchard (default ~/.config/orchard)
//   - State:  $XDG_STATE_HOME/orchard  (default ~/.local/state/orchard)
//
// Anything that touches paths (config init, daemon pidfile, query CLI
// looking up the daemon address) goes through these helpers so behaviour
// stays consistent and testable via env-var override.
package orchpaths

import (
	"os"
	"path/filepath"
)

// ConfigDir returns the orchard config directory, respecting
// XDG_CONFIG_HOME. It does not create the directory.
func ConfigDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "orchard"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "orchard"), nil
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

// ConfigFile returns the absolute path to config.json (does not create).
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
