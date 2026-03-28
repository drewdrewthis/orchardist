# ADR-006: TEA pattern for TUI event handling

## Status

Accepted

## Date

2026-03-29

## Context

The TUI previously mixed input handling, state mutation, and rendering in a single event loop. Key handlers directly mutated `App` state and triggered side effects inline, making event flow hard to trace.

Adding mouse support and new dialogs (cleanup, transfer, heal, new worktree) increased the complexity of event routing. Every new interaction required careful ordering of guard clauses and mutations spread across the event loop body.

Testing was difficult because it required simulating full terminal events rather than testing state transitions in isolation. There was no clear boundary between "what the user did" and "what the app does about it."

## Decision

Adopt The Elm Architecture (TEA) — a unidirectional data flow through three distinct phases:

### 1. `handle_event(&self, event) -> Option<Message>`

A pure function mapping raw terminal events (key presses, mouse clicks, resize) to a `Message` enum. No state mutation. Returns `None` for unhandled events.

### 2. `update(&mut self, msg: Message) -> UpdateResult`

The only place state mutation occurs. Takes a `Message`, updates `App` state, and returns an `UpdateResult` indicating whether to continue, quit, or perform a side effect (switch session, open browser, delete worktree).

### 3. `render(&self, frame: &mut Frame)`

A stateless view function. Reads `App` state and draws the UI. No mutation, no side effects.

---

The `Message` enum in `tui/message.rs` defines every user intent:

```
Quit, CursorUp, CursorDown, CursorTo(usize), Enter, OpenPr, OpenIssue,
TogglePriority, Delete, NewWorktree, Transfer, CycleFilter, Search,
Refresh, Reconnect, Help, ...
```

Mouse events map to the same `Message` variants as their keyboard equivalents — a click maps to `CursorTo` plus a context-dependent action; scroll maps to `CursorUp`/`CursorDown`. This eliminates duplicated state mutation logic between input devices.

`UpdateResult` signals the event loop what to do next: continue polling, quit, or perform an I/O side effect. The event loop itself is a thin driver that calls these three functions in sequence.

## Consequences

**Positive:**

- Input handling is testable without a terminal — construct a `Message`, call `update`, assert state.
- Mouse and keyboard events share the same state mutation logic, eliminating duplication.
- New actions require adding a `Message` variant and an `update` match arm — a clear, consistent extension point.
- Rendering is a pure function of state, making screenshot tests reliable.

**Negative:**

- Indirection — tracing a keypress to its effect requires following the message through `handle_event` → `update`.
- The `Message` enum grows with every new action and could become large over time.

## Alternatives considered

**Direct mutation in key handlers (prior approach)** — rejected because it mixed concerns and was untestable in isolation.

**Trait-based command pattern** — rejected as over-engineered for a single TUI. The `Message` enum is simpler and sufficient.

**Redux-style with middleware** — rejected as unnecessary complexity for a single-threaded TUI.
