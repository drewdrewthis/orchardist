Feature: GUI panel open — OpenPanel query
  As the orchard-gui SessionPane
  I need to resolve a row identity (paneId and/or sessionUuid) to a full PanelData shape
  So that the panel header, REPL pill, transcript, and composer render without client-side joins.

  Operation consumed:
    OpenPanel($paneIds: [String!], $cwd: String)
      — tmuxPanes(filter:{paneIdIn:$paneIds, cwd:$cwd, command:"claude"}) [...PaneCard]
      — claudeInstances [...SessionCard]
      — conversations {sessionUuid, lastSeenAt, firstSeenAt, messageCount, open, recap, cwd, jsonlPath, agentName, customTitle}
      — workView.repos[].worktrees [...WorktreeEnrichment]

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And at least one live Claude REPL is running in a tmux pane

  @integration
  Scenario: Panel opens by paneId — daemon resolves session and worktree
    When SessionPane fires OpenPanel with paneIds: ["%26"] and no cwd
    Then tmuxPanes returns the pane matching %26 spreading PaneCard
    And claudeInstances includes the SessionCard for the Claude process in that pane
    And conversations includes the Conversation whose sessionUuid matches the instance
    And workView.repos[].worktrees includes a WorktreeEnrichment matching the session's process cwd
    And PanelData.pane, PanelData.session, PanelData.conversation, and PanelData.worktree are all non-null

  @integration
  Scenario: Panel opens by sessionUuid — no pane in scope
    When SessionPane fires OpenPanel with no paneIds and cwd from conversation.cwd
    Then tmuxPanes returns the pane(s) whose process cwd matches, filtered by command="claude"
    And claudeInstances includes the matching session
    And the panel can render worktree breadcrumbs and PR/issue chips from the resolved worktree

  @integration
  Scenario: Panel resolves conversation fields for header rendering
    When the OpenPanel query returns
    Then conversation.jsonlPath is a non-empty string (used to read the transcript via Tauri)
    And conversation.firstSeenAt and lastSeenAt are RFC3339 timestamps or null
    And conversation.messageCount is a non-negative integer
    And conversation.open is a boolean reflecting heartbeat freshness
    And conversation.recap is a string or null
    And conversation.agentName and customTitle are strings or null

  @integration
  Scenario: REPL state pill maps daemon InstanceState correctly
    When OpenPanel returns a ClaudeInstance with state
    Then state = "working" and inflightToolCount = 0 renders the pill as "working" (pulsing green)
    And state = "working" and inflightToolCount > 0 renders the pill as "responding" (pulsing amber)
    And state = "idle" renders the pill as "idle" (green)
    And state = "input" renders the pill as "thinking" (slow amber)
    And state = "stalled" renders the pill as "stalled" (red)
    And state = "dead" or "no_claude" renders the pill as "dead" (grey line-through)
    And no ClaudeInstance resolved renders the pill as "derived" (grey dot, no label)

  @integration
  Scenario: PR chips render from WorktreePR — fetched separately from WorktreeEnrichment
    When a worktree row is opened in the panel
    And the panel fires a second fetch including the WorktreePR fragment on the worktree
    Then the worktree gains a pr field with: number, state, statusCheckRollup, reviewDecision, mergeable, mergeStateStatus
    And when pr.statusCheckRollup = "FAILURE", a "CI" badge renders in red
    And when pr.reviewDecision = "CHANGES_REQUESTED", a "review" badge renders in red
    And when pr.mergeable = "CONFLICTING" or mergeStateStatus = "DIRTY", a "conflict" badge renders in red
    And when pr.state = "DRAFT", a "draft" badge renders in grey

  @integration
  Scenario: Panel loading interstitial is minimised via titleHint
    When the sidebar emits a row selection with titleHint = "feature-branch"
    Then the panel renders "feature-branch" as the title before the OpenPanel round-trip completes
    And the REPL pill renders in "derived" state (grey dot) until the query resolves
    And no "Loading…" spinner blocks the panel chrome from appearing

  @integration
  Scenario: Dead session — no tmux pane, conversation.jsonlPath present
    When OpenPanel returns no pane (tmuxPanes is empty) but conversation.jsonlPath is set
    Then the panel renders the conversation header with breadcrumbs from the worktree
    And the REPL pill shows "dead"
    And TranscriptView loads the JSONL at jsonlPath via the Tauri bridge
    And the composer is hidden (no effectivePaneId)
    And the panel shows "No live tmux pane — open Terminal view to attach a fresh client."

  @integration
  Scenario: OpenPanel returns empty — nothing resolved
    When paneId and sessionUuid both fail to match any daemon state
    Then PanelData is null
    And SessionPane renders "No tmux pane resolved."
