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

/**
 * Tauri-only sentinel: throw a clear error in browser dev rather than
 * letting `invoke` blow up with `Cannot read properties of undefined`.
 * The GUI deliberately keeps these proxies thin — callers handle the
 * "browser dev, no Tauri" case at the call site.
 */
function inTauri(): boolean {
	return typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
}
function requireTauri(op: string): void {
	if (!inTauri()) {
		throw new Error(`${op} requires the desktop app — Tauri bridge not available in browser dev`);
	}
}

export interface WorktreeRow {
	path: string;
	branch: string | null;
	head: string;
	is_bare: boolean;
	is_main: boolean;
	has_conflicts: boolean;
}

export async function listWorktrees(): Promise<WorktreeRow[]> {
	requireTauri("listWorktrees");
	return await invoke<WorktreeRow[]>("list_worktrees");
}

export async function createWorktree(
	repoRoot: string,
	worktreePath: string,
	branch: string,
): Promise<"new" | "existing"> {
	requireTauri("createWorktree");
	return await invoke<"new" | "existing">("create_worktree", {
		repoRoot,
		worktreePath,
		branch,
	});
}

export async function removeWorktree(repoRoot: string, worktreePath: string): Promise<void> {
	requireTauri("removeWorktree");
	await invoke("remove_worktree", { repoRoot, worktreePath });
}

export async function pruneWorktrees(repoRoot: string): Promise<void> {
	requireTauri("pruneWorktrees");
	await invoke("prune_worktrees", { repoRoot });
}

/**
 * Type a chat message into a live tmux pane (the Claude REPL).
 * The pane id is what the daemon reports as `TmuxPane.paneId` (e.g. `%66`).
 */
export async function tmuxSendText(paneId: string, text: string): Promise<void> {
	requireTauri("tmuxSendText");
	await invoke("tmux_send_text", { paneId, text });
}
