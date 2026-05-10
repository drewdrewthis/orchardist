/**
 * Recent-activity lens — anchor: claudeInstances. Sort: lastActivityMs desc.
 *
 * Worktree + agentName/customTitle come from the daemon's join
 * (ClaudeInstance.worktree, ClaudeInstance.conversation). The `conversations`
 * top-level still surfaces the messageCount / open / recap fields the
 * lens uses for its meta column.
 */
import { RecentLensStore, type RecentLens$result } from "$houdini";
import { parseTime } from "./client";
import type { SessionCardT, WorktreeEnrichment } from "./fragments";
import type { SidebarItem } from "$lib/data/sidebar-item";
import { buildSidebarItem } from "$lib/data/sidebar-item";

/** Singleton store for the recent lens. */
export const recentStore = new RecentLensStore();

type Data = NonNullable<RecentLens$result>;

/**
 * Project the daemon's pre-joined data into a flat, time-sorted list of
 * `SidebarItem[]`. Pure — call inside `$derived` against
 * `$recentStore.data`.
 */
export function buildRecentItems(
	data: Data | null | undefined,
): SidebarItem[] {
	if (!data) return [];
	const sessions = data.claudeInstances as unknown as SessionCardT[];
	const items = sessions.map((session) => {
		const worktree = (session.worktree ?? null) as WorktreeEnrichment | null;
		const conv = session.conversation;
		const lastActivityMs = parseTime(conv?.lastSeenAt) || parseTime(session.lastActivityAt);
		const hints = conv ? { agentName: conv.agentName ?? null, customTitle: conv.customTitle ?? null } : null;
		return { session, worktree, lastActivityMs, hints };
	});
	items.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
	return items.map((r) =>
		buildSidebarItem(r.session, r.worktree, r.lastActivityMs, [], r.hints),
	);
}
