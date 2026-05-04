// Package config hosts the `orchard config {init,add-repo}` cobra
// subcommand group. These edit ~/.config/orchard/config.json directly;
// the running daemon reflects changes via fsnotify (Workstream B).
//
// Workstream A scope: `init` writes a default config and creates the
// state directory. `add-repo` is a stub that advertises the future
// command shape — it errors cleanly until Workstream B ships the
// project provider.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/drewdrewthis/git-orchard-rs/internal/orchpaths"
)

// DefaultConfig is the JSON skeleton written by `config init`. It carries
// just enough shape to be edited by hand or by `config add-repo`. The
// schema will firm up as Workstream B's project provider lands.
type DefaultConfig struct {
	Version  int      `json:"version"`
	Projects []string `json:"projects"`
}

// Command returns the `config` subcommand group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage orchard configuration files",
		Long:  "Initialise and edit ~/.config/orchard/config.json. The running daemon reflects changes via fsnotify.",
	}
	cmd.AddCommand(initCmd(), addRepoCmd())
	return cmd
}

func initCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Write default config and create state directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd.OutOrStdout(), force)
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing config.json")
	return c
}

func addRepoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add-repo PATH",
		Short: "Append a project path to the config (Workstream B)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented in Workstream A; lands with the project provider in Workstream B")
		},
	}
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
	cfg := DefaultConfig{Version: 1, Projects: []string{}}
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
