// Package daemon hosts the `orchard daemon {start,stop,status}` cobra
// subcommand group. It owns the daemon lifecycle from the user's POV;
// the actual GraphQL server lives in internal/server.
//
// Lifecycle model (v1, intentionally simple):
//   - `start` writes a pidfile, runs the server in the foreground, traps
//     SIGINT/SIGTERM for graceful shutdown, removes the pidfile on exit.
//   - `stop` reads the pidfile and sends SIGTERM to that pid.
//   - `status` reads the pidfile, probes /health if the pid is alive,
//     and prints a one-line summary.
//
// No double-fork, no PID 1 daemonisation, no syslog. launchd / systemd
// (scripts/init/) own backgrounding when the user wants it.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/drewdrewthis/git-orchard-rs/internal/orchpaths"
	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeaccount"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/contracts"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/hostservice"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
)

// Command returns the `daemon` subcommand group rooted with three leaves.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the orchard GraphQL daemon",
		Long:  "Start, stop, or query the local orchard daemon listening on " + server.DefaultAddr + ".",
	}
	cmd.AddCommand(startCmd(), stopCmd(), statusCmd())
	return cmd
}

func startCmd() *cobra.Command {
	var addr string
	c := &cobra.Command{
		Use:   "start",
		Short: "Run the orchard daemon in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd.Context(), addr)
		},
	}
	c.Flags().StringVar(&addr, "addr", server.DefaultAddr, "host:port to bind")
	return c
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Send SIGTERM to the running orchard daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop()
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report whether the orchard daemon is running",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.OutOrStdout())
		},
	}
}

// runStart is the foreground daemon entry point. It honours SIGINT and
// SIGTERM by cancelling the server context for a clean shutdown.
func runStart(parentCtx context.Context, addr string) error {
	pidPath, err := orchpaths.PidFile()
	if err != nil {
		return fmt.Errorf("resolve pidfile: %w", err)
	}
	if err := ensureParentDir(pidPath); err != nil {
		return err
	}
	if pid, err := readPid(pidPath); err == nil && processAlive(pid) {
		return fmt.Errorf("orchard daemon already running (pid %d, pidfile %s)", pid, pidPath)
	}
	if err := writePid(pidPath, os.Getpid()); err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}
	defer os.Remove(pidPath)

	ctx, cancel := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := slog.Default()

	cfgPath, err := orchpaths.ConfigFile()
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	configProvider := configprovider.NewProvider(
		configprovider.NewJSONFileAdapter(cfgPath, logger),
		logger,
	)
	if err := configProvider.Start(ctx); err != nil {
		return fmt.Errorf("start config provider: %w", err)
	}
	defer func() { _ = configProvider.Stop() }()

	psProvider := ps.New(ps.NewAdapter("local"), logger)
	tmuxProvider := tmux.New(tmux.NewAdapter(localHostID()), logger)
	claudeProjectsRoot := claudeprojectsRoot()
	claudeProjectsProvider := claudeprojects.New(claudeProjectsRoot, "local", logger)

	hsvc, hsvcErr := buildHostServiceProvider(ctx)
	if hsvcErr != nil {
		fmt.Fprintf(os.Stderr, "orchard: hostservice unavailable: %v\n", hsvcErr)
	}

	fedCfg, err := peerproxy.LoadFederationConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load federation config: %w", err)
	}
	peerProvider := peerproxy.NewProvider(fedCfg, logger)
	localEvents := peerproxy.NewLocalInvalidator()

	opts := []server.Option{
		server.WithProjects(configProvider),
		server.WithPS(psProvider),
		server.WithTmux(tmuxProvider),
		server.WithClaudeProjects(claudeProjectsProvider),
		server.WithClaudeAccount(claudeaccount.New("local", logger)),
		server.WithContracts(contracts.New(logger)),
		server.WithPeerProxy(peerProvider),
		server.WithPeerSecret(fedCfg.PeerSecret),
		server.WithLocalEvents(localEvents),
	}
	if hsvc != nil {
		opts = append(opts, server.WithHostService(hsvc))
	}

	srv := server.New(addr, logger, opts...)
	return srv.Run(ctx)
}

// localHostID returns the host id for tmux nodes. v1 stays neutral.
func localHostID() tmux.HostID {
	return tmux.HostID("local")
}

// buildHostServiceProvider resolves the local machine id, reads the
// services watchlist from config, and constructs the hostservice provider.
func buildHostServiceProvider(ctx context.Context) (*hostservice.Provider, error) {
	hostID, err := hostservice.LocalHostID(ctx)
	if err != nil {
		return nil, fmt.Errorf("read machine identity: %w", err)
	}
	cfgPath, err := orchpaths.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	services, err := hostservice.LoadServicesFromConfig(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load services watchlist: %w", err)
	}
	return hostservice.New(hostID, services), nil
}

// runStop reads the pidfile and signals the running daemon.
func runStop() error {
	pidPath, err := orchpaths.PidFile()
	if err != nil {
		return err
	}
	pid, err := readPid(pidPath)
	if err != nil {
		return fmt.Errorf("daemon not running (no pidfile at %s)", pidPath)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find pid %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Process gone — clear the stale pidfile and report success.
		if errors.Is(err, os.ErrProcessDone) || strings.Contains(err.Error(), "process already finished") {
			_ = os.Remove(pidPath)
			return fmt.Errorf("daemon already stopped (cleared stale pidfile)")
		}
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	// Wait briefly for the process to exit and remove its own pidfile.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon (pid %d) did not exit within 5s", pid)
}

// runStatus prints a one-line summary suitable for human eyes and shell
// scripting. Exit code 0 when up, non-zero when down (cobra does this
// automatically when RunE returns an error).
func runStatus(w io.Writer) error {
	pidPath, err := orchpaths.PidFile()
	if err != nil {
		return err
	}
	pid, err := readPid(pidPath)
	if err != nil {
		return fmt.Errorf("orchard daemon: down (no pidfile at %s)", pidPath)
	}
	if !processAlive(pid) {
		return fmt.Errorf("orchard daemon: down (stale pidfile, pid %d not alive)", pid)
	}
	uptime, healthErr := probeHealth()
	if healthErr != nil {
		fmt.Fprintf(w, "orchard daemon: up (pid %d) — health probe failed: %v\n", pid, healthErr)
		return nil
	}
	fmt.Fprintf(w, "orchard daemon: up (pid %d, uptime %ds)\n", pid, uptime)
	return nil
}

// probeHealth fetches /health and returns the reported uptime in seconds.
func probeHealth() (int64, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + server.DefaultAddr + "/health")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Status  string `json:"status"`
		UptimeS int64  `json:"uptimeS"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("decode: %w", err)
	}
	return payload.UptimeS, nil
}

// claudeprojectsRoot returns the directory the daemon should watch for
// Claude Code transcripts. CLAUDE_PROJECTS_ROOT overrides; default is
// ~/.claude/projects.
func claudeprojectsRoot() string {
	if v := os.Getenv("CLAUDE_PROJECTS_ROOT"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".claude/projects"
	}
	return home + "/.claude/projects"
}

func ensureParentDir(path string) error {
	dir := pathDir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return nil
}

func pathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

func writePid(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644)
}

func readPid(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("malformed pidfile: %w", err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("malformed pidfile: pid %d not positive", pid)
	}
	return pid, nil
}

// processAlive sends signal 0, which performs error checking but does
// not actually deliver a signal. Standard Unix idiom for "is pid alive?"
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
