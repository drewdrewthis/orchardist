package resolvers

// launchClaudeSession backs Mutation.launchSession. It shells out directly
// to tmux (the same pattern as SendTextToPane) instead of going through the
// read-only tmux snapshot adapter: create a detached session rooted at cwd,
// resolve its pane, then type the `claude` launch line into the pane's login
// shell and submit it with a separate Enter. The Claude session UUID is
// pre-assigned via `claude --session-id` so the caller can immediately
// subscribe to conversationChanged(sessionUuid:) and target the pane with
// sendTextToPane — no waiting for the JSONL to first appear under an
// unknown id.
//
// Lives outside *.resolvers.go so gqlgen never rewrites it.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	graphql1 "github.com/drewdrewthis/orchardist/internal/server/graphql"
)

func launchClaudeSession(ctx context.Context, input graphql1.LaunchSessionInput) (*graphql1.LaunchSessionResult, error) {
	cwd, err := resolveLaunchDir(input.Cwd)
	if err != nil {
		return nil, err
	}

	base := sanitizeTmuxName(derefString(input.Name))
	if base == "" {
		base = sanitizeTmuxName(filepath.Base(cwd))
	}
	sessionUUID := uuid.NewString()

	// 1. Create the detached tmux session rooted at cwd, retrying with a fresh
	//    suffix on a name collision. Letting new-session arbitrate is atomic;
	//    a has-session pre-check would race a concurrent create between the
	//    check and the create.
	sessionName, err := createUniqueSession(ctx, base, cwd)
	if err != nil {
		return nil, err
	}

	// Tear the session down if launch fails before it becomes a usable REPL,
	// so a half-created detached session can't leak into the list. Uses
	// exec.Command (not ctx) so cleanup still runs when ctx was cancelled.
	launched := false
	defer func() {
		if !launched {
			_ = exec.Command("tmux", "kill-session", "-t", "="+sessionName).Run()
		}
	}()

	// 2. Resolve the pane id of the freshly created session.
	paneOut, err := exec.CommandContext(ctx, "tmux", "list-panes", "-t", "="+sessionName, "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w: %s", err, strings.TrimSpace(string(paneOut)))
	}
	paneID := strings.TrimSpace(strings.SplitN(string(paneOut), "\n", 2)[0])
	if paneID == "" {
		return nil, fmt.Errorf("could not resolve pane id for new session %q", sessionName)
	}

	// 3. Type the claude launch line into the pane, then submit with Enter.
	//    Two-step literal-write + Enter mirrors SendTextToPane and runs the
	//    command in the pane's login shell (full PATH/env, e.g. ~/.local/bin).
	launchCmd := buildClaudeLaunch(sessionUUID, sessionName, derefString(input.Model), derefString(input.Prompt))
	if out, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", paneID, "-l", launchCmd).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux send-keys (launch): %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", paneID, "Enter").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("tmux send-keys (enter): %w: %s", err, strings.TrimSpace(string(out)))
	}

	launched = true
	return &graphql1.LaunchSessionResult{
		SessionName: sessionName,
		PaneID:      paneID,
		SessionUUID: sessionUUID,
		Cwd:         cwd,
	}, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// resolveLaunchDir validates and normalizes the launch working directory:
// trim, expand a leading ~, and confirm it is an existing directory.
func resolveLaunchDir(raw string) (string, error) {
	cwd := strings.TrimSpace(raw)
	if cwd == "" {
		return "", fmt.Errorf("cwd is empty")
	}
	if cwd == "~" || strings.HasPrefix(cwd, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		cwd = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(cwd, "~"), "/"))
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("cwd not accessible: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd is not a directory: %s", cwd)
	}
	return cwd, nil
}

// sanitizeTmuxName reduces a string to a tmux-safe session name. tmux
// forbids '.' and ':' in session names and chokes on whitespace; map any
// non [A-Za-z0-9_-] rune to '-', collapse runs, and trim. Empty in → "".
func sanitizeTmuxName(s string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, strings.TrimSpace(s))
	for strings.Contains(mapped, "--") {
		mapped = strings.ReplaceAll(mapped, "--", "-")
	}
	return strings.Trim(mapped, "-")
}

// createUniqueSession creates a detached tmux session rooted at cwd, named
// base (or "session" when empty), retrying with a short random suffix on a
// name collision. tmux rejects a duplicate name with a non-zero exit and
// "duplicate session" on stderr; letting new-session arbitrate is atomic,
// unlike a has-session pre-check which races a concurrent create between the
// check and the create. Returns the name actually created.
func createUniqueSession(ctx context.Context, base, cwd string) (string, error) {
	if base == "" {
		base = "session"
	}
	name := base
	for i := 0; i < 50; i++ {
		out, err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", name, "-c", cwd).CombinedOutput()
		if err == nil {
			return name, nil
		}
		if !strings.Contains(string(out), "duplicate session") {
			return "", fmt.Errorf("tmux new-session: %w: %s", err, strings.TrimSpace(string(out)))
		}
		name = base + "-" + uuid.NewString()[:4]
	}
	return "", fmt.Errorf("tmux new-session: no unique name for %q after 50 attempts", base)
}

// buildClaudeLaunch assembles the shell command line that boots an
// interactive Claude REPL with a pre-assigned session id. Values that can
// carry shell metacharacters (name, model, prompt) are single-quoted; the
// UUID is hex+dashes and needs no quoting.
func buildClaudeLaunch(sessionUUID, name, model, prompt string) string {
	parts := []string{
		"claude",
		"--session-id", sessionUUID,
		"--dangerously-skip-permissions",
		"--effort", "max",
		"--name", shellSingleQuote(name),
	}
	if m := strings.TrimSpace(model); m != "" {
		parts = append(parts, "--model", shellSingleQuote(m))
	}
	if p := strings.TrimSpace(prompt); p != "" {
		parts = append(parts, shellSingleQuote(p))
	}
	return strings.Join(parts, " ")
}

// shellSingleQuote wraps s in single quotes for /bin/sh, escaping embedded
// single quotes with the standard '\'' idiom.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
