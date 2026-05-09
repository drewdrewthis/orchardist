/**
 * Needs-attention lens — anchor: claudeInstances, plus worktree
 * enrichment so we can derive PR/issue/CI signals.
 *
 * Three tiers:
 *   1. Blocked — PR has CI failing, or changes-requested review, or merge conflict.
 *   2. Waiting — Claude session idle > 5min (no recent activity).
 *   3. Active — currently working sessions; FYI only.
 *
 * Granular Claude states beyond "working" / "no_claude" are a daemon
 * gap (filed); when they exist we'll layer "asking question" /
 * "awaiting input" into tier 2.
 */
import { gql } from "graphql-request";
import { http, parseTime } from "./client";
import {
	SESSION_CARD_FRAGMENT,
	WORKTREE_ENRICHMENT_FRAGMENT,
	type SessionCardT,
	type WorktreeEnrichment,
} from "./fragments";

const ATTENTION_QUERY = gql`
	${SESSION_CARD_FRAGMENT}
	${WORKTREE_ENRICHMENT_FRAGMENT}
	query AttentionLens {
		claudeInstances {
			...SessionCard
		}
		conversations {
			sessionUuid
			lastSeenAt
		}
		workView {
			projects {
				id
				name
				worktrees {
					...WorktreeEnrichment
				}
			}
		}
	}
`;

export type AttentionTier = "blocked" | "waiting" | "active";

export interface AttentionRow {
	session: SessionCardT;
	worktree: WorktreeEnrichment | null;
	tier: AttentionTier;
	reasons: string[]; // human-readable, populated on tier=blocked / waiting
	lastActivityMs: number;
}

interface AttentionResponse {
	claudeInstances: SessionCardT[];
	conversations: Array<{ sessionUuid: string; lastSeenAt: string | null }>;
	workView: {
		projects: Array<{ id: string; name: string; worktrees: WorktreeEnrichment[] }>;
	};
}

const FIVE_MIN_MS = 5 * 60_000;

/**
 * Find the worktree whose path contains the session's process cwd.
 * Most-specific (deepest) match wins — necessary because nested
 * worktrees (e.g. `repo` and `repo/.worktrees/branch`) both share a
 * prefix.
 */
function matchWorktree(session: SessionCardT, worktrees: WorktreeEnrichment[]): WorktreeEnrichment | null {
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
): {
	tier: AttentionTier;
	reasons: string[];
} {
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

export async function fetchAttention(now: number = Date.now()): Promise<AttentionRow[]> {
	try {
		const data = await http().request<AttentionResponse>(ATTENTION_QUERY);
		const allWorktrees: WorktreeEnrichment[] = data.workView.projects.flatMap((p) => p.worktrees);
		const lastByUuid = new Map<string, number>();
		for (const c of data.conversations) {
			const t = parseTime(c.lastSeenAt);
			if (t > 0) lastByUuid.set(c.sessionUuid, t);
		}
		const rows = data.claudeInstances.map((session): AttentionRow => {
			const worktree = matchWorktree(session, allWorktrees);
			const lastActivityMs =
				lastByUuid.get(session.sessionUuid) ?? parseTime(session.lastActivityAt);
			const { tier, reasons } = classify(session, worktree, lastActivityMs, now);
			return {
				session,
				worktree,
				tier,
				reasons,
				lastActivityMs,
			};
		});
		// Sort: blocked > waiting > active, then most-recent activity first.
		const order: Record<AttentionTier, number> = { blocked: 0, waiting: 1, active: 2 };
		rows.sort((a, b) => {
			const t = order[a.tier] - order[b.tier];
			if (t !== 0) return t;
			return b.lastActivityMs - a.lastActivityMs;
		});
		return rows;
	} catch (err) {
		console.warn("[orchard-gui] attention lens fetch failed:", err);
		return [];
	}
}
