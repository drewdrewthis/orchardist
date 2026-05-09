/**
 * Issue lens — anchor: GitHub issues that we're actively working on,
 * filtered through worktrees with open PRs.
 *
 * Rule: an issue appears iff some worktree exists on a branch whose PR
 * is OPEN/DRAFT and the daemon has joined that worktree to the issue
 * (`worktree.issue != null`). The issue is the row; worktree + PR +
 * Claude session are enrichment.
 */
import { gql } from "graphql-request";
import { http, parseTime } from "./client";
import {
	SESSION_CARD_FRAGMENT,
	WORKTREE_ENRICHMENT_FRAGMENT,
	type SessionCardT,
	type WorktreeEnrichment,
} from "./fragments";

const ISSUE_QUERY = gql`
	${SESSION_CARD_FRAGMENT}
	${WORKTREE_ENRICHMENT_FRAGMENT}
	query IssueLens {
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

export interface IssueRow {
	issue: { number: number; state: string; title: string | null };
	worktree: WorktreeEnrichment;
	session: SessionCardT | null;
	lastActivityMs: number;
}

interface IssueResponse {
	claudeInstances: SessionCardT[];
	conversations: Array<{ sessionUuid: string; lastSeenAt: string | null }>;
	workView: {
		projects: Array<{ id: string; name: string; worktrees: WorktreeEnrichment[] }>;
	};
}

function findSessionFor(
	worktree: WorktreeEnrichment,
	sessions: SessionCardT[],
	lastByUuid: Map<string, number>,
): { session: SessionCardT | null; lastActivityMs: number } {
	let best: SessionCardT | null = null;
	let bestMs = 0;
	for (const s of sessions) {
		const cwd = s.process?.cwd;
		if (!cwd) continue;
		if (cwd === worktree.path || cwd.startsWith(worktree.path + "/")) {
			const ms = lastByUuid.get(s.sessionUuid) ?? parseTime(s.lastActivityAt);
			if (ms > bestMs || best == null) {
				best = s;
				bestMs = ms;
			}
		}
	}
	return { session: best, lastActivityMs: bestMs };
}

export async function fetchIssues(): Promise<IssueRow[]> {
	try {
		const data = await http().request<IssueResponse>(ISSUE_QUERY);
		const allWorktrees: WorktreeEnrichment[] = data.workView.projects.flatMap((p) => p.worktrees);
		const lastByUuid = new Map<string, number>();
		for (const c of data.conversations) {
			const t = parseTime(c.lastSeenAt);
			if (t > 0) lastByUuid.set(c.sessionUuid, t);
		}
		const rows: IssueRow[] = [];
		for (const w of allWorktrees) {
			if (!w.issue) continue;
			if (!w.pr) continue;
			const prState = w.pr.state.toUpperCase();
			if (prState !== "OPEN" && prState !== "DRAFT") continue;
			const { session, lastActivityMs } = findSessionFor(w, data.claudeInstances, lastByUuid);
			rows.push({ issue: w.issue, worktree: w, session, lastActivityMs });
		}
		rows.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
		return rows;
	} catch (err) {
		console.warn("[orchard-gui] issue lens fetch failed:", err);
		return [];
	}
}
