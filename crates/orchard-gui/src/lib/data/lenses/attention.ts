/**
 * Needs-attention lens — anchors on every worktree (dormant or not).
 * Drew (2026-05-10): "all of the worktrees should always show up on
 * all views — needs attention is doesn't need an active convo." Four
 * tiers, ordered for triage:
 *   1. Blocked — PR has CI failing, changes-requested review,
 *                merge conflict, or branch-protection block.
 *   2. Waiting — live session AND idle > 5min.
 *   3. Active  — live session, recently active. FYI.
 *   4. Quiet   — no PR signal, no live session. Just so you can see
 *                the worktree exists and act on it.
 *
 * Each worktree contributes at least one row: a row per live
 * claudeInstance, OR a single dormant row if no instance is attached.
 */
import { AttentionLensStore, type AttentionLens$result } from "$houdini";
import { parseTime } from "./client";
import type { SessionCardT, WorktreeEnrichment } from "./fragments";
import type { SidebarItem, SidebarSection } from "$lib/data/sidebar-item";
import { buildSidebarItem, buildDormantWorktreeItem } from "$lib/data/sidebar-item";

/** Singleton store for the attention lens. */
export const attentionStore = new AttentionLensStore();

export type AttentionTier = "blocked" | "waiting" | "active" | "quiet";

type Data = NonNullable<AttentionLens$result>;

const FIVE_MIN_MS = 5 * 60_000;

/**
 * Compute PR-driven blocking signals. Returns the tier-deciding reasons —
 * the row component re-derives the user-facing chips from the PR fields
 * directly (single source of truth), so this function is the tiering
 * input only, not a duplicate emission.
 */
function prBlockingReasons(worktree: WorktreeEnrichment | null): string[] {
	if (!worktree?.pr) return [];
	const pr = worktree.pr;
	const reasons: string[] = [];
	if (pr.statusCheckRollup === "FAILURE" || pr.statusCheckRollup === "ERROR")
		reasons.push("CI failing");
	if (pr.reviewDecision === "CHANGES_REQUESTED")
		reasons.push("changes requested");
	if (pr.mergeable === "CONFLICTING" || pr.mergeStateStatus === "DIRTY")
		reasons.push("merge conflict");
	else if (pr.mergeStateStatus === "BLOCKED") reasons.push("merge blocked");
	return reasons;
}

/**
 * Tier a row anchored on a single live ClaudeInstance.
 */
function classifyLive(
	session: SessionCardT,
	worktree: WorktreeEnrichment | null,
	lastActivityMs: number,
	now: number,
): { tier: AttentionTier; reasons: string[] } {
	const blockReasons = prBlockingReasons(worktree);
	if (blockReasons.length > 0) return { tier: "blocked", reasons: blockReasons };

	if (lastActivityMs > 0 && now - lastActivityMs > FIVE_MIN_MS) {
		const minutes = Math.floor((now - lastActivityMs) / 60_000);
		return { tier: "waiting", reasons: [`idle ${minutes}m`] };
	}
	return { tier: "active", reasons: [] };
}

/**
 * Project every worktree (with or without sessions) into the right
 * triage tier. Pure — call inside `$derived` against
 * `$attentionStore.data`.
 */
export function buildAttentionSections(
	data: Data | null | undefined,
	now: number,
): SidebarSection[] {
	const sections: Record<AttentionTier, SidebarSection> = {
		blocked: { id: "blocked", label: "Blocked", items: [] },
		waiting: { id: "waiting", label: "Waiting", items: [] },
		active: { id: "active", label: "Active", items: [] },
		quiet: { id: "quiet", label: "Quiet", items: [] },
	};
	const order: AttentionTier[] = ["blocked", "waiting", "active", "quiet"];
	if (!data) return order.map((t) => sections[t]);

	type Row = {
		item: SidebarItem;
		tier: AttentionTier;
		lastActivityMs: number;
	};
	const rows: Row[] = [];

	for (const repo of data.workView.repos) {
		for (const w of repo.worktrees as unknown as Array<
			WorktreeEnrichment & { claudeInstances?: SessionCardT[] | null }
		>) {
			const sessions = (w.claudeInstances ?? []) as SessionCardT[];

			if (sessions.length === 0) {
				// Dormant worktree. PR signal still tiers it into Blocked
				// when actionable — otherwise Quiet, but always visible.
				// The row derives PR chips from worktree.pr directly; we
				// don't re-emit them as `reasons` here.
				const blockReasons = prBlockingReasons(w);
				const item = buildDormantWorktreeItem(w);
				rows.push({
					item,
					tier: blockReasons.length > 0 ? "blocked" : "quiet",
					lastActivityMs: 0,
				});
				continue;
			}

			for (const s of sessions) {
				const conv = s.conversation;
				const lastActivityMs =
					parseTime(conv?.lastSeenAt) || parseTime(s.lastActivityAt);
				const { tier, reasons } = classifyLive(s, w, lastActivityMs, now);
				// `reasons` from classifyLive carries only the non-PR rationale
				// (e.g. "idle 12m" for the waiting tier). PR signals are derived
				// in the row component from worktree.pr fields — no duplication.
				const lensSpecific = reasons.filter(
					(r) => !r.startsWith("CI") && r !== "changes requested" && r !== "merge conflict" && r !== "merge blocked",
				);
				const hints = conv
					? { agentName: conv.agentName ?? null, customTitle: conv.customTitle ?? null }
					: null;
				const item = buildSidebarItem(s, w, lastActivityMs, lensSpecific, hints);
				rows.push({ item, tier, lastActivityMs });
			}
		}
	}

	// Sort each tier by activity desc; dormant rows naturally sink (ms=0).
	rows.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
	// Defensive dedup — the daemon can attach one ClaudeInstance to multiple
	// repos when a session's cwd matches more than one (langwatch-saas vs
	// langwatch/langwatch share parent dirs). Keep first occurrence.
	const seen = new Set<string>();
	for (const r of rows) {
		if (seen.has(r.item.id)) continue;
		seen.add(r.item.id);
		sections[r.tier].items.push(r.item);
	}
	return order.map((t) => sections[t]);
}
