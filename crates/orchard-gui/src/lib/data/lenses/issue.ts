/**
 * Issue lens — anchor: GitHub issues that we're actively working on.
 * A worktree appears iff its PR is OPEN/DRAFT and the daemon has joined
 * it to an issue. The issue is the row; worktree + PR + Claude session
 * are enrichment.
 *
 * Daemon owns the joins: Worktree.claudeInstances brings the sessions
 * for each worktree; ClaudeInstance.conversation brings agentName/
 * customTitle. No cwd→worktree matching on the client.
 */
import { IssueLensStore, type IssueLens$result } from "$houdini";
import { parseTime } from "./client";
import type { SessionCardT, WorktreeEnrichment } from "./fragments";
import type { SidebarItem, SidebarSection } from "$lib/data/sidebar-item";
import { buildSidebarItem } from "$lib/data/sidebar-item";

/** Singleton store for the issue lens. */
export const issueStore = new IssueLensStore();

type Data = NonNullable<IssueLens$result>;

/**
 * Project into sectioned `SidebarItem[]` per #540 B0/B1. One section
 * per issue. Worktrees with no Claude session attached are dropped at
 * projection time (the unified item model requires a session).
 */
export function buildIssueSections(
	data: Data | null | undefined,
): SidebarSection[] {
	if (!data) return [];
	const sections = new Map<number, SidebarSection>();
	for (const repo of data.workView.repos) {
		for (const w of repo.worktrees as unknown as Array<
			WorktreeEnrichment & { claudeInstances?: SessionCardT[] | null }
		>) {
			if (!w.issue) continue;
			if (!w.pr) continue;
			const prState = w.pr.state.toUpperCase();
			if (prState !== "OPEN" && prState !== "DRAFT") continue;
			const sessions = (w.claudeInstances ?? []) as SessionCardT[];
			if (sessions.length === 0) continue;

			let sec = sections.get(w.issue.number);
			if (!sec) {
				const label =
					w.issue.title != null
						? `#${w.issue.number} · ${w.issue.title}`
						: `#${w.issue.number}`;
				sec = { id: `issue-${w.issue.number}`, label, items: [] };
				sections.set(w.issue.number, sec);
			}
			for (const s of sessions) {
				const conv = s.conversation;
				const lastActivityMs = parseTime(conv?.lastSeenAt) || parseTime(s.lastActivityAt);
				const hints = conv
					? { agentName: conv.agentName ?? null, customTitle: conv.customTitle ?? null }
					: null;
				sec.items.push(buildSidebarItem(s, w, lastActivityMs, [], hints));
			}
		}
	}
	return Array.from(sections.values());
}
