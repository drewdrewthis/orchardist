---
name: orchard-worktree-test
description: Test a worktree's orchard binary locally before merging by repointing the ~/Library/pnpm/orchard symlink to the worktree's target/release/orchard. Use when the user says "test this worktree", "test locally", "test the binary", "let me try it", or before merging a PR that touches TUI/CLI behavior. Also handles reverting the symlink back to main.
user-invocable: true
argument-hint: "[test|revert]"
---

# Orchard Worktree Test

The `orchard` command in the user's shell is a symlink at `~/Library/pnpm/orchard` that points at `/Users/USER/workspace/orchardist/target/release/orchard` (the **main checkout's** release build). To test a worktree's changes via the shell `orchard` command, the symlink must be temporarily repointed at the worktree's `target/release/orchard`.

## Modes

| Argument | Action |
|----------|--------|
| `test` (default) | Build the worktree, repoint symlink to it, prompt user to test |
| `revert` | Restore symlink to main checkout, no rebuild |

## Test Mode

1. **Confirm working directory is a worktree.** Run `pwd` — it should be under `.worktrees/`. If not, abort and tell the user this skill only makes sense in a worktree.

2. **Build release in the worktree.** Run `cargo build --release` (use `~/.claude/scripts/build-lock.sh global timeout 300 cargo build --release` if the resource guard requires it). If the build fails, stop and report the error.

3. **Capture the current symlink target** so revert is reliable even if the user later forgets:
   ```
   readlink ~/Library/pnpm/orchard
   ```
   Save it to memory (state in your response) — typical value is `/Users/USER/workspace/orchardist/target/release/orchard`.

4. **Repoint the symlink** to the worktree's binary:
   ```
   ln -sf <worktree-abs-path>/target/release/orchard ~/Library/pnpm/orchard
   ```

5. **Verify** with `ls -la ~/Library/pnpm/orchard` and confirm the new target.

6. **Tell the user to test.** Suggest specific things to verify based on the PR's changes (e.g., "scroll the preview pane", "open with 20+ worktrees"). Remind them the symlink is repointed and `orchard` in any shell now runs the worktree binary.

7. **Wait for confirmation.** Do not auto-revert. The user will say "looks good" / "merge it" / "revert" / "broken".

## Revert Mode

Triggered by `revert` argument, or after the user confirms testing is done (success or failure):

1. **Restore the symlink** to the main checkout:
   ```
   ln -sf /Users/USER/workspace/orchardist/target/release/orchard ~/Library/pnpm/orchard
   ```

2. **Verify** with `ls -la ~/Library/pnpm/orchard`.

3. **Confirm** to the user the symlink is restored. If main's `target/release/orchard` is stale (e.g., the user has been working in worktrees for a while), mention that they may want to `cargo build --release` from main to refresh it.

## Notes

- Never modify the binaries themselves — only the symlink.
- The repoint persists across shells until reverted, so always revert before walking away.
- If multiple worktrees are being tested in sequence, repoint between them rather than reverting in the middle.
