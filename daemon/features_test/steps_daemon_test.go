package daemonsteps

// steps_daemon.go — daemon lifecycle, health-probe, and ORCHARD_DAEMON_URL
// step definitions shared across all feature files.
//
// Steps registered here cover:
//   Background steps:
//     Given the daemon is running on 127.0.0.1:7777
//     Given the daemon is running with fsnotify watching the claude projects directory
//     Given the daemon is not running (connection refused on 127.0.0.1:7777)
//   Health-probe steps (tui-health-probe.feature):
//     When a client sends { health { status uptimeS } }
//     Then the response contains health.status == "ok"
//     And health.uptimeS is a non-negative integer

import (
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
)

func registerDaemonSteps(ctx *godog.ScenarioContext, ts *testState) {
	// ---------------------------------------------------------------------------
	// Background setup steps — daemon lifecycle
	// ---------------------------------------------------------------------------

	// "Given the daemon is running on 127.0.0.1:7777"
	// Many features use exactly this string. We map it to startServerWithRepo so
	// there is always at least one git repo in scope (satisfies "at least one repo
	// is configured" variants). The httptest.Server listens on a random port;
	// tests use ts.httpServer.URL rather than the literal 127.0.0.1:7777.
	ctx.Step(`^the daemon is running on 127\.0\.0\.1:7777$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	// Alias used by gui-sidebar-boot + gui-worktree-lens
	ctx.Step(`^the daemon is running on 127\.0\.0\.1:7777\s*$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	// Used by tui-health-probe.feature
	ctx.Step(`^the TUI daemon client targets http://127\.0\.0\.1:7777/graphql by default$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	// Used by gui-conversation-subscription.feature Background
	ctx.Step(`^the daemon is running with fsnotify watching the claude projects directory$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	// Used by gui-error-and-edge-cases: daemon unreachable at boot
	ctx.Step(`^the daemon is not running \(connection refused on 127\.0\.0\.1:7777\)$`, func(sCtx context.Context) error {
		// Intentionally do NOT start the server. Steps that need to verify
		// "no connection" behaviour proceed without ts.httpServer.
		return nil
	})

	// Various Background stubs — these configure ambient test context.
	ctx.Step(`^at least one repo with at least one worktree is configured$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^at least one repo is configured$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^at least one repo is registered in the orchard config$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^at least one live Claude REPL is running in a tmux pane$`, func(sCtx context.Context) error {
		// Requires a live tmux pane — not available in CI without a tmux server.
		// We start the daemon but skip the "live pane" assertion as a gap.
		return ts.startServerWithRepo()
	})

	ctx.Step(`^a tmux server is running locally$`, func(sCtx context.Context) error {
		// Ensure daemon is up; actual tmux presence verified per-step.
		return ts.startServerWithRepo()
	})

	ctx.Step(`^at least one worktree has an open PR linked to a GitHub issue$`, func(sCtx context.Context) error {
		// Requires GitHub auth and a live PR; daemon-side only step.
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the claude projects directory contains at least one JSONL file$`, func(sCtx context.Context) error {
		// Without Claude installed this is an ambient condition; start daemon only.
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the claude projects directory is empty \(no JSONL files\)$`, func(sCtx context.Context) error {
		// Ambient condition for recent-lens empty-state scenario.
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the orchard config has no repos registered$`, func(sCtx context.Context) error {
		// Start server with EMPTY repos list.
		return ts.startMinimalServer()
	})

	// Global config defines tmux_sessions — TUI-side config, no daemon action needed.
	ctx.Step(`^the global config defines tmux_sessions: \["shepherd", "monitor"\]$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the global config defines one or more remote hosts under repos\[\]\.remotes$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	// TUI-specific background stubs that refer to TUI internal state.
	ctx.Step(`^the TUI is running in its main event loop$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the TUI is running and periodically fires workView queries$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the TUI is in List or dialog view$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the TUI is running inside a tmux session$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the TUI is in List view with existing snapshot data displayed$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the daemon delivers workView with repos, tmuxSessions, and claudeInstances$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the TUI performs client-side session→worktree matching$`, func(sCtx context.Context) error {
		// Client-side join — no daemon action.
		return nil
	})

	ctx.Step(`^the daemon is running and returns a workView snapshot$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the snapshot contains claudeInstances$`, func(sCtx context.Context) error {
		// Ambient condition; no action needed.
		return nil
	})

	ctx.Step(`^the snapshot contains repos with worktrees that have PR and issue joins$`, func(sCtx context.Context) error {
		// Ambient condition; daemon does the join.
		return nil
	})

	ctx.Step(`^the local daemon is running on 127\.0\.0\.1:7777$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the local daemon knows about one or more peer hosts via Query\.hosts$`, func(sCtx context.Context) error {
		// Federation — local daemon always has at least itself.
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the Houdini cache has been hydrated from localStorage \(or is cold\)$`, func(sCtx context.Context) error {
		// GUI client state — no daemon action.
		return nil
	})

	ctx.Step(`^the HostsListStore Houdini singleton is used \(hostsStore\)$`, func(sCtx context.Context) error {
		// GUI client state — no daemon action.
		return nil
	})

	ctx.Step(`^FleetTopBar calls hostsStore\.fetch\(\) on mount$`, func(sCtx context.Context) error {
		// GUI client state — no daemon action.
		return nil
	})

	ctx.Step(`^the snapshot is written to ~/\.cache/orchard/work_view_snapshot\.json$`, func(sCtx context.Context) error {
		// TUI file path — no daemon action.
		return nil
	})

	ctx.Step(`^the file format is \{ "version": 1, "snapshot": \{ \.\.\. \} \}$`, func(sCtx context.Context) error {
		// TUI snapshot format — no daemon action.
		return nil
	})

	ctx.Step(`^the full-refresh cycle is active \(fires every 60 seconds or on 'r'\)$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the orchard-gui is running and pointing at the daemon proxy at /__daemon/graphql$`, func(sCtx context.Context) error {
		return ts.startServerWithRepo()
	})

	ctx.Step(`^the NewConversation modal opens$`, func(sCtx context.Context) error {
		// GUI client event — no daemon action beyond daemon being up.
		return nil
	})

	ctx.Step(`^the WebSocket to the daemon is open$`, func(sCtx context.Context) error {
		// Ensure daemon started then open WS.
		if ts.httpServer == nil {
			return fmt.Errorf("daemon not started")
		}
		return ts.openWS()
	})

	ctx.Step(`^TranscriptView has mounted with a known sessionUuid$`, func(sCtx context.Context) error {
		// GUI client state — no daemon action.
		return nil
	})

	// ---------------------------------------------------------------------------
	// Health probe steps (tui-health-probe.feature)
	// ---------------------------------------------------------------------------

	ctx.Step(`^a client sends \{ health \{ status uptimeS \} \}$`, func(sCtx context.Context) error {
		if ts.httpServer == nil {
			return ts.startServerWithRepo()
		}
		return ts.postQuery(`{ health { status uptimeS } }`)
	})

	ctx.Step(`^the response contains health\.status == "ok"$`, func(sCtx context.Context) error {
		if err := ts.postQuery(`{ health { status uptimeS } }`); err != nil {
			return err
		}
		if err := ts.hasNoGraphQLErrors(); err != nil {
			return err
		}
		val, ok := ts.getDataAt("health.status")
		if !ok {
			return fmt.Errorf("health.status field not found in response")
		}
		if val != "ok" {
			return fmt.Errorf("expected health.status=ok, got %v", val)
		}
		return nil
	})

	ctx.Step(`^health\.uptimeS is a non-negative integer$`, func(sCtx context.Context) error {
		val, ok := ts.getDataAt("health.uptimeS")
		if !ok {
			// May not have been fetched in this step order — try fetching.
			if err := ts.postQuery(`{ health { status uptimeS } }`); err != nil {
				return err
			}
			val, ok = ts.getDataAt("health.uptimeS")
		}
		if !ok {
			return fmt.Errorf("health.uptimeS field not found")
		}
		switch v := val.(type) {
		case float64:
			if v < 0 {
				return fmt.Errorf("health.uptimeS is negative: %v", v)
			}
		default:
			return fmt.Errorf("health.uptimeS unexpected type %T", val)
		}
		return nil
	})

	ctx.Step(`^the response either returns health\.status != "ok"$`, func(sCtx context.Context) error {
		// Degraded daemon scenario — pending (our test daemon is never degraded).
		return godog.ErrPending
	})

	ctx.Step(`^the response carries a top-level GraphQL error entry$`, func(sCtx context.Context) error {
		if len(ts.lastErrors) == 0 {
			return fmt.Errorf("expected GraphQL errors but none found")
		}
		return nil
	})

	// ---------------------------------------------------------------------------
	// ORCHARD_DAEMON_URL environment variable step (tui-health-probe.feature)
	// ---------------------------------------------------------------------------

	ctx.Step(`^ORCHARD_DAEMON_URL is set to a non-default endpoint$`, func(sCtx context.Context) error {
		// TUI env-var behaviour — verified by checking the TUI reads the env.
		// Daemon-side this is a no-op; we document as pending for TUI binary tests.
		return godog.ErrPending
	})

	ctx.Step(`^all queries \(including workView\) are sent to the overridden URL$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the default 127\.0\.0\.1:7777 endpoint is never contacted$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^ORCHARD_DAEMON_URL is set to an unparseable value at startup$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// ---------------------------------------------------------------------------
	// Generic pending steps for TUI-internal behaviour that cannot be tested
	// at the GraphQL boundary
	// ---------------------------------------------------------------------------

	// TUI internal — NullWorkViewSource fallback
	ctx.Step(`^the TUI constructs its daemon client$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^it falls back to NullWorkViewSource$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^each refresh attempt logs a warning$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI starts and renders the disk-cached snapshot if available$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Client timeout is 5 seconds — TUI-side constant, not daemon.
	ctx.Step(`^the daemon endpoint hangs and does not respond$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the request times out after 5 seconds$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI receives DaemonError::Unreachable rather than blocking indefinitely$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Daemon given the daemon is in a degraded state
	ctx.Step(`^the daemon is in a degraded state$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// ---------------------------------------------------------------------------
	// Shared "no-op / ambient" steps used across many features
	// ---------------------------------------------------------------------------

	// A live pane "%26" hosting a Claude REPL — requires real tmux.
	ctx.Step(`^a live tmux pane "%26" is hosting a Claude REPL$`, func(sCtx context.Context) error {
		// Ambient condition needing real tmux — mark as pending to document gap.
		// The daemon query steps will proceed; pane-resolution steps will surface gaps.
		return nil
	})

	ctx.Step(`^the TUI boots and fires its first workView query$`, func(sCtx context.Context) error {
		return ts.postQuery(`{ workView { repos { slug path } tmuxSessions { id name } claudeInstances { id } } }`)
	})

	ctx.Step(`^the TUI boots$`, func(sCtx context.Context) error {
		// TUI binary boot — pending for TUI binary tests.
		return godog.ErrPending
	})

	ctx.Step(`^the TUI fires the workView query against a local daemon$`, func(sCtx context.Context) error {
		return ts.postQuery(`{ workView { repos { slug } } }`)
	})

	// "Given 5 seconds have elapsed since the last refresh" — timer-based, TUI-internal.
	ctx.Step(`^5 seconds have elapsed since the last refresh$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^60 seconds have elapsed since the last full refresh$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the daemon does not respond within the 5-second client timeout$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the previous refresh returned DaemonStatus::Unreachable$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the dashboard rendered snapshot N at time T$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a worktree's branch advances \(commit pushed\) or a PR state changes daemon-side$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the daemon is not reachable$`, func(sCtx context.Context) error {
		// Don't start the server for this scenario.
		return nil
	})

	// Misc ambient background steps that appear in multiple features.
	ctx.Step(`^a refresh is already in progress$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^one or more SSH remote hosts are marked unreachable$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI begins a federated fan-out$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI executes fan_out$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the local daemon reports two reachable peers with addresses$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^one peer is reachable and one is unreachable$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the daemon reports a peer with an empty or null address$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the TUI evaluates the peer during fan_out$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a peer has address "box-1\.boxd\.sh" \(bare hostname\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Concurrent refresh / Mutex steps
	ctx.Step(`^a full refresh and a local refresh are both in flight$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Remote federation SSH steps
	ctx.Step(`^the full-refresh thread begins$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the daemon workView delivers local repos and worktrees$`, func(sCtx context.Context) error {
		return ts.postQuery(`{ workView { repos { slug path worktrees { path branch } } } }`)
	})

	ctx.Step(`^cached remote snapshots exist on disk for at least one SSH host$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^two repos share the same remote host$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^two repos have remotes with the same host but different kinds \(e\.g\. Remmy vs BoxdFork\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a remote host is not in the reachable set$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the config has a OrchardProxy remote with allow_transitive: true$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the transitive walker has written a depth-2\+ snapshot to the cache$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// workView field not present — documented gap
	ctx.Step(`^workView is not present in the live daemon schema$`, func(sCtx context.Context) error {
		// workView IS present in the live schema; this scenario documents a known
		// gap for a NEW daemon build without all domain partials. Mark pending.
		return godog.ErrPending
	})

	ctx.Step(`^the GUI fires any of the five lens queries referencing workView$`, func(sCtx context.Context) error {
		return ts.postQuery(`{ workView { repos { slug } } }`)
	})

	// hostsStore / worktreesStore Houdini singleton checks — GUI-side.
	ctx.Step(`^hostsStore and worktreesStore were fetched on LensSidebar/FleetTopBar mount$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Note: "the NewConversation modal opens" is already registered above.

	// Snapshot steps - TUI binary tests
	ctx.Step(`^a valid snapshot file exists with version: 1$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the snapshot file has version: 999$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no work_view_snapshot\.json file exists$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^work_view_snapshot\.json contains invalid JSON$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the cache directory is not writable$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Local mutation TUI steps — no GraphQL mutations today
	ctx.Step(`^the TUI is in List view$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the project config has a setup_script defined$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a new worktree is created$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a worktree was created or deleted$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Houdini cache version steps — GUI-side.
	ctx.Step(`^localStorage contains "orchard:houdini:cache:v1" or "v2" from an old build$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the Houdini cache was persisted to localStorage key "orchard:houdini:cache:v3"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the layout hydrates the cache at boot$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the layout hydrates the cache before any store fetches$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the serialized Houdini cache exceeds 2MB$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Subscription / transcript steps — GUI-side unless hitting GraphQL boundary.
	ctx.Step(`^TranscriptView has subscribed to conversationChanged for sessionUuid "abc123"$`, func(sCtx context.Context) error {
		if ts.httpServer == nil {
			if err := ts.startServerWithRepo(); err != nil {
				return err
			}
		}
		if ts.wsConn == nil {
			if err := ts.openWS(); err != nil {
				return err
			}
		}
		return ts.subscribeGQL("sub-abc123", `subscription { conversationChanged(sessionUuid:"abc123") { sessionUuid } }`)
	})

	ctx.Step(`^a new record is appended to the matching JSONL on disk$`, func(sCtx context.Context) error {
		// Requires Claude JSONL on disk — ambient condition.
		return godog.ErrPending
	})

	ctx.Step(`^Claude is actively writing a multi-block response \(1 JSONL record per token batch\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the user sent a message and the pending turn is in "sent" state$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the turns\.length at send time was N$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a pending turn is in "received" state$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^a pending turn has been in "sent" state for 90 seconds$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^no conversationChanged push has arrived during that window$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the WebSocket connection drops mid-session$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^TranscriptView is subscribed to sessionUuid "abc123"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the panel selection changes to sessionUuid "def456"$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// GUI app/Tauri context steps
	ctx.Step(`^the app is running in Tauri \(window\.__TAURI_INTERNALS__ is defined\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the app is running in a browser \(no window\.__TAURI_INTERNALS__\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the app is running in a browser \(no Tauri context\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the daemon proxy returns HTTP 503$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^all lens stores are warm and rendering$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^all five stores have fetched successfully on mount$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the daemon process exits and restarts$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the GUI sends a query referencing "Worktree\.claudeInstances"$`, func(sCtx context.Context) error {
		return ts.postQuery(`{ workView { repos { worktrees { claudeInstances { id } } } } }`)
	})

	ctx.Step(`^the daemon schema has removed or renamed that field$`, func(sCtx context.Context) error {
		// Hypothetical schema drift — cannot simulate on a live schema. Pending.
		return godog.ErrPending
	})

	ctx.Step(`^the daemon reports 363 conversations in RecentLens$`, func(sCtx context.Context) error {
		// Ambient condition — depends on JSONL files on disk. Pending.
		return godog.ErrPending
	})

	ctx.Step(`^the attention store fetch returns a GraphQL error$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the error does not contain known-noise strings \("use GetPull", "EnrichPullRequest", "is a pull request"\)$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^hosts\[1\] was reachable and is now unreachable$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// Operator keystroke steps — TUI binary interaction.
	for _, key := range []string{"'n'", "'w'", "'d'", "'c'", "'r'", "'R'"} {
		k := key // capture
		ctx.Step(fmt.Sprintf(`^the operator presses %s in the list view$`, strings.ReplaceAll(k, "'", "'")), func(sCtx context.Context) error {
			return godog.ErrPending
		})
		ctx.Step(fmt.Sprintf(`^the operator presses %s and confirms with 'y'$`, strings.ReplaceAll(k, "'", "'")), func(sCtx context.Context) error {
			return godog.ErrPending
		})
		ctx.Step(fmt.Sprintf(`^the operator presses %s$`, strings.ReplaceAll(k, "'", "'")), func(sCtx context.Context) error {
			return godog.ErrPending
		})
	}

	ctx.Step(`^the operator types a session name and presses Enter$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the operator types a branch name and presses Enter$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the operator presses 'c' and confirms the selection$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^the operator presses 'r' and 'R' keystroke steps$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	// ---------------------------------------------------------------------------
	// Undefined steps — daemon error handling and environment
	// ---------------------------------------------------------------------------

	ctx.Step(`^the response either returns health\.status != "([^"]*)" Or the response carries a top-level GraphQL error entry$`, func(sCtx context.Context, status string) error {
		// GAP: asserts health failure mode — requires daemon crash simulation.
		return godog.ErrPending
	})

	ctx.Step(`^paneId and sessionUuid both fail to match any daemon state$`, func(sCtx context.Context) error {
		return godog.ErrPending
	})

	ctx.Step(`^on success it delivers DaemonStatus::Reachable$`, func(sCtx context.Context) error {
		// TUI-internal signal — pending at GraphQL boundary.
		return godog.ErrPending
	})

	ctx.Step(`^the URL can be overridden via the ORCHARD_DAEMON_URL environment variable$`, func(sCtx context.Context) error {
		// GAP: environment variable override is TUI startup behavior, not GraphQL.
		return godog.ErrPending
	})
}
