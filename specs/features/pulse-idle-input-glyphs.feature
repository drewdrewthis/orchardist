Feature: Pulse idle/input glyphs in column A; drop ❓ from status
  As a user scanning the TUI for sessions that need attention
  I want Idle and Input activity glyphs to animate in unison at 1s cadence
  So that stuck/waiting sessions draw the eye in peripheral vision and the activity axis stays distinct from the workflow status axis

  Background:
    Given the signal module defines Activity with variants None, Idle, Working, Input, Exhausted
    And the signal module defines PipelineStatus with variants including NeedsInput
    And the TUI render loop polls every 100ms in crates/orchard/src/tui/mod.rs

  # ===================================================================
  # Activity glyphs — static frames (Working, Exhausted, None)
  # ===================================================================

  @unit
  Scenario: Activity::Working renders static ⚡ regardless of tick
    Given the Activity::Working variant
    When glyph_at(tick) is called for tick in {0, 1}
    Then both frames return "⚡"

  @unit
  Scenario: Activity::Exhausted renders static 💀 regardless of tick
    Given the Activity::Exhausted variant
    When glyph_at(tick) is called for tick in {0, 1}
    Then both frames return "💀"

  @unit
  Scenario: Activity::None renders blank regardless of tick
    Given the Activity::None variant
    When glyph_at(tick) is called for tick in {0, 1}
    Then both frames return ""

  # ===================================================================
  # Activity glyphs — animated frames (Idle, Input)
  # ===================================================================

  @unit
  Scenario: Activity::Idle pulses between ○ and ● across ticks
    Given the Activity::Idle variant
    When glyph_at(0) and glyph_at(1) are called
    Then glyph_at(0) returns "○"
    And glyph_at(1) returns "●"

  @unit
  Scenario: Activity::Input pulses between ○ and ? across ticks
    Given the Activity::Input variant
    When glyph_at(0) and glyph_at(1) are called
    Then glyph_at(0) returns "○"
    And glyph_at(1) returns "?"

  @unit
  Scenario: glyph_at wraps every 2 ticks (symmetric 1s cadence)
    Given any animated Activity variant (Idle or Input)
    Then glyph_at(0) == glyph_at(2) == glyph_at(4)
    And glyph_at(1) == glyph_at(3) == glyph_at(5)

  @unit
  Scenario: legacy Activity::glyph() remains available and returns the tick=0 frame
    # Non-TUI callers (tests, JSON, labels) should not need to know about animation.
    Given any Activity variant V
    Then V.glyph() returns the same string as V.glyph_at(0)

  # ===================================================================
  # Theme additions — idle_pulse and input_pulse colors
  # ===================================================================

  @unit
  Scenario: Theme exposes claude_idle_pulse as orange by default
    Given the default Theme
    Then theme.claude_idle_pulse is an orange color (e.g. Color::Rgb(255, 165, 0) or equivalent)

  @unit
  Scenario: Theme exposes claude_input_pulse as red by default
    Given the default Theme
    Then theme.claude_input_pulse equals theme.error (or an explicit red)

  @unit
  Scenario: activity_style uses claude_idle_pulse for Idle
    Given the default Theme
    When activity_style(Activity::Idle, &theme) is called
    Then the returned Style foreground is theme.claude_idle_pulse

  @unit
  Scenario: activity_style uses claude_input_pulse for Input
    Given the default Theme
    When activity_style(Activity::Input, &theme) is called
    Then the returned Style foreground is theme.claude_input_pulse

  @unit
  Scenario: activity_style leaves Working unchanged (claude_active)
    Given the default Theme
    When activity_style(Activity::Working, &theme) is called
    Then the returned Style foreground is theme.claude_active

  @unit
  Scenario: activity_style leaves Exhausted unchanged (error)
    Given the default Theme
    When activity_style(Activity::Exhausted, &theme) is called
    Then the returned Style foreground is theme.error

  # ===================================================================
  # Status column — drop ❓ glyph, render hourglass when activity is Input
  # ===================================================================
  #
  # Decision: keep PipelineStatus::NeedsInput variant (it still drives sort
  # severity — first match in the merge-blocker hierarchy), but blank its glyph
  # and drop it from the legend. The visible "waiting on you" signal is carried
  # by the animated hourglass in the status column, which is driven off rollup
  # Activity::Input (not off PipelineStatus::NeedsInput). This decouples the
  # activity axis from the glyph while preserving sort order.

  @unit
  Scenario: PipelineStatus::NeedsInput.glyph() returns empty string
    # Existing signal.rs test `every_status_has_distinct_glyph` must be updated
    # to exclude NeedsInput from the distinctness set (alongside Coding, which
    # is already excluded).
    Given PipelineStatus::NeedsInput
    Then PipelineStatus::NeedsInput.glyph() returns ""

  @unit
  Scenario: Statuses other than Coding and NeedsInput each produce a unique non-empty glyph
    Given all PipelineStatus variants except Coding and NeedsInput
    Then each variant's glyph() is non-empty and distinct

  @integration
  Scenario: Cleanup dialog legend no longer lists NeedsInput with ❓
    # dialogs.rs ~353 currently renders `PipelineStatus::NeedsInput.glyph()` as
    # the first legend entry. After this feature it must be removed from the
    # legend (because the glyph is blank) and replaced with an hourglass entry.
    Given the cleanup dialog is rendered via TestBackend
    Then the rendered buffer does not contain the "❓" glyph
    And the rendered buffer contains a legend entry with "⏳" or "⌛" and "waiting on user input"

  @unit
  Scenario: status_glyph_at renders rotating hourglass when rollup activity is Input
    Given a row with rollup Activity::Input
    When status_glyph_at(row, tick=0) is called
    Then it returns "⏳"
    And status_glyph_at(row, tick=1) returns "⌛"

  @unit
  Scenario: status_glyph_at wraps every 2 ticks for Input activity
    Given a row with rollup Activity::Input
    Then status_glyph_at(row, 0) == status_glyph_at(row, 2)
    And status_glyph_at(row, 1) == status_glyph_at(row, 3)

  @unit
  Scenario: status_glyph_at falls through to PipelineStatus glyph for non-Input rows
    Given a row with rollup Activity::Working and status PipelineStatus::CiFailing
    When status_glyph_at(row, tick=0) is called
    Then it returns "🚫"

  @unit
  Scenario: status_glyph_at returns blank for Coding status with non-Input activity
    Given a row with rollup Activity::Working and status PipelineStatus::Coding
    When status_glyph_at(row, tick=0) is called
    Then it returns ""

  # ===================================================================
  # Sort severity preserved
  # ===================================================================

  @unit
  Scenario: Input-activity rows still sort above CiFailing rows
    # Removing ❓ glyph must not demote Input rows in the sort order.
    Given two worktree rows:
      | branch | activity | status     |
      | a      | Input    | NeedsInput |
      | b      | Working  | CiFailing  |
    When rows are sorted by RowSortKey
    Then branch "a" comes before branch "b"

  # ===================================================================
  # Unison tick — single source, all rows pulse together
  # ===================================================================

  @unit
  Scenario: Pulse tick is derived from elapsed wall-clock seconds
    # Stateless design — no per-row or per-App counter that could drift.
    Given the current wall-clock time
    When pulse_tick() is called
    Then it returns (now_secs % 2) as u8
    And two calls within the same second return the same tick

  @integration
  Scenario: Two renders with the same tick produce identical buffers
    # Prevents a render that straddles a second boundary from producing a mixed
    # frame (torn frame). The tick is sampled once at the top of render and
    # threaded through, so repeating a render with the same tick must be a
    # fixed point.
    Given an App with one Activity::Idle row and one Activity::Input row
    When render is invoked with pulse tick forced to 0
    And render is invoked a second time with pulse tick forced to 0
    Then the two rendered buffers are byte-identical

  @integration
  Scenario: All animated rows in one frame use the same tick value
    Given an App with three rows in Activity::Idle and two rows in Activity::Input
    When render is invoked
    Then every Idle row's column-A cell contains the same glyph
    And every Input row's column-A cell contains the same glyph

  # ===================================================================
  # Render throttling — no CPU spike when animations are offscreen
  # ===================================================================

  @unit
  Scenario: has_animated_visible_row returns true when any visible row is Idle
    Given App.task_rows visible range contains one row with rollup Activity::Idle
    Then has_animated_visible_row(&app) returns true

  @unit
  Scenario: has_animated_visible_row returns true when any visible row is Input
    Given App.task_rows visible range contains one row with rollup Activity::Input
    Then has_animated_visible_row(&app) returns true

  @unit
  Scenario: has_animated_visible_row returns false when no visible row animates
    Given App.task_rows contain only rollup Activity::{None, Working, Exhausted}
    Then has_animated_visible_row(&app) returns false

  @integration
  Scenario: Render loop repaints on pulse-tick boundary only when animations are visible
    # Baseline 100ms poll stays. When the tick boundary is crossed (tick changed
    # since App.last_pulse_tick) AND has_animated_visible_row is true, force a redraw.
    # When nothing animates, no forced redraw happens — event-driven repaint only.
    # Storage: App gains a `last_pulse_tick: u8` field, initialized to pulse_tick()
    # on startup, updated after each forced pulse redraw.
    Given the run_loop in crates/orchard/src/tui/mod.rs
    And App has a `last_pulse_tick: u8` field
    When one second elapses and has_animated_visible_row is true and pulse_tick() != app.last_pulse_tick
    Then terminal.draw is invoked and app.last_pulse_tick is updated
    When one second elapses and has_animated_visible_row is false
    Then no forced redraw is triggered by pulse-tick alone

  # ===================================================================
  # TUI snapshot — two frames differ for animated rows
  # ===================================================================

  @integration
  Scenario: Snapshot at tick=0 differs from tick=1 when any row animates
    Given a test App with one Idle row and one Input row
    When render is invoked with pulse tick forced to 0
    And render is invoked again with pulse tick forced to 1
    Then the rendered buffer at tick=0 differs from the buffer at tick=1
    And the difference is isolated to column A of the animated rows and the status column of the Input row

  @integration
  Scenario: Snapshot at tick=0 equals tick=1 when no row animates
    Given a test App with only Working/Exhausted/None rows
    When render is invoked with pulse tick forced to 0 and then 1
    Then the two rendered buffers are identical

  # ===================================================================
  # Regression — status column no longer shows ❓ for NeedsInput
  # ===================================================================

  @integration
  Scenario: Rendered row with Input activity does not contain ❓ anywhere
    Given a test App with one worktree whose rollup activity is Input
    When render is invoked
    Then the rendered buffer does not contain the "❓" glyph
    And column A contains either "○" or "?"
    And the status column contains either "⏳" or "⌛"

  # ===================================================================
  # Legend — new pulsing entries shown
  # ===================================================================

  @integration
  Scenario: Legend lists Idle pulse and Input pulse entries
    # The legend is rendered via ratatui; inspect the rendered buffer.
    Given the help/legend overlay is rendered via TestBackend
    Then the rendered buffer mentions Idle with "○" and "●" (pulsing)
    And the rendered buffer mentions Input with "○" and "?" (pulsing)
    And the rendered buffer mentions the hourglass as "waiting on user input"

  # ===================================================================
  # Scope: rows that animate
  # ===================================================================

  @integration
  Scenario: Standalone sessions (orchardist, non-worktree) animate the same way
    # Standalone session rows flow through the same activity rollup. They must
    # pulse in unison with worktree rows.
    Given App.standalone_sessions contains a row with rollup Activity::Idle
    When render is invoked
    Then the standalone row's activity cell pulses ○/● at the same tick as worktree rows

  @integration
  Scenario: Expanded pane sub-rows inherit the pulse behavior
    # Child pane rows (expanded tmux session/window/pane tree) render per-pane
    # activity glyphs via the same Activity enum. They pulse when Idle or Input.
    Given an expanded worktree row with a child pane in Activity::Input
    When render is invoked
    Then the child pane cell pulses ○/? at the shared tick

  # ===================================================================
  # Out of scope — explicit non-goals
  # ===================================================================
  # - watch/diff.rs ClaudeNeedsInput events are untouched (they carry data,
  #   not glyphs).
  # - tmux status-bar segment: orchard does not currently write a per-row tmux
  #   status segment that renders PipelineStatus glyphs; nothing to change.
  # - json_output.rs does not render status glyphs (it exposes enum string /
  #   label). NeedsInput still appears in JSON; consumers get the label.
  # - Working/Exhausted animation, configurable blink rate, orchardist policy
  #   on idle sessions — all explicitly out of scope per the issue.
