package claudeinstance

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// ShadowClassifier reads the session jsonl and classifies state using
// the pure jsonl classifier. It runs alongside the resolver output for
// diagnostic purposes — Phase 1 compared hook vs jsonl; Phase 2 logs
// when the hook-derived value diverges from the now-authoritative jsonl.
//
// When the two states disagree, the disagreement is logged at INFO so
// operators can diagnose edge cases in production.
type ShadowClassifier struct {
	projectsDir string
	logger      *slog.Logger
}

// NewShadowClassifier constructs a ShadowClassifier. When projectsDir
// is empty it resolves to ~/.claude/projects. Returns nil when the home
// directory is unresolvable; the composer guards against nil.
func NewShadowClassifier(projectsDir string, logger *slog.Logger) *ShadowClassifier {
	if projectsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		projectsDir = filepath.Join(home, ".claude", "projects")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ShadowClassifier{projectsDir: projectsDir, logger: logger}
}

// CompareAndLog runs the jsonl classifier for one heartbeat and logs any
// disagreement with the hook-derived state. Never modifies hookState.
// Tolerates missing jsonl files silently.
//
// Phase 2: jsonl is now authoritative; hookState is the reference value
// being compared against. Log message reflects the new authoritative source.
func (s *ShadowClassifier) CompareAndLog(
	hb Heartbeat,
	hookState graphql.InstanceState,
	now time.Time,
) {
	if s == nil || hb.Cwd == "" || hb.SessionID == "" {
		return
	}

	records, err := readRecordsFromPath(s.projectsDir, hb.Cwd, hb.SessionID)
	if err != nil {
		s.logger.Debug("claudeinstance shadow: jsonl read failed",
			"session_uuid", hb.SessionID,
			"tmux_session", hb.TmuxSession,
			"err", err,
		)
		return
	}
	if records == nil {
		s.logger.Debug("claudeinstance shadow: jsonl not found",
			"session_uuid", hb.SessionID,
			"tmux_session", hb.TmuxSession,
		)
		return
	}

	snap := ClassifyState(records, now)

	if snap.State != hookState {
		s.logger.Info("claudeinstance shadow: state diverged from hook",
			"session_uuid", hb.SessionID,
			"tmux_session", hb.TmuxSession,
			"hook_state", string(hookState),
			"jsonl_state", string(snap.State),
			"jsonl_inflight", snap.InflightToolCount,
		)
	}
}
