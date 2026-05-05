// Package config hosts the `orchard config {init,add-repo}` cobra
// subcommand group. These edit ~/.config/orchard/config.json directly;
// the running daemon reflects changes via fsnotify (Workstream B).
//
// Workstream A scope: `init` writes a default config and creates the
// state directory.
// Workstream B-config scope: `add-repo PATH` validates and appends a
// project entry; the daemon's fsnotify watcher reflects the change
// without any mutation API.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/drewdrewthis/git-orchard-rs/internal/orchpaths"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
)

// Command returns the `config` subcommand group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage orchard configuration files",
		Long:  "Initialise and edit ~/.config/orchard/config.json. The running daemon reflects changes via fsnotify.",
	}
	cmd.AddCommand(initCmd(), addRepoCmd(), addPeerCmd())
	return cmd
}

func initCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Write default config and create state directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd.OutOrStdout(), force)
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing config.json")
	return c
}

func addRepoCmd() *cobra.Command {
	var (
		name        string
		id          string
		allowNonGit bool
	)
	c := &cobra.Command{
		Use:   "add-repo PATH",
		Short: "Append a project to ~/.config/orchard/config.json",
		Long: "Validate PATH (must exist and, by default, contain a .git directory),\n" +
			"append it to the config file's projects list, and rely on the running\n" +
			"daemon's fsnotify watcher to reflect the change. No daemon mutation\n" +
			"API — config is the source of truth (ADR-011 §5.1).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAddRepo(cmd.OutOrStdout(), args[0], name, id, allowNonGit)
		},
	}
	c.Flags().StringVar(&name, "name", "", "human-readable label (defaults to basename of PATH)")
	c.Flags().StringVar(&id, "id", "", "stable id (defaults to slug of name, then short hash of directory)")
	c.Flags().BoolVar(&allowNonGit, "allow-non-git", false, "skip the .git/ presence check (use for nested or virtual worktrees)")
	return c
}

// runAddRepo loads the config file, appends or updates the project row
// for PATH, and writes the file atomically (tmp + rename) so the daemon
// observes a single fsnotify event.
func runAddRepo(w io.Writer, pathArg, name, id string, allowNonGit bool) error {
	abs, err := filepath.Abs(pathArg)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", pathArg, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("path does not exist: %s", abs)
		}
		return fmt.Errorf("stat %s: %w", abs, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", abs)
	}
	if !allowNonGit {
		if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("not a git repo (no .git/ at %s); pass --allow-non-git to override", abs)
			}
			return fmt.Errorf("stat .git: %w", err)
		}
	}

	cfgPath, err := orchpaths.ConfigFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return fmt.Errorf("ensure config dir: %w", err)
	}

	file, err := loadOrInitFile(cfgPath)
	if err != nil {
		return err
	}

	row := configprovider.ProjectRow{
		ID:        configprovider.ProjectID(id),
		Directory: abs,
		Name:      name,
	}.Normalise()

	// Replace any existing entry for this directory or id.
	replaced := false
	for i, existing := range file.Projects {
		if existing.Directory == row.Directory || (row.ID != "" && existing.ID == row.ID) {
			file.Projects[i] = row
			replaced = true
			break
		}
	}
	if !replaced {
		file.Projects = append(file.Projects, row)
	}

	if err := writeFileAtomic(cfgPath, file); err != nil {
		return err
	}
	if replaced {
		fmt.Fprintf(w, "updated project %s (%s) in %s\n", row.ID, row.Directory, cfgPath)
	} else {
		fmt.Fprintf(w, "added project %s (%s) to %s\n", row.ID, row.Directory, cfgPath)
	}
	return nil
}

// loadOrInitFile reads cfgPath into a configprovider.File. A missing
// file yields an empty File (version 1) so the very first add-repo call
// also serves as an implicit init.
func loadOrInitFile(cfgPath string) (configprovider.File, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return configprovider.File{Version: 1}, nil
		}
		return configprovider.File{}, fmt.Errorf("read %s: %w", cfgPath, err)
	}
	if len(data) == 0 {
		return configprovider.File{Version: 1}, nil
	}
	var f configprovider.File
	if err := json.Unmarshal(data, &f); err != nil {
		return configprovider.File{}, fmt.Errorf("parse %s: %w", cfgPath, err)
	}
	if f.Version == 0 {
		f.Version = 1
	}
	return f, nil
}

// writeFileAtomic marshals f to JSON, writes to a sibling tmp file, and
// renames into place. The rename is what fsnotify emits a single event
// for, avoiding a half-written intermediate state.
func writeFileAtomic(cfgPath string, f configprovider.File) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(cfgPath), ".config.*.json")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, cfgPath); err != nil {
		cleanup()
		return fmt.Errorf("rename %s → %s: %w", tmpName, cfgPath, err)
	}
	return nil
}

func runInit(w io.Writer, force bool) error {
	cfgDir, err := orchpaths.ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", cfgDir, err)
	}
	cfgPath, err := orchpaths.ConfigFile()
	if err != nil {
		return err
	}
	if !force {
		if _, err := os.Stat(cfgPath); err == nil {
			fmt.Fprintf(w, "config already exists: %s (use --force to overwrite)\n", cfgPath)
		} else {
			if err := writeConfig(cfgPath); err != nil {
				return err
			}
			fmt.Fprintf(w, "wrote default config: %s\n", cfgPath)
		}
	} else {
		if err := writeConfig(cfgPath); err != nil {
			return err
		}
		fmt.Fprintf(w, "wrote default config (forced): %s\n", cfgPath)
	}

	stateDir, err := orchpaths.StateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", stateDir, err)
	}
	fmt.Fprintf(w, "ensured state directory: %s\n", stateDir)
	return nil
}

func writeConfig(path string) error {
	cfg := configprovider.File{Version: 1, Projects: []configprovider.ProjectRow{}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
