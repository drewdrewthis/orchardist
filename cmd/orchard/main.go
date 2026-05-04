// Command orchard is the single binary for the orchard daemon and CLI.
//
// Per ADR-011, the same binary runs as a daemon (`orchard daemon start`)
// and as a client (`orchard query ...`, `orchard config ...`). main.go
// is intentionally thin — it wires cobra and dispatches to the three
// subcommand groups under internal/cli/.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/drewdrewthis/git-orchard-rs/internal/cli/config"
	"github.com/drewdrewthis/git-orchard-rs/internal/cli/daemon"
	"github.com/drewdrewthis/git-orchard-rs/internal/cli/query"
)

// version is overridden via -ldflags at release time.
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "orchard",
		Short:         "orchard — query and cache layer over local developer state",
		Long:          "orchard is the read-only join layer over git, tmux, claude, processes, and federated peers. See ADR-011 for the design.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(daemon.Command())
	root.AddCommand(config.Command())
	root.AddCommand(query.Command())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "orchard:", err)
		os.Exit(1)
	}
}
