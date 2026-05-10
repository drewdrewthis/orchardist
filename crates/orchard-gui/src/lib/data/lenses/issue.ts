/**
 * Issue lens — anchor: GitHub issues that we're actively working on,
 * filtered through worktrees with open PRs.
 *
 * Rule: an issue appears iff some worktree exists on a branch whose PR
 * is OPEN/DRAFT and the daemon has joined that worktree to the issue
 * (`worktree.issue != null`). The issue is the row; worktree + PR +
 * Claude session are enrichment.
 *
 * Houdini operation lives at `lenses/houdini/IssueLens.gql`. This file
 * exposes the singleton store + `buildIssueRows`.
 */
import { IssueLensStore, type IssueLens$result } from "$houdini";
import { parseTime } from "./client";
import type { SessionCardT, WorktreeEnrichment } from "./fragments";

/** Singleton Houdini store for the issue lens. */
export const issueStore = new IssueLensStore();

type Data = NonNullable<IssueLens$result>;

export interface IssueRow {
	issue: { number: number; state: string; title: string | null };
	worktree: WorktreeEnrichment;
	session: SessionCardT | null;
	lastActivityMs: number;
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

/**
 * Project the Houdini result into ordered issue rows. Pure —
 * components call this inside `$derived` against `$issueStore.data`.
 */
export function buildIssueRows(data: Data | null | undefined): IssueRow[] {
	if (!data) return [];
	const allWorktrees = data.workView.projects.flatMap(
		(p) => p.worktrees as unknown as WorktreeEnrichment[],
	);
	const sessions = data.claudeInstances as unknown as SessionCardT[];
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
		const { session, lastActivityMs } = findSessionFor(w, sessions, lastByUuid);
		rows.push({ issue: w.issue, worktree: w, session, lastActivityMs });
	}
	rows.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
	return rows;
}

