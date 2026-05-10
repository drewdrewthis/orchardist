/**
 * Recent-activity lens — anchor: claudeInstances, enriched with the
 * conversation transcript's lastSeenAt (the daemon's
 * ClaudeInstance.lastActivityAt is currently always null; the same
 * sessionUuid appears in `conversations` with the JSONL timestamp).
 * Sort: lastActivityMs desc.
 *
 * Houdini operation lives at `lenses/houdini/RecentLens.gql`. This
 * file exposes the singleton store + the `buildRecentRows` projection.
 */
import { RecentLensStore, type RecentLens$result } from "$houdini";
import { parseTime } from "./client";
import type { SessionCardT, WorktreeEnrichment } from "./fragments";
import type { SidebarItem } from "$lib/data/sidebar-item";
import { buildSidebarItem } from "$lib/data/sidebar-item";

/** Singleton Houdini store for the recent lens. */
export const recentStore = new RecentLensStore();

type Data = NonNullable<RecentLens$result>;

export interface RecentRow {
	session: SessionCardT;
	worktree: WorktreeEnrichment | null;
	/** Best-known activity timestamp: jsonl lastSeenAt > daemon lastActivityAt > 0. */
	lastActivityMs: number;
	messageCount: number;
	open: boolean;
	recap: string | null;
	hints: { agentName: string | null; customTitle: string | null } | null;
}

/**
 * Find the worktree whose path contains the session's process cwd.
 * Most-specific (deepest) match wins — necessary because nested
 * worktrees both share a prefix.
 */
function matchWorktree(
	session: SessionCardT,
	worktrees: WorktreeEnrichment[],
): WorktreeEnrichment | null {
	const cwd = session.process?.cwd;
	if (!cwd) return null;
	let best: WorktreeEnrichment | null = null;
	for (const w of worktrees) {
		if (cwd === w.path || cwd.startsWith(w.path + "/")) {
			if (!best || w.path.length > best.path.length) best = w;
		}
	}
	return best;
}

/**
 * Project the Houdini result into ordered recent rows. Pure —
 * components call this inside `$derived` against `$recentStore.data`.
 */
export function buildRecentRows(data: Data | null | undefined): RecentRow[] {
	if (!data) return [];
	const convByUuid = new Map<string, Data["conversations"][number]>();
	for (const c of data.conversations) convByUuid.set(c.sessionUuid, c);
	const allWorktrees = data.workView.repos.flatMap(
		(r) => r.worktrees as unknown as WorktreeEnrichment[],
	);
	return (data.claudeInstances as unknown as SessionCardT[])
		.map((s): RecentRow => {
			const conv = convByUuid.get(s.sessionUuid) || null;
			const fromConv = parseTime(conv?.lastSeenAt);
			const fromDaemon = parseTime(s.lastActivityAt);
			return {
				session: s,
				worktree: matchWorktree(s, allWorktrees),
				lastActivityMs: fromConv || fromDaemon,
				messageCount: conv?.messageCount ?? 0,
				open: conv?.open ?? false,
				recap: conv?.recap ?? null,
				hints: conv
					? { agentName: conv.agentName ?? null, customTitle: conv.customTitle ?? null }
					: null,
			};
		})
		.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
}

/**
 * Projection into `SidebarItem[]` per #540 B0/B1. Recent activity is a
 * flat lens — no grouping axis — so the caller renders it as a single
 * unsectioned list.
 */
export function buildRecentItems(
	data: Data | null | undefined,
): SidebarItem[] {
	const rows = buildRecentRows(data);
	return rows.map((r) =>
		buildSidebarItem(r.session, r.worktree, r.lastActivityMs, [], r.hints),
	);
}
