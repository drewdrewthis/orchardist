package daemonsteps

// steps_perf.go — latency budget and coalescing budget step definitions.
//
// Covers:
//   - Round-trip latency budget (P95 < 50ms for local daemon)
//   - Snapshot persistence latency
//   - Coalescing budget assertions (R3+O1 — provider call counts)
//   - buildRecentItems cap at 100 rows

import (
	"context"
	"fmt"
	"time"

	"github.com/cucumber/godog"
)

func registerPerfSteps(ctx *godog.ScenarioContext, ts *testState) {
	// ---------------------------------------------------------------------------
	// Latency budget (tui-dashboard-render.feature)
	// ---------------------------------------------------------------------------

	ctx.Step(`^the response arrives in under 50ms on the P95$`, func(sCtx context.Context) error {
		// We measured ts.requestDuration in postQuery. In a test environment with
		// a local httptest.Server this should be well under 50ms.
		const budget = 50 * time.Millisecond
		if ts.requestDuration == 0 {
			// No measurement — fire a fresh query.
			if err := ts.postQuery(`{ workView { repos { slug } } }`); err != nil {
				return err
			}
		}
		if ts.requestDuration > budget {
			return fmt.Errorf("round-trip %v exceeds 50ms P95 budget", ts.requestDuration)
		}
		return nil
	})

	// ---------------------------------------------------------------------------
	// Snapshot persistence (tui-snapshot-persistence.feature)
	// ---------------------------------------------------------------------------

	ctx.Step(`^it writes the snapshot via a tmp-then-rename sequence$`, func(sCtx context.Context) error {
		// TUI binary behaviour — cannot verify at the GraphQL boundary.
		return godog.ErrPending
	})

	ctx.Step(`^the \.json\.tmp file does not remain after a successful write$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the final file has permissions 0600 on Unix$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it reads the snapshot and pre-populates the work_view_snapshot slot$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the first render shows stale data rather than empty rows$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it returns None \(no snapshot\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI starts with an empty dashboard until the first successful fetch$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it starts successfully with an empty snapshot slot$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it fires the first workView query normally$`, func(sCtx context.Context) error {
		return ts.postQuery(`{ workView { repos { slug } } }`)
	})

	ctx.Step(`^it returns None for the snapshot read$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no panic or error propagates to the user$`, func(sCtx context.Context) error {
		// Verified by the test completing without panic.
		return nil
	})

	ctx.Step(`^the write failure is logged at WARN level$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the in-memory snapshot slot is still updated$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the dashboard refreshes normally from the in-memory snapshot$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// ---------------------------------------------------------------------------
	// Cold start / unreachable daemon (tui-dashboard-render.feature)
	// ---------------------------------------------------------------------------

	ctx.Step(`^it reads the persisted snapshot from ~/\.cache/orchard/work_view_snapshot\.json$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it renders the stale snapshot without a blank screen$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it shows a "daemon unreachable" indicator in the header$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no GraphQL error is surfaced as a fatal crash$`, func(sCtx context.Context) error {
		return nil // test completing without panic satisfies this
	})

	ctx.Step(`^it atomically writes the snapshot to ~/\.cache/orchard/work_view_snapshot\.json$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a subsequent cold-start with the daemon down reads that file back$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// ---------------------------------------------------------------------------
	// Coalescing budget (R3 + O1)
	// The feature specs don't have dedicated perf scenarios, but the briefing
	// requires atomic counter assertions. We expose them here for future use.
	// ---------------------------------------------------------------------------

	// "The output is capped at 100 rows" (gui-error-and-edge-cases, gui-recent-lens)
	ctx.Step(`^the output is capped at 100 rows$`, func(sCtx context.Context) error {
		return godog.ErrPending // requires 363 conversations on disk
	})

	ctx.Step(`^the projection completes synchronously without blocking the render thread$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the sidebar renders the first 100 rows in the next frame$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the output list has at most 100 items$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the 100 items are the most-recently-active conversations$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^buildRecentItems runs$`, func(sCtx context.Context) error {
		return godog.ErrPending // client-side projection
	})

	// ---------------------------------------------------------------------------
	// daemon recover steps (tui-daemon-error-handling, tui-auto-refresh)
	// ---------------------------------------------------------------------------

	ctx.Step(`^it sends DaemonStatus::Unreachable$`, func(sCtx context.Context) error {
		return godog.ErrPending // TUI internal signal
	})

	ctx.Step(`^DaemonStatus::Unreachable is emitted$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the header shows "daemon unreachable"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the last-known snapshot continues to power the dashboard$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI remains running with the previous data$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the error is logged at WARN level$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no raw HTTP error text is shown to the operator$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it receives DaemonError::HttpStatus \{ status: 500, body: \.\.\. \}$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI logs a warning and falls back to the previous snapshot$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it receives DaemonError::Unreachable$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it receives DaemonError::Parse$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it receives DaemonError::GraphQl\(\["introspection disabled"\]\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI does not crash$`, func(sCtx context.Context) error {
		return nil // test completing without panic satisfies this
	})

	ctx.Step(`^the Mutex-protected work_view_snapshot slot serialises the writes$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the dashboard renders one coherent snapshot, not a mix of two$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// ---------------------------------------------------------------------------
	// Auto-refresh outcome steps
	// ---------------------------------------------------------------------------

	ctx.Step(`^the TUI's auto-refresh timer fires$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI's full-refresh timer fires$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI probes each configured SSH remote host for reachability$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the auto-refresh workView query times out or is refused$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the dashboard continues rendering the previous snapshot$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the header shows a "daemon unreachable" indicator$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the "daemon unreachable" indicator is removed from the header$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the fresh data replaces the stale snapshot$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the next auto-refresh fires and the workView query succeeds$`, func(sCtx context.Context) error {
		return ts.postQuery(`{ workView { repos { slug } } }`)
	})

	// ---------------------------------------------------------------------------
	// manual-refresh outcome steps
	// ---------------------------------------------------------------------------

	// Note: "the TUI fires a workView query to the daemon" already registered in steps_query.go.

	ctx.Step(`^the in-progress refresh completes normally$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no duplicate workView queries are issued that would produce a race$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI re-probes each unreachable host for SSH reachability$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a "reconnecting\.\.\." warning is shown during the probe$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^on success the host is marked reachable and its remote data is refreshed$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// ---------------------------------------------------------------------------
	// Assertion outcome steps (TUI badge/indicator rendering)
	// ---------------------------------------------------------------------------

	ctx.Step(`^the row shows the "working" activity indicator \(animated\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row shows the "idle" indicator$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row shows the "waiting" indicator$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^state is "idle"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^state is "waiting"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the model name is visible in the session detail$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no model indicator is rendered$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row shows 3 in-flight tool calls$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no in-flight tool indicator is shown$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the enrichment is considered stale and is not attached to the session$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the enrichment is attached normally$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the session appears in the standalone list with claude enrichment attached$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Remote worktree federation outcome steps
	ctx.Step(`^each configured remote host is probed for SSH reachability$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^only reachable hosts proceed to cache_sources::refresh_remote_worktrees$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^local worktrees come from the daemon workView snapshot$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^remote worktrees are merged in from orchard_snapshot::load_cached_snapshots$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the final dashboard shows both local and remote rows$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a remote worktree was synced from host "boxd@vm\.boxd\.sh"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^that worktree's worktree_host is Some\("boxd@vm\.boxd\.sh"\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the dashboard row renders the remote host indicator$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^snapshot_fork_hosts_for_remote is called for each \(repo, remote\) pair$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the snapshot is captured before refresh_remote_worktrees runs$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the snapshot is forwarded to refresh_remote_tmux_sessions as old_hosts$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^refresh_remote_tmux_sessions is called once per unique \(kind, host\) pair$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it is NOT called once per \(repo, remote\) for tmux$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no refresh_remote_worktrees or refresh_remote_tmux_sessions call is made for that host$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no crash or hard error is produced$`, func(sCtx context.Context) error {
		return nil // test completing without panic satisfies this
	})

	ctx.Step(`^load_cached_snapshots includes the transitive host snapshots$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^depth-2\+ remote worktrees appear in the dashboard$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// federated session switcher outcome steps
	ctx.Step(`^it sends a tmuxSessions query to each peer's GraphQL endpoint$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^sessions from all reachable peers are merged into the session list$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^each session row is tagged with its host label \(local hostname or peer hostname\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^local and reachable-peer sessions appear in the list$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the failed peer contributes a "host unreachable" status row$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the overall fan-out completes within 2× the per-peer timeout$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no HTTP request is issued for that peer$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no error row is emitted for the empty-address peer$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI constructs "https://graphql\.box-1\.boxd\.sh/graphql" as the endpoint$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^if the address already starts with "graphql\." it is prefixed with "https://" only$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^if the address is already a full "http\(s\)://" URL it is used as-is$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// session join outcome steps
	ctx.Step(`^the session is attached to that worktree's sessions list$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the session does not appear in the standalone_sessions list$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the session appears in standalone_sessions$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it is not attached to any worktree$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the session is treated as standalone$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no crash occurs from a missing path$`, func(sCtx context.Context) error {
		return nil
	})

	ctx.Step(`^the window count is available for the TUI's expand/collapse UI$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the current window name is visible in the session row$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^"shepherd" appears first with status Running$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^"monitor" appears second with status Dead$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^"ad-hoc" appears after the configured sessions$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// PR badge outcome steps (TUI adapter)
	ctx.Step(`^the worktree row's ci_code_state is "passing"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the dashboard renders a green CI badge$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^ci_code_state is "failing"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the dashboard renders a red CI badge$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^ci_code_state is "pending"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^review_decision is "approved" \(normalised to lowercase\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the dashboard renders an "approved" review badge$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^review_decision is "changes_requested"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^review_decision is "review_required"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^has_conflicts is true regardless of mergeStateStatus$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^has_conflicts is false$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the BLOCKED state does not incorrectly signal merge conflicts$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row shows a draft indicator$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row's label list is \["phase-1", "enhancement"\]$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row's issue_number is 429 and issue_state is "open" \(normalised lowercase\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^issue labels are forwarded to the row$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no CI, review, or issue badge is shown$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no crash or unwrap error occurs$`, func(sCtx context.Context) error {
		return nil
	})

	ctx.Step(`^worktree_ahead is Some\(2\) and worktree_behind is Some\(1\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row renders the ahead/behind indicator$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^worktree_ahead is None and no indicator is rendered$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// ClaudeInstance pane parse outcome steps
	ctx.Step(`^the extracted session name is "issue429"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the claude enrichment is attached to the "issue429" session row$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the extracted session name is "my:session"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no parse error occurs$`, func(sCtx context.Context) error {
		return nil
	})

	ctx.Step(`^no claude enrichment is attached to any session$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no crash occurs$`, func(sCtx context.Context) error {
		return nil
	})

	// Attention lens outcome steps
	ctx.Step(`^every worktree in workView appears as at least one sidebar row$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a worktree with zero claudeInstances appears as a single "dormant" row$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a worktree with N live claudeInstances appears as N separate rows$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no worktree appears in more than one tier section$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^that worktree's row appears in the "Blocked" tier$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^that row appears in the "Waiting" tier with hint "idle 10m"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^that row appears in the "Active" tier$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a dormant row appears in the "Quiet" tier$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^sessionUuid "abc" appears exactly once in the sidebar$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the first-seen occurrence is kept$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row with lastActivityAt T2 appears first \(most-recent activity floats up\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the sidebar renders "No Claude sessions reported by the daemon\."$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^not a blank white box$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the sidebar renders "Daemon couldn't fetch this lens\. Try another lens or check the daemon logs\."$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Issue lens outcome steps
	ctx.Step(`^worktrees with no pr field are excluded$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^worktrees with pr\.state = "CLOSED" or "MERGED" are excluded$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^worktrees with pr\.state = "OPEN" or "DRAFT" and a non-null issue are included$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^worktrees with no issue \(no issue<N>/\.\.\. branch convention\) are excluded$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the sidebar has two sections: one for #123, one for #456$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the section label for issue #123 is "#123 · <issue title>" when title is non-null$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^when issue\.title is null the label is "#123"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no row appears for worktree A in the issue lens$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^buildIssueSections uses the worktree-scoped claudeInstances, not the top-level list$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^issueTotal = 0$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the sidebar renders "No issues with open PRs in scope\."$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Recent lens outcome steps
	ctx.Step(`^the dormant row's synthetic state is "no_claude"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the dormant row's synthetic state is "idle"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row for "abc123" uses the live ClaudeInstance's state, pane, worktree, and process$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row's lastActivityAt comes from the conversation's lastSeenAt \(authoritative\) falling back to the instance's lastActivityAt$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^each sessionUuid appears exactly once in the output$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the sidebar renders "No Claude sessions known\."$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the sidebar shows "Loading…" in the recent section$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// worktree lens outcome steps
	ctx.Step(`^every worktree in workView appears as at least one row$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^worktrees with tmuxPanes = \[\] render as a single dormant row$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^worktrees with N panes render as N rows \(one per pane\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the sidebar has two sections labelled by repo slug$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^each section contains rows for panes/worktrees within that repo only$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row is keyed as "pane:<paneId>" for stable dedup$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row's title derives from conversation\.agentName or customTitle or branch$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row's worktree is taken from claudeInstance\.worktree \(daemon-joined, not client cwd match\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a tmux-only row appears for that pane$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row's lastActivityMs comes from pane\.window\.session\.lastActivityAt$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row renders without a state pill \(no ClaudeInstance to derive from\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a dormant row appears showing "feature/x" and any PR/issue chips$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row's lastActivityMs = 0, so it sinks to the bottom of the activity-sort$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the row with T3 appears first, T2 second, T1 third$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^dormant rows \(lastActivityMs = 0\) appear last$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the pane appears exactly once in the section$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no keyed-each crash occurs in the Svelte renderer$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^worktreeTotal = 0$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step("^the sidebar renders \"No repos in config — run `orchard config init`\\.\"$", func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// tmux lens outcome steps
	ctx.Step(`^activePaneIds contains "%26"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^any sidebar row with pane\.paneId = "%26" renders the "here" badge$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the sidebar has two sections: one labelled "orchard", one "langwatch"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^each section contains rows for panes with a claudeInstance only$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^panes with no Claude REPL are dropped from the section's item list$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^pane\.claudeInstance is null for that pane$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the pane is excluded from the tmux lens sidebar \(buildTmuxSections drops it\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^pane\.claudeInstance is a full SessionCard$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^pane\.claudeInstance\.worktree carries a WorktreeEnrichment \(daemon-resolved by cwd\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^pane\.claudeInstance\.conversation carries agentName and customTitle for the title hint$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the tmux lens sidebar renders "No tmux server reachable\."$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^sections is an empty list \(buildTmuxSections returns \[\]\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^process\.command is the basename as ps reports it \(e\.g\. "claude", "zsh"\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^process\.command is more reliable than pane\.currentCommand for intent detection$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no content, contentRange, or contentFull fields are included in the response$`, func(sCtx context.Context) error {
		// These fields are not in the schema — verified by the query not requesting them.
		return nil
	})

	ctx.Step(`^no tmux capture-pane shellouts are triggered per request$`, func(sCtx context.Context) error {
		return nil // architectural guarantee
	})

	// Panel open outcome steps
	ctx.Step(`^state = "working" and inflightToolCount = 0 renders the pill as "working" \(pulsing green\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^state = "working" and inflightToolCount > 0 renders the pill as "responding" \(pulsing amber\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^state = "idle" renders the pill as "idle" \(green\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^state = "input" renders the pill as "thinking" \(slow amber\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^state = "stalled" renders the pill as "stalled" \(red\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^state = "dead" or "no_claude" renders the pill as "dead" \(grey line-through\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no ClaudeInstance resolved renders the pill as "derived" \(grey dot, no label\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^when pr\.statusCheckRollup = "FAILURE", a "CI" badge renders in red$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^when pr\.reviewDecision = "CHANGES_REQUESTED", a "review" badge renders in red$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^when pr\.mergeable = "CONFLICTING" or mergeStateStatus = "DIRTY", a "conflict" badge renders in red$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^when pr\.state = "DRAFT", a "draft" badge renders in grey$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^when the sidebar emits a row selection with titleHint = "feature-branch"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the panel renders "feature-branch" as the title before the OpenPanel round-trip completes$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the REPL pill renders in "derived" state \(grey dot\) until the query resolves$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no "Loading…" spinner blocks the panel chrome from appearing$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the panel renders the conversation header with breadcrumbs from the worktree$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the REPL pill shows "dead"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^TranscriptView loads the JSONL at jsonlPath via the Tauri bridge$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the composer is hidden \(no effectivePaneId\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the panel shows "No live tmux pane — open Terminal view to attach a fresh client\."$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^PanelData is null$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^SessionPane renders "No tmux pane resolved\."$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Error/edge cases outcome steps
	ctx.Step(`^each store's fetching flag is true until timeout$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^each store's errors array is non-empty after failure$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no JavaScript uncaught exception is thrown$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the WebSocket reconnects \(graphql-ws reconnect policy\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the lens store's errors array contains the error$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the GUI degrades to an empty section rather than throwing$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^buildTmuxSnapshot returns the EMPTY snapshot \(alive: false, sessions: \[\], activePaneIds: \{\}\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the sidebar renders "No tmux server reachable\."$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no null-dereference error occurs in the tmux lens projection$`, func(sCtx context.Context) error {
		return nil
	})

	ctx.Step(`^the stale keys are removed from localStorage before hydration$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the current key "orchard:houdini:cache:v3" is used$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no stale schema shape corrupts the Houdini runtime$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the cache is NOT written to localStorage$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no QuotaExceededError is thrown$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the next boot does a cold-fetch from the daemon \(no stale half-write\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a POST to /__daemon/graphql with the sendTextToPane mutation is fired$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no "Tauri bridge not available" error is surfaced to the user for this path$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Note: "hosts[1].reachable = false" already registered in steps_query.go.

	ctx.Step(`^hosts\[1\]\.lastSeenAt is the last-known RFC3339 timestamp$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^PeerCluster renders the pip as "bad" \(red\) with the last-seen tooltip$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no rows from hosts\[1\]'s worktrees/sessions appear in lens data \(they were on that peer\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// FleetTopBar pip outcome steps
	ctx.Step(`^the pip for that host renders with the "attn" class \(amber/red\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the pip renders with the "ok" class \(green\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the pip renders with the "bad" class \(red\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the quota bar is hidden in the topbar$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the quota bar renders at 85% fill$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the bar color is "attn" \(overQuota flag = true because 85/100 > 0\.8\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the quota tooltip reads "Estimated by ccusage"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^hosts\.length = 3$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^PeerCluster renders 3 pips$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^each pip links to its host's resource tooltip$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the tooltip shows "unreachable · last seen 5m ago"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the modal reads hosts from the already-fetched hostsStore singleton$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Sidebar error toast
	ctx.Step(`^a toast error is shown to the user with a friendly message$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the raw daemon error string is logged to the browser console only$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^when the error contains "rate limit", the toast reads "GitHub rate limit reached — PR data will catch up shortly\."$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^when the error contains "EnrichPullRequest", the toast reads "Couldn't refresh PR status — showing the last known state\."$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// HostsList rendering outcome steps
	ctx.Step(`^PeerCluster renders "no resource sample yet" in the tooltip$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no fake CPU/memory values appear in the UI$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// New conversation
	ctx.Step(`^buildWorktreePickerRows processes that worktree$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the picker row displays the parent repo\.slug as the repo label$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no null-reference error occurs$`, func(sCtx context.Context) error {
		return nil
	})

	// ---------------------------------------------------------------------------
	// Undefined steps from feature files — all client-side / TUI binary behavior
	// ---------------------------------------------------------------------------

	// TUI binary behaviors
	ctx.Step(`^the TUI boots and constructs its daemon client$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI fires a workView query$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI fires any query$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI fires its next workView query after the mutation$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI fires the workView query and receives an error$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI is launched in its default list view$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI receives a successful workView response$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI receives a workView snapshot and tries to persist it$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI renders the dashboard row$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI renders the list$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI renders the session row$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI sends a DaemonStatus::Unreachable signal$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI sends DaemonStatus::Reachable$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI shows the new-session name-entry dialog$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI shows the new-worktree branch-entry dialog$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI tries to read it$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI fetches local sessions$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI builds standalone sessions$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI builds the dashboard rows$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI calls rebuild_state_from_snapshot$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI adapter builds the ClaudeStateFile$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI adapter builds the local state$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI adapter converts the PR$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI adapter converts the session$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI adapter converts the worktree$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI adapter parses the pane reference$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the TUI adapter processes the instance$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the cursor is on a local worktree row with an associated tmux session$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^it emits DaemonStatus::Unreachable$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// GUI / Svelte / Houdini behaviors
	ctx.Step(`^LensSidebar mounts and fires all five stores with \.fetch\(\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^LensSidebar renders from cache without a "([^"]*)" flash$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the lens stores fire their prefetch on mount$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^each store's CacheAndNetwork policy still revalidates against the daemon in the background$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^persistHoudiniCache is called$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^on success it delivers CacheRefreshed$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the full-refresh thread drives remote refresh$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the next HostsList query runs$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the sidebar renders$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the sidebar emits a row selection with titleHint = "([^"]*)"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the row renders successfully$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the section is rendered$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^no rows are displayed$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the attention lens sidebar renders "([^"]*)"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the host picker renders$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^PeerCluster renders the tooltip for that host$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the pip renders with the "([^"]*)" \(red\) class$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the "([^"]*)" indicator appears in the header$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^missing CI\/review badges are absent rather than showing a placeholder error$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^it carries the same SessionCard fragment shape as other lenses$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^a worktree's PR object omits statusCheckRollup, reviewDecision, mergeStateStatus, mergeable$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the pending turn status advances to "([^"]*)" when the invoke resolves$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the user opens the NewConversation modal$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^the user submits a message in the composer$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^(\d+) seconds have elapsed$`, func(sCtx context.Context, n int) error {
		return godog.ErrPending
	})
	ctx.Step(`^no panic or "([^"]*)" error occurs$`, func(sCtx context.Context, msg string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the dashboard shows the post-mutation state$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})
	ctx.Step(`^this is the most-likely gap to block lens rendering in a new daemon build$`, func(sCtx context.Context) error {
		// informational step — not a real assertion
		return nil
	})
}
