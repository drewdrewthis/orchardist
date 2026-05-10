/**
 * Needs-attention lens — anchor: claudeInstances, plus worktree
 * enrichment so we can derive PR/issue/CI signals.
 *
 * Three tiers:
 *   1. Blocked — PR has CI failing, or changes-requested review, or merge conflict.
 *   2. Waiting — Claude session idle > 5min (no recent activity).
 *   3. Active — currently working sessions; FYI only.
 *
 * The Houdini operation lives at `lenses/houdini/AttentionLens.gql`;
 * its generated `AttentionLensStore` owns the fetch + the normalized
 * cache. This file exposes:
 *   - `attentionStore` — the singleton store consumers subscribe to
 *   - `buildAttentionRows(data, now)` — pure projection from the
 *     Houdini result into ordered tier rows.
 *
 * Granular Claude states beyond "working" / "no_claude" are a daemon
 * gap; when they exist we'll layer "asking question" / "awaiting
 * input" into tier 2.
 */
import { AttentionLensStore, type AttentionLens$result } from "$houdini";
import { parseTime } from "./client";
import type { SessionCardT, WorktreeEnrichment } from "./fragments";

/**
 * Singleton store for the attention lens. Houdini's normalized cache
 * means concurrent subscribers share one snapshot — instantiating
 * once at module scope is the right shape.
 */
export const attentionStore = new AttentionLensStore();

export type AttentionTier = "blocked" | "waiting" | "active";

type Data = NonNullable<AttentionLens$result>;

export interface AttentionRow {
	session: SessionCardT;
	worktree: WorktreeEnrichment | null;
	tier: AttentionTier;
	reasons: string[];
	lastActivityMs: number;
}

const FIVE_MIN_MS = 5 * 60_000;

/**
 * Find the worktree whose path contains the session's process cwd.
 * Most-specific (deepest) match wins — necessary because nested
 * worktrees (e.g. `repo` and `repo/.worktrees/branch`) both share a
 * prefix.
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

function classify(
	session: SessionCardT,
	worktree: WorktreeEnrichment | null,
	lastActivityMs: number,
	now: number,
): { tier: AttentionTier; reasons: string[] } {
	const reasons: string[] = [];

	// Tier 1 — blocked. PR signal only fires when a PR actually exists.
	if (worktree?.pr) {
		const pr = worktree.pr;
		if (pr.statusCheckRollup === "FAILURE") reasons.push("CI failing");
		if (pr.reviewDecision === "CHANGES_REQUESTED") reasons.push("changes requested");
		// mergeStateStatus values that mean "won't merge cleanly":
		// CONFLICTING, DIRTY, UNKNOWN-but-mergeable=CONFLICTING.
		if (pr.mergeable === "CONFLICTING" || pr.mergeStateStatus === "DIRTY")
			reasons.push("merge conflict");
		if (reasons.length > 0) return { tier: "blocked", reasons };
	}

	// Tier 2 — waiting. Use the resolved lastActivityMs (jsonl-derived
	// when available); brand-new sessions with no activity yet shouldn't
	// count as idle.
	if (session.state !== "no_claude" && lastActivityMs > 0 && now - lastActivityMs > FIVE_MIN_MS) {
		const minutes = Math.floor((now - lastActivityMs) / 60_000);
		reasons.push(`idle ${minutes}m`);
		return { tier: "waiting", reasons };
	}

	// Tier 3 — active.
	return { tier: "active", reasons: [] };
}

/**
 * Project the Houdini result into ordered tier rows. Pure — components
 * call this inside `$derived` against `$attentionStore.data` so the
 * cache+network policy drives reactivity for free.
 */
export function buildAttentionRows(
	data: Data | null | undefined,
	now: number,
): AttentionRow[] {
	if (!data) return [];
	// Houdini-typed nodes are structurally compatible with the
	// hand-written `SessionCardT` / `WorktreeEnrichment` shapes — both
	// come from the same schema. The cast is a type bridge for Phase 2;
	// Phase 3 retires the hand-written interfaces.
	const allWorktrees = data.workView.repos.flatMap(
		(r) => r.worktrees as unknown as WorktreeEnrichment[],
	);
	const lastByUuid = new Map<string, number>();
	for (const c of data.conversations) {
		const t = parseTime(c.lastSeenAt);
		if (t > 0) lastByUuid.set(c.sessionUuid, t);
	}
	const rows = (data.claudeInstances as unknown as SessionCardT[]).map((session): AttentionRow => {
		const worktree = matchWorktree(session, allWorktrees);
		const lastActivityMs =
			lastByUuid.get(session.sessionUuid) ?? parseTime(session.lastActivityAt);
		const { tier, reasons } = classify(session, worktree, lastActivityMs, now);
		return { session, worktree, tier, reasons, lastActivityMs };
	});
	// Sort: blocked > waiting > active, then most-recent activity first.
	const order: Record<AttentionTier, number> = { blocked: 0, waiting: 1, active: 2 };
	rows.sort((a, b) => {
		const t = order[a.tier] - order[b.tier];
		if (t !== 0) return t;
		return b.lastActivityMs - a.lastActivityMs;
	});
	return rows;
}
