// Adapter wraps the tmux CLI. Per ADR-011 §3 it is stateless: cache,
// watcher, and invalidation live in the surrounding Provider.
//
// All field separation in -F format strings uses U+0001 (`\x01`) — tmux
// never emits that byte for the format-variables we read, so the parser
// never needs to consider quoting or escaping.

package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Field separator in -F format strings. tmux format-variables we read
// (names, indexes, integers, paths, terminal names) never contain this
// byte, so splitting on it round-trips losslessly.
const fieldSep = "\x01"

// CommandRunner is the test seam — production wires execRunner.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

// Run executes name with args and returns combined stdout. Errors are wrapped
// with enough context to diagnose failures in production logs.
//
// Issue #464: systemd-oomd kills the daemon with SIGKILL when the tmux exec
// storm causes excessive fork pressure. Previously the error string said only
// "signal: killed", which is indistinguishable from a ctx-cancel race in
// logs. Now signal-terminated exits surface the SIGxxx name so operators can
// confirm the oomd hypothesis without attaching a debugger.
func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Inspect WaitStatus to distinguish signal-terminated from
			// non-zero-exit — the two require different remediation paths.
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				sig := ws.Signal()
				return stdout.Bytes(), fmt.Errorf("%s %v: signal: %s (stderr: %q)", name, args, signalName(sig), strings.TrimSpace(stderr.String()))
			}
			return stdout.Bytes(), fmt.Errorf("%s %v: %w (stderr: %q)", name, args, err, strings.TrimSpace(stderr.String()))
		}
		return stdout.Bytes(), fmt.Errorf("%s %v: %w", name, args, err)
	}
	return stdout.Bytes(), nil
}

// signalName returns the canonical SIGxxx name for common signals.
// For unrecognised signals it falls back to syscall.Signal.String(), which
// returns e.g. "signal 31". The SIGxxx form is preferred because it matches
// what operators grep for in logs and is unambiguous across platforms.
func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	case syscall.SIGABRT:
		return "SIGABRT"
	default:
		return sig.String()
	}
}

// Adapter is the stateless tmux CLI client.
type Adapter struct {
	host   HostID
	socket string // empty = default socket
	runner CommandRunner
}

// NewAdapter constructs an Adapter targeting the default tmux socket on
// the given host. Use WithSocket for tests / non-default sockets.
func NewAdapter(host HostID) *Adapter {
	return &Adapter{host: host, runner: execRunner{}}
}

// WithSocket returns a copy of a addressing the given tmux socket via
// `-S <path>`. Lets E2E tests run against a sandbox tmux server.
func (a *Adapter) WithSocket(path string) *Adapter {
	cp := *a
	cp.socket = path
	return &cp
}

// WithRunner returns a copy of a using the given runner — for tests.
func (a *Adapter) WithRunner(r CommandRunner) *Adapter {
	cp := *a
	cp.runner = r
	return &cp
}

// Host returns the host id baked into every key the adapter emits.
func (a *Adapter) Host() HostID { return a.host }

// Socket exposes the configured socket path (empty = tmux default).
func (a *Adapter) Socket() string { return a.socket }

// tmuxArgs returns the global flags every tmux invocation gets.
// `-L`/`-S` come first per `man 1 tmux`.
func (a *Adapter) tmuxArgs(rest ...string) []string {
	out := make([]string, 0, len(rest)+2)
	if a.socket != "" {
		out = append(out, "-S", a.socket)
	}
	out = append(out, rest...)
	return out
}

// IsAlive shells out the cheapest possible command to detect a live
// tmux server. Used by both the adapter (to short-circuit on dead
// daemons) and the resolver (TmuxServer.alive).
func (a *Adapter) IsAlive(ctx context.Context) bool {
	_, err := a.runner.Run(ctx, "tmux", a.tmuxArgs("info")...)
	return err == nil
}

// FetchAll runs the four list-* commands and folds them into a single
// Snapshot. When the daemon is dead, FetchAll returns an EmptySnapshot
// without an error — callers see "no sessions" instead of an error.
func (a *Adapter) FetchAll(ctx context.Context) (Snapshot, error) {
	if !a.IsAlive(ctx) {
		snap := EmptySnapshot()
		snap.Server = ServerInfo{SocketPath: a.socketOrDefault(), Alive: false}
		return snap, nil
	}

	sessions, err := a.listSessions(ctx)
	if err != nil {
		return EmptySnapshot(), fmt.Errorf("list-sessions: %w", err)
	}
	windows, err := a.listWindows(ctx)
	if err != nil {
		return EmptySnapshot(), fmt.Errorf("list-windows: %w", err)
	}
	panes, err := a.listPanes(ctx)
	if err != nil {
		return EmptySnapshot(), fmt.Errorf("list-panes: %w", err)
	}
	clients, err := a.listClients(ctx)
	if err != nil {
		return EmptySnapshot(), fmt.Errorf("list-clients: %w", err)
	}
	server := a.serverInfo(ctx)

	return Snapshot{
		Server:   server,
		Sessions: sessions,
		Windows:  windows,
		Panes:    panes,
		Clients:  clients,
	}, nil
}

// CapturePane runs `tmux capture-pane -p` against a single pane. start
// and end are line numbers in tmux's history coordinate (negative
// values walk backwards into history, 0 is the top of the screen, etc.
// — see `man 1 tmux` "WINDOWS AND PANES").
//
// Pass startLine=0, endLine=0 with full=true to capture the entire
// visible+history buffer (`-S -`, `-E -`).
func (a *Adapter) CapturePane(ctx context.Context, pane PaneKey, start, end int, full bool, stripAnsi bool) (string, error) {
	args := []string{"capture-pane", "-p", "-t", pane.PaneID}
	if !stripAnsi {
		// `-e` keeps escape sequences. By default capture-pane strips
		// them, which matches `stripAnsi: true` in the schema.
		args = append(args, "-e")
	}
	if full {
		args = append(args, "-S", "-", "-E", "-")
	} else {
		if start != 0 {
			args = append(args, "-S", strconv.Itoa(start))
		}
		if end != 0 {
			args = append(args, "-E", strconv.Itoa(end))
		}
	}

	out, err := a.runner.Run(ctx, "tmux", a.tmuxArgs(args...)...)
	if err != nil {
		return "", fmt.Errorf("capture-pane %s: %w", pane.PaneID, err)
	}
	return string(out), nil
}

// CapturePaneTail captures the last `lines` rows. Implemented as
// `-S -<lines>` per tmux convention (negative starts walk into history).
func (a *Adapter) CapturePaneTail(ctx context.Context, pane PaneKey, lines int, stripAnsi bool) (string, error) {
	if lines <= 0 {
		lines = 50
	}
	args := []string{"capture-pane", "-p", "-t", pane.PaneID, "-S", strconv.Itoa(-lines)}
	if !stripAnsi {
		args = append(args, "-e")
	}
	out, err := a.runner.Run(ctx, "tmux", a.tmuxArgs(args...)...)
	if err != nil {
		return "", fmt.Errorf("capture-pane tail %s: %w", pane.PaneID, err)
	}
	return string(out), nil
}

// ----------------------------------------------------------------------
// list-* parsers. Each format string lists the variables in a known
// order separated by U+0001; the parser splits and converts.
// ----------------------------------------------------------------------

const sessionFormat = "" +
	"#{session_name}" + fieldSep +
	"#{session_created}" + fieldSep +
	"#{session_attached}" + fieldSep +
	"#{session_activity}" + fieldSep +
	"#{session_windows}" + fieldSep +
	"#{session_window_index}"

func (a *Adapter) listSessions(ctx context.Context) (map[SessionKey]Session, error) {
	out, err := a.runner.Run(ctx, "tmux", a.tmuxArgs("list-sessions", "-F", sessionFormat)...)
	if err != nil {
		// `no server running` is a valid (empty) state for newer tmux;
		// the IsAlive check above usually catches it. Defensive return.
		if strings.Contains(err.Error(), "no server running") {
			return map[SessionKey]Session{}, nil
		}
		return nil, err
	}
	out = bytes.TrimRight(out, "\n")
	if len(out) == 0 {
		return map[SessionKey]Session{}, nil
	}
	sessions := make(map[SessionKey]Session)
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		fields := strings.Split(string(line), fieldSep)
		if len(fields) != 6 {
			continue
		}
		key := SessionKey{Host: a.host, Name: fields[0]}
		sessions[key] = Session{
			Key:            key,
			CreatedAt:      parseUnix(fields[1]),
			Attached:       fields[2] != "" && fields[2] != "0",
			AttachedCount:  parseInt(fields[2]),
			LastActivityAt: parseUnix(fields[3]),
			WindowCount:    parseInt(fields[4]),
			CurrentWindow:  parseInt(fields[5]),
		}
	}
	return sessions, nil
}

const windowFormat = "" +
	"#{session_name}" + fieldSep +
	"#{window_index}" + fieldSep +
	"#{window_name}" + fieldSep +
	"#{window_active}" + fieldSep +
	"#{window_panes}" + fieldSep +
	"#{window_active_pane}"

func (a *Adapter) listWindows(ctx context.Context) (map[WindowKey]Window, error) {
	out, err := a.runner.Run(ctx, "tmux", a.tmuxArgs("list-windows", "-a", "-F", windowFormat)...)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") {
			return map[WindowKey]Window{}, nil
		}
		return nil, err
	}
	out = bytes.TrimRight(out, "\n")
	if len(out) == 0 {
		return map[WindowKey]Window{}, nil
	}
	windows := make(map[WindowKey]Window)
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		fields := strings.Split(string(line), fieldSep)
		if len(fields) != 6 {
			continue
		}
		key := WindowKey{
			Host:    a.host,
			Session: fields[0],
			Index:   parseInt(fields[1]),
		}
		windows[key] = Window{
			Key:         key,
			Name:        fields[2],
			Active:      fields[3] == "1",
			PaneCount:   parseInt(fields[4]),
			CurrentPane: fields[5],
		}
	}
	return windows, nil
}

const paneFormat = "" +
	"#{session_name}" + fieldSep +
	"#{window_index}" + fieldSep +
	"#{pane_id}" + fieldSep +
	"#{pane_title}" + fieldSep +
	"#{pane_current_command}" + fieldSep +
	"#{pane_pid}" + fieldSep +
	"#{pane_width}" + fieldSep +
	"#{pane_height}" + fieldSep +
	"#{pane_dead}"

func (a *Adapter) listPanes(ctx context.Context) (map[PaneKey]Pane, error) {
	out, err := a.runner.Run(ctx, "tmux", a.tmuxArgs("list-panes", "-a", "-F", paneFormat)...)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") {
			return map[PaneKey]Pane{}, nil
		}
		return nil, err
	}
	out = bytes.TrimRight(out, "\n")
	if len(out) == 0 {
		return map[PaneKey]Pane{}, nil
	}
	panes := make(map[PaneKey]Pane)
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		fields := strings.Split(string(line), fieldSep)
		if len(fields) != 9 {
			continue
		}
		paneKey := PaneKey{Host: a.host, PaneID: fields[2]}
		panes[paneKey] = Pane{
			Key: paneKey,
			WindowKey: WindowKey{
				Host:    a.host,
				Session: fields[0],
				Index:   parseInt(fields[1]),
			},
			Title:          fields[3],
			CurrentCommand: fields[4],
			CurrentPid:     parseInt(fields[5]),
			Width:          parseInt(fields[6]),
			Height:         parseInt(fields[7]),
			Dead:           fields[8] == "1",
		}
	}
	return panes, nil
}

const clientFormat = "" +
	"#{client_name}" + fieldSep +
	"#{client_session}" + fieldSep +
	"#{client_tty}" + fieldSep +
	"#{client_termname}" + fieldSep +
	"#{client_created}" + fieldSep +
	"#{client_activity}" + fieldSep +
	"#{client_readonly}" + fieldSep +
	"#{client_window_index}" + fieldSep +
	"#{client_active_pane}"

func (a *Adapter) listClients(ctx context.Context) (map[ClientKey]Client, error) {
	out, err := a.runner.Run(ctx, "tmux", a.tmuxArgs("list-clients", "-F", clientFormat)...)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") {
			return map[ClientKey]Client{}, nil
		}
		return nil, err
	}
	out = bytes.TrimRight(out, "\n")
	if len(out) == 0 {
		return map[ClientKey]Client{}, nil
	}
	clients := make(map[ClientKey]Client)
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		fields := strings.Split(string(line), fieldSep)
		if len(fields) != 9 {
			continue
		}
		// tmux historically used the tty path as `client_name`; newer
		// tmux releases honour the client name passed to `attach -t`.
		// Either way the value is unique within a server.
		name := fields[0]
		if name == "" {
			name = fields[2]
		}
		key := ClientKey{Host: a.host, ClientName: name}
		clients[key] = Client{
			Key:            key,
			Session:        fields[1],
			TTY:            fields[2],
			TermName:       fields[3],
			AttachedAt:     parseUnix(fields[4]),
			LastActivityAt: parseUnix(fields[5]),
			Readonly:       fields[6] == "1",
			CurrentWindow:  parseIntOrNeg1(fields[7]),
			CurrentPane:    fields[8],
		}
	}
	return clients, nil
}

// serverInfo gathers `display-message`-driven facts about the running
// tmux server. Best-effort; non-fatal failures collapse to zero values.
func (a *Adapter) serverInfo(ctx context.Context) ServerInfo {
	info := ServerInfo{SocketPath: a.socketOrDefault(), Alive: true}
	pidOut, err := a.runner.Run(ctx, "tmux", a.tmuxArgs("display-message", "-p", "#{pid}")...)
	if err == nil {
		info.Pid = parseInt(strings.TrimSpace(string(pidOut)))
	}
	return info
}

// socketOrDefault returns the configured socket path, falling back to a
// stable string for the daemon's default. tmux's actual default lives
// in $TMPDIR (e.g. /private/tmp/tmux-501/default on macOS); for callers
// that need a stable identifier the symbolic name is fine.
func (a *Adapter) socketOrDefault() string {
	if a.socket != "" {
		return a.socket
	}
	return "default"
}

// ----------------------------------------------------------------------
// Helpers. Tiny, intentionally — keep parsing locality high.
// ----------------------------------------------------------------------

func parseInt(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func parseIntOrNeg1(s string) int {
	if s == "" {
		return -1
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

// parseUnix accepts tmux's epoch-second timestamps (e.g. session_created).
// Empty / unparseable values yield the zero time, which downstream maps
// to a null GraphQL value.
func parseUnix(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}
