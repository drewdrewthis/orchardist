/**
 * Worktree lens — anchor: workView.repos[].worktrees[]. One section per
 * repo (label = "owner/repo"); within each, one item per Claude session
 * attached to a worktree in that repo.
 *
 * Daemon owns the join (Worktree.claudeInstances). Empty worktrees still
 * surface so the user sees their topology. Drew (2026-05-10): "I want a
 * worktree lens to be back."
 */
import { WorktreeLensStore, type WorktreeLens$result } from "$houdini";
import { parseTime } from "./client";
import type { SessionCardT, WorktreeEnrichment } from "./fragments";
import type { SidebarItem, SidebarSection } from "$lib/data/sidebar-item";
import { buildSidebarItem } from "$lib/data/sidebar-item";

/** Singleton store for the worktree lens. */
export const worktreeStore = new WorktreeLensStore();

type Data = NonNullable<WorktreeLens$result>;

/**
 * Project into one section per repo, items sorted by activity desc so
 * the busiest worktrees float to the top.
 */
export function buildWorktreeSections(
	data: Data | null | undefined,
): SidebarSection[] {
	if (!data) return [];
	const sections: SidebarSection[] = [];
	for (const repo of data.workView.repos) {
		const items: SidebarItem[] = [];
		for (const w of repo.worktrees as unknown as Array<
			WorktreeEnrichment & { claudeInstances?: SessionCardT[] | null }
		>) {
			const sessions = (w.claudeInstances ?? []) as SessionCardT[];
			for (const s of sessions) {
				const conv = s.conversation;
				const lastActivityMs = parseTime(conv?.lastSeenAt) || parseTime(s.lastActivityAt);
				const hints = conv
					? { agentName: conv.agentName ?? null, customTitle: conv.customTitle ?? null }
					: null;
				items.push(buildSidebarItem(s, w, lastActivityMs, [], hints));
			}
		}
		items.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
		sections.push({
			id: `repo-${repo.id}`,
			label: repo.slug,
			items,
		});
	}
	return sections;
}
