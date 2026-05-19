// mutations.go implements the tmux domain's GraphQL mutations.
//
// All mutations exec the corresponding script under scripts/ and project
// its --json output as the response (L5). Daemon does not re-implement
// mutation logic beyond input validation + script-exec wrapping.
//
// Auth gating (M6): origin/capability checks happen at the resolver level
// before the script is exec'd. The script trusts its caller; the resolver
// is the trust boundary.
//
// Scripts:
//   - scripts/tmux-send-text.sh  → sendTextToPane
//   - scripts/tmux-kill-pane.sh  → killPane
//   - scripts/tmux-new-window.sh → newWindow
package tmux

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// MutationOriginChecker is the M6 auth gate. The caller wires this from
// the HTTP origin / WebSocket handshake trust context.
// Returning a non-nil error blocks the mutation.
type MutationOriginChecker interface {
	CheckMutationAllowed(ctx context.Context, mutationName, paneID string) error
}

// MutationResolvers holds the tmux mutation resolver implementations.
type MutationResolvers struct {
	Svc           TmuxService
	OriginChecker MutationOriginChecker // M6: may be nil (no auth, trusted context)
	ScriptsDir    string                // path to scripts/ dir; defaults to "scripts"
}

// scriptEnvelope is the L2 --json output shape all tmux scripts emit.
type scriptEnvelope struct {
	Ok    bool             `json:"ok"`
	Data  *json.RawMessage `json:"data,omitempty"`
	Error *scriptError     `json:"error,omitempty"`
}

type scriptError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SendTextToPane resolves Mutation.sendTextToPane.
//
// M4: validates paneId and text before exec.
// M6: checks origin before exec.
// L5: execs scripts/tmux-send-text.sh --pane <id> --text <body> --json.
func (r *MutationResolvers) SendTextToPane(ctx context.Context, paneID, text string) (bool, error) {
	// M4: input validation at resolver level.
	if strings.TrimSpace(paneID) == "" {
		return false, fmt.Errorf("sendTextToPane: paneId must not be empty")
	}
	if len(text) == 0 {
		return false, fmt.Errorf("sendTextToPane: text must not be empty")
	}

	// M6: origin/capability gate before exec.
	if r.OriginChecker != nil {
		if err := r.OriginChecker.CheckMutationAllowed(ctx, "sendTextToPane", paneID); err != nil {
			return false, fmt.Errorf("sendTextToPane: unauthorized: %w", err)
		}
	}

	script := r.scriptPath("tmux-send-text.sh")
	env, err := r.execScript(ctx, script, "--pane", paneID, "--text", text, "--json")
	if err != nil {
		return false, fmt.Errorf("sendTextToPane: exec: %w", err)
	}
	if !env.Ok {
		msg := "script failed"
		if env.Error != nil {
			msg = env.Error.Message
		}
		return false, fmt.Errorf("sendTextToPane: %s", msg)
	}
	return true, nil
}

// KillPane resolves Mutation.killPane.
//
// M4: validates paneId.
// M6: origin gate.
// L5: execs scripts/tmux-kill-pane.sh --pane <id> --json.
func (r *MutationResolvers) KillPane(ctx context.Context, paneID string) (bool, error) {
	if strings.TrimSpace(paneID) == "" {
		return false, fmt.Errorf("killPane: paneId must not be empty")
	}
	if r.OriginChecker != nil {
		if err := r.OriginChecker.CheckMutationAllowed(ctx, "killPane", paneID); err != nil {
			return false, fmt.Errorf("killPane: unauthorized: %w", err)
		}
	}
	script := r.scriptPath("tmux-kill-pane.sh")
	env, err := r.execScript(ctx, script, "--pane", paneID, "--json")
	if err != nil {
		return false, fmt.Errorf("killPane: exec: %w", err)
	}
	if !env.Ok {
		msg := "script failed"
		if env.Error != nil {
			msg = env.Error.Message
		}
		return false, fmt.Errorf("killPane: %s", msg)
	}
	return true, nil
}

// NewWindow resolves Mutation.newWindow.
//
// M4: validates sessionName.
// M6: origin gate (paneID is empty for this mutation; use sessionName as target).
// L5: execs scripts/tmux-new-window.sh --session <name> [--name <windowName>] --json.
func (r *MutationResolvers) NewWindow(ctx context.Context, sessionName string, windowName *string) (*TmuxWindowNode, error) {
	if strings.TrimSpace(sessionName) == "" {
		return nil, fmt.Errorf("newWindow: sessionName must not be empty")
	}
	if r.OriginChecker != nil {
		if err := r.OriginChecker.CheckMutationAllowed(ctx, "newWindow", ""); err != nil {
			return nil, fmt.Errorf("newWindow: unauthorized: %w", err)
		}
	}

	args := []string{"--session", sessionName, "--json"}
	if windowName != nil && *windowName != "" {
		args = append(args, "--name", *windowName)
	}
	script := r.scriptPath("tmux-new-window.sh")
	env, err := r.execScript(ctx, script, args...)
	if err != nil {
		return nil, fmt.Errorf("newWindow: exec: %w", err)
	}
	if !env.Ok {
		msg := "script failed"
		if env.Error != nil {
			msg = env.Error.Message
		}
		return nil, fmt.Errorf("newWindow: %s", msg)
	}

	// Project the data field as a new window node (L8: return affected node).
	if env.Data == nil {
		return nil, nil
	}
	var data struct {
		Session string `json:"session"`
		Index   int    `json:"index"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal(*env.Data, &data); err != nil {
		return nil, fmt.Errorf("newWindow: parse response: %w", err)
	}
	host := string(r.Svc.Host())
	w := Window{
		Key:  WindowKey{Host: HostID(host), Session: data.Session, Index: data.Index},
		Name: data.Name,
	}
	return projectWindowNode(w), nil
}

// execScript runs the script at path with the given args and parses the
// L2 JSON envelope from stdout. The script must emit {ok, data?, error?}.
func (r *MutationResolvers) execScript(ctx context.Context, path string, args ...string) (scriptEnvelope, error) {
	cmd := exec.CommandContext(ctx, "bash", append([]string{path}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		// Script exited non-zero — parse envelope from stdout if present.
		if len(out) > 0 {
			var env scriptEnvelope
			if jsonErr := json.Unmarshal(out, &env); jsonErr == nil {
				return env, nil
			}
		}
		return scriptEnvelope{}, fmt.Errorf("%s: %w", path, err)
	}
	var env scriptEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		return scriptEnvelope{}, fmt.Errorf("%s: parse envelope: %w (stdout: %q)", path, err, string(out))
	}
	return env, nil
}

// scriptPath returns the absolute path to a script. Uses r.ScriptsDir when
// set; otherwise falls back to a "scripts" dir relative to process cwd.
func (r *MutationResolvers) scriptPath(name string) string {
	dir := r.ScriptsDir
	if dir == "" {
		dir = "scripts"
	}
	return dir + "/" + name
}
