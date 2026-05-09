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
	workView: {
		projects: Array<{ id: string; name: string; worktrees: WorktreeEnrichment[] }>;
	};
}

function findSessionFor(worktree: WorktreeEnrichment, sessions: SessionCardT[]): SessionCardT | null {
	let best: SessionCardT | null = null;
	for (const s of sessions) {
		const cwd = s.process?.cwd;
		if (!cwd) continue;
		if (cwd === worktree.path || cwd.startsWith(worktree.path + "/")) {
			// Pick the most-recently-active session in this worktree.
			if (!best || parseTime(s.lastActivityAt) > parseTime(best.lastActivityAt)) {
				best = s;
			}
		}
	}
	return best;
}

export async function fetchIssues(): Promise<IssueRow[]> {
	try {
		const data = await http().request<IssueResponse>(ISSUE_QUERY);
		const allWorktrees: WorktreeEnrichment[] = data.workView.projects.flatMap((p) => p.worktrees);
		const rows: IssueRow[] = [];
		for (const w of allWorktrees) {
			if (!w.issue) continue;
			if (!w.pr) continue;
			const prState = w.pr.state.toUpperCase();
			if (prState !== "OPEN" && prState !== "DRAFT") continue;
			const session = findSessionFor(w, data.claudeInstances);
			rows.push({
				issue: w.issue,
				worktree: w,
				session,
				lastActivityMs: parseTime(session?.lastActivityAt),
			});
		}
		rows.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
		return rows;
	} catch (err) {
		console.warn("[orchard-gui] issue lens fetch failed:", err);
		return [];
	}
}
