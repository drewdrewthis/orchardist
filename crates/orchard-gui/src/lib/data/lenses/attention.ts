/**
 * Needs-attention lens — anchor: claudeInstances. Three tiers:
 *   1. Blocked  — PR has CI failing, or changes-requested review, or merge conflict.
 *   2. Waiting  — Claude session idle > 5min (no recent activity).
 *   3. Active   — currently working sessions; FYI only.
 *
 * Drew (2026-05-10): joins live on the daemon. ClaudeInstance.worktree
 * and ClaudeInstance.conversation are server-resolved; this projection
 * consumes them directly — no cwd→worktree matching, no uuid map glue.
 */
import { AttentionLensStore, type AttentionLens$result } from "$houdini";
import { parseTime } from "./client";
import type { SessionCardT, WorktreeEnrichment } from "./fragments";
import type { SidebarItem, SidebarSection } from "$lib/data/sidebar-item";
import { buildSidebarItem } from "$lib/data/sidebar-item";

/** Singleton store for the attention lens. */
export const attentionStore = new AttentionLensStore();

export type AttentionTier = "blocked" | "waiting" | "active";

type Data = NonNullable<AttentionLens$result>;

const FIVE_MIN_MS = 5 * 60_000;

function classify(
	session: SessionCardT,
	worktree: WorktreeEnrichment | null,
	lastActivityMs: number,
	now: number,
): { tier: AttentionTier; reasons: string[] } {
	const reasons: string[] = [];

	if (worktree?.pr) {
		const pr = worktree.pr;
		if (pr.statusCheckRollup === "FAILURE") reasons.push("CI failing");
		if (pr.reviewDecision === "CHANGES_REQUESTED") reasons.push("changes requested");
		if (pr.mergeable === "CONFLICTING" || pr.mergeStateStatus === "DIRTY")
			reasons.push("merge conflict");
		if (reasons.length > 0) return { tier: "blocked", reasons };
	}

	if (session.state !== "no_claude" && lastActivityMs > 0 && now - lastActivityMs > FIVE_MIN_MS) {
		const minutes = Math.floor((now - lastActivityMs) / 60_000);
		reasons.push(`idle ${minutes}m`);
		return { tier: "waiting", reasons };
	}

	return { tier: "active", reasons: [] };
}

/**
 * Project the daemon's pre-joined data into ordered tier sections.
 * Pure — call inside `$derived` against `$attentionStore.data`.
 */
export function buildAttentionSections(
	data: Data | null | undefined,
	now: number,
): SidebarSection[] {
	const sections: Record<AttentionTier, SidebarSection> = {
		blocked: { id: "blocked", label: "Blocked", items: [] },
		waiting: { id: "waiting", label: "Waiting", items: [] },
		active: { id: "active", label: "Active", items: [] },
	};
	if (!data) return [sections.blocked, sections.waiting, sections.active];

	const sessions = (data.claudeInstances as unknown as SessionCardT[]).slice();
	const rows = sessions.map((session) => {
		const worktree = (session.worktree ?? null) as WorktreeEnrichment | null;
		const conv = session.conversation;
		const lastActivityMs = parseTime(conv?.lastSeenAt) || parseTime(session.lastActivityAt);
		const { tier, reasons } = classify(session, worktree, lastActivityMs, now);
		const hints = conv ? { agentName: conv.agentName ?? null, customTitle: conv.customTitle ?? null } : null;
		return { session, worktree, lastActivityMs, tier, reasons, hints };
	});
	rows.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
	for (const r of rows) {
		sections[r.tier].items.push(
			buildSidebarItem(r.session, r.worktree, r.lastActivityMs, r.reasons, r.hints),
		);
	}
	return [sections.blocked, sections.waiting, sections.active];
}
