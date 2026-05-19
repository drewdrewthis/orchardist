# Testing Philosophy

> This document mirrors the testing philosophy from langwatch
> (`langwatch-workspace/langwatch-saas/langwatch/dev/docs/TESTING_PHILOSOPHY.md`)
> with the following deltas:
> - We use Go (daemon), Rust (TUI), and TypeScript (GUI). Patterns are per-language.
> - We don't use `@unimplemented`. Orchard is greenfield: scenario exists → test exists. No exceptions.
> - We don't maintain a `LEGACY_UNBOUND` deny-list. There is no legacy.
> - Scenario↔test parity is CI-enforced via `make check-feature-parity`.

---

## Core Principles

### Test Behavior, Not Implementation

Focus on **what** the code does, not **how** it does it. Tests validate user-visible
outcomes and GraphQL contracts, enabling internal refactoring without rewriting tests.

### No "Should" in Test Names

Use present tense, active voice. Describe expected behavior directly.

| Avoid | Prefer |
|-------|--------|
| `TestHealthShouldReturnOk` | `TestHealth_ReturnsStatusOk` |
| `TestReposShouldHaveFields` | `TestReposQuery_ReturnsRequiredFields` |
| `it("should redirect guests")` | `it("redirects guest users")` |

### Naming Convention per Language

| Language | Unit under test | Example |
|----------|----------------|---------|
| Go | `TestDomain_Action` | `TestHealth_ReturnsStatusOk` |
| Go (sub-case) | `t.Run("when X", ...)` | `t.Run("when pane not found", ...)` |
| Rust | `fn test_domain_action` (snake_case) | `fn test_snapshot_writes_atomically` |
| TypeScript | `describe("unit()")` + `it("does X")` | `describe("buildAttentionSections()")` |

### Nested Grouping for Context

Go uses `t.Run("when X", ...)` blocks to group sub-cases. This is the Go equivalent
of nested `describe` blocks in TypeScript/Jest.

```go
func TestReposQuery_WorktreeCarriesRequiredFields(t *testing.T) {
    ts := startServerWithRepo(t)

    t.Run("when worktrees are present", func(t *testing.T) {
        r := postGQL(t, ts.URL, `{ repos { worktrees { branch head } } }`)
        assertNoErrors(t, r)
        // ... assert fields
    })
}
```

### Single Expectation Per Test

Isolating assertions makes failures immediately clear. When multiple independent
behaviors need testing, use separate test functions or `t.Run` blocks.

---

## Coverage is Mandatory

Every scenario in a feature file has a test. This is not aspirational — it is enforced.

- **Bug fixes** must include a regression test tagged `@regression`.
- **New features** must have integration tests covering all acceptance criteria.
- **Refactors** must not reduce scenario coverage.

---

## Test Hierarchy

| Level | Purpose | Mocking | File suffix |
|-------|---------|---------|-------------|
| **E2E** | Stable core happy paths only | None | `*_e2e_test.go` |
| **Integration** | GraphQL boundary, domain joins | External only | `*_integration_test.go` (or plain `*_test.go`) |
| **Unit** | Pure logic, branches | Everything external | `*_test.go` |

E2E tests are deprioritized (same rationale as langwatch — expensive, brittle).
Integration and unit tests are the primary mechanism.

### Language-Specific Patterns

| Language | E2E | Integration | Unit |
|----------|-----|-------------|------|
| Go | `*_e2e_test.go` | `*_integration_test.go` | `*_test.go` |
| Rust | `*_e2e_tests.rs` | `*_integration_tests.rs` | `*_tests.rs` / inline `#[cfg(test)]` |
| TypeScript | `*.e2e.test.ts` | `*.integration.test.ts` | `*.unit.test.ts` |

---

## Feature File Parity

Feature specs define what tests must exist. **Every scenario must have at least one test.**

### Feature File Locations

| Tree | Feature files | Tests |
|------|--------------|-------|
| Daemon (Go) | `daemon/features/**/*.feature` | `daemon/**/*_test.go` |
| TUI (Rust) | `crates/orchard/features/**/*.feature` | `crates/orchard/**/*.rs` |
| GUI (TypeScript) | `crates/orchard-gui/features/**/*.feature` | `crates/orchard-gui/**/*.{test,spec}.ts` |

### Scenario Annotation Convention

The parity checker matches `.feature` scenario titles to `// @scenario <title>` annotations
placed **directly above** the test function declaration.

**Go:**
```go
// @scenario Health query returns status ok when daemon is serving
func TestHealth_ReturnsStatusOk(t *testing.T) {
    // ...
}
```

**Rust:**
```rust
// @scenario Snapshot is written atomically after every successful workView fetch
#[test]
fn test_snapshot_writes_atomically() {
    // ...
}
```

**TypeScript:**
```typescript
/** @scenario Attention lens sidebar shows blocked tier first */
it("renders blocked tier at top", () => {
    // ...
});
```

Titles must match verbatim. Stale annotations (titles not in any `.feature` file) fail CI.

### Tags

| Tag | Meaning | Use when |
|-----|---------|----------|
| `@unit` | Pure logic test | Testing functions, utilities, transformations |
| `@integration` | Component/boundary test | Testing rendering, API calls, GraphQL queries |
| `@regression` | Prevents a previously-fixed bug recurring | Bug fix scenarios |
| `@e2e` | Stable core flow | Only 5-10 stable happy-path tests |

---

## What We Don't Do

- **No `@unimplemented`.** Orchard is greenfield. If a scenario exists, a test exists.
- **No `LEGACY_UNBOUND` deny-list.** There is no legacy codebase here. The parity checker has zero tolerance.
- **No stub tests that always pass.** `assert.True(true)` is not a test. If a scenario can't be implemented in the daemon, move it to the client tree or delete it.
- **No godog.** Plain Go test functions with `// @scenario` annotations; no BDD framework overhead.

---

## Parity Enforcement

Run all three checkers with:

```bash
make check-feature-parity
```

Individual checkers:

```bash
make check-feature-parity-daemon   # zero tolerance
make check-feature-parity-tui      # informational gaps (follow-up work)
make check-feature-parity-gui      # informational gaps (follow-up work)
```

The daemon checker exits non-zero on **any** unbound scenario or stale annotation.
The TUI and GUI checkers exit non-zero only for stale annotations (follow-up gaps are surfaced but don't block).

---

## Mocking Strategy

Prefer real implementations over mocks.

- **Daemon tests (Go)**: use `httptest.Server` with real `gitprovider`, `host.Provider`, `claudeprojects` pointing at temp dirs. The GraphQL boundary is the test boundary (T4).
- **TUI tests (Rust)**: use real daemon fixture (or in-process fake that respects the service interface). No mocking below the GraphQL client.
- **GUI tests (TypeScript)**: stub external boundaries (graphql-ws, Tauri bridge) with `msw`/`vitest`; render components against real Houdini store logic.

---

## Test Data

Create minimal, context-specific data. Only generate what the test needs.

```go
// Avoid: kitchen-sink fixture
func setupFullOrchardState(t *testing.T) { /* 200 lines */ }

// Prefer: minimal fixture scoped to the test
ts := startServerWithRepo(t) // one repo, one worktree
```

---

## Latency and Coalescing Budgets

Tests for scenarios with stated latency budgets (e.g. "round-trip within 50ms") assert the
budget directly:

```go
_, elapsed := postGQLTimed(t, ts.URL, `{ repos { worktrees { branch } } }`)
if elapsed > 50*time.Millisecond {
    t.Errorf("latency %v exceeds 50ms budget", elapsed)
}
```

Tests for loader coalescing (T5) count underlying provider calls via instrumented counters
and assert `≤1 call per request`.
