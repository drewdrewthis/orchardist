/**
 * Worktree lens — anchor: workView.repos[].worktrees[]. One section per
 * repo (label = "owner/repo"); within each, one item per tmux pane
 * attached to a worktree in that repo.
 *
 * Iterates Worktree.tmuxPanes (not claudeInstances). Each pane gets a
 * row: panes with a Claude instance use the full SessionCard shape;
 * panes without use a tmux-only row (state="no_claude"). Worktrees with
 * zero panes still surface as dormant rows so the user sees their
 * topology. Drew (2026-05-10): "I want a worktree lens to be back."
 */
import { WorktreeLensStore, type WorktreeLens$result } from "$houdini";
import { parseTime } from "./client";
import type { PaneCardT, SessionCardT, WorktreeEnrichment } from "./fragments";
import type { SidebarItem, SidebarSection } from "$lib/data/sidebar-item";
import {
	buildSidebarItem,
	buildDormantWorktreeItem,
	buildTmuxOnlyPaneItem,
} from "$lib/data/sidebar-item";

/** Singleton store for the worktree lens. */
export const worktreeStore = new WorktreeLensStore();

type Data = NonNullable<WorktreeLens$result>;

/**
 * Project into one section per repo, items sorted by activity desc so
 * the busiest worktrees float to the top.
 *
 * Iteration anchor is Worktree.tmuxPanes — one sidebar item per pane.
 * Panes with a Claude instance render the full Claude row; panes without
 * render a tmux-only row. Worktrees with no panes render a dormant row.
 */
export function buildWorktreeSections(
	data: Data | null | undefined,
): SidebarSection[] {
	if (!data) return [];
	const sections: SidebarSection[] = [];
	for (const repo of data.workView.repos) {
		const items: SidebarItem[] = [];
		for (const w of repo.worktrees as unknown as Array<
			WorktreeEnrichment & { tmuxPanes?: PaneCardT[] | null }
		>) {
			const panes = (w.tmuxPanes ?? []) as PaneCardT[];
			if (panes.length === 0) {
				// Dormant worktree — no live tmux panes. Drew (2026-05-10):
				// "if there is a live worktree, that means it should be visible."
				// Render a row anyway so the user can see + act on it (open a
				// session, destroy, etc.). Branch / PR / issue chips still
				// surface from the worktree enrichment.
				items.push(buildDormantWorktreeItem(w));
				continue;
			}
			for (const pane of panes) {
				const ci = pane.claudeInstance as SessionCardT | null;
				if (ci) {
					// Pane has a Claude session — build the full enriched row.
					const worktree = (ci.worktree ?? w) as WorktreeEnrichment;
					const conv = ci.conversation;
					const lastActivityMs =
						parseTime(conv?.lastSeenAt) || parseTime(ci.lastActivityAt);
					const hints = conv
						? { agentName: conv.agentName ?? null, customTitle: conv.customTitle ?? null }
						: null;
					// Key the item by paneId (not by ci.id) so that two panes sharing
					// the same ClaudeInstance (edge case) each get their own row.
					const item = buildSidebarItem(ci, worktree, lastActivityMs, [], hints);
					items.push({ ...item, id: `pane:${pane.paneId}` });
				} else {
					// Pane has no Claude session — tmux-only row keyed by paneId.
					const paneWorktree = (pane.process?.worktree ?? w) as WorktreeEnrichment;
					const lastActivityMs =
						parseTime(pane.window?.session?.lastActivityAt) ?? 0;
					items.push(buildTmuxOnlyPaneItem(pane, paneWorktree, lastActivityMs));
				}
			}
		}
		// Dormant rows have lastActivityMs=0 and naturally sink to the bottom
		// of the activity-desc sort.
		items.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
		sections.push({
			id: `repo-${repo.id}`,
			label: repo.slug,
			items,
		});
	}
	return sections;
}
