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

// LegacyConfigFile returns the absolute path to the legacy config location
// (~/.config/orchard/config.json), whether that file currently exists, and
// any error encountered while resolving the home directory.
//
// This function performs a single os.Stat call. It is intended to be called
// at most once per process, exclusively at the config-load failure site,
// to decide whether to emit a migration hint. It must never be called from
// ConfigFile(), ConfigDir(), or any other path helper.
func LegacyConfigFile() (path string, exists bool, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	p := filepath.Join(home, ".config", "orchard", "config.json")
	_, statErr := os.Stat(p)
	if statErr == nil {
		return p, true, nil
	}
	if os.IsNotExist(statErr) {
		return p, false, nil
	}
	// Stat failed for a reason other than "not found" — return the path and
	// treat as absent to avoid blocking startup.
	return p, false, statErr
}

// MigrationHintMessage returns the user-facing hint text to emit when the
// new config path is missing but the legacy path exists.
//
// The message is separated from the logging call so it can be unit-tested
// independently of the logger.
func MigrationHintMessage(legacyPath, newPath string) string {
	return "Found legacy config at " + legacyPath +
		" — the canonical location is now " + newPath +
		". To migrate: mv ~/.config/orchard ~/.orchard"
}
