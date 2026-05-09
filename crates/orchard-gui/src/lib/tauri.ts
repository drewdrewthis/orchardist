/**
 * Tauri command bridges to `worktree-core` (Layer 1 of research/037).
 *
 * These are *stateless system ops* — no daemon required, no broadcast.
 * The CLI binaries call `worktree-core` directly; the GUI calls it through
 * these Tauri-invoke proxies.
 *
 * Stateful ops (chat send, contract update, cross-host transfer) live in
 * `$lib/data/chat.ts` (chat-core via Tauri) or `$lib/data/daemon.ts`
 * (daemon GraphQL) — not here.
 */

import { invoke } from "@tauri-apps/api/core";

export interface WorktreeRow {
	path: string;
	branch: string | null;
	head: string;
	is_bare: boolean;
	is_main: boolean;
	has_conflicts: boolean;
}

export async function listWorktrees(): Promise<WorktreeRow[]> {
	return await invoke<WorktreeRow[]>("list_worktrees");
}

export async function createWorktree(
	repoRoot: string,
	worktreePath: string,
	branch: string,
): Promise<"new" | "existing"> {
	return await invoke<"new" | "existing">("create_worktree", {
		repoRoot,
		worktreePath,
		branch,
	});
}

export async function removeWorktree(repoRoot: string, worktreePath: string): Promise<void> {
	await invoke("remove_worktree", { repoRoot, worktreePath });
}

export async function pruneWorktrees(repoRoot: string): Promise<void> {
	await invoke("prune_worktrees", { repoRoot });
}
