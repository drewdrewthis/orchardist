/**
 * Panel-side query — given a row's identity (paneId and/or sessionUuid),
 * fetch everything the open panel needs to render: pane breadcrumb,
 * claude session, conversation transcript metadata, worktree+pr+issue
 * enrichment.
 *
 * The panel runs this query itself so it owns its data shape; the
 * sidebar only emits row identity. Tabs hold {paneId, sessionUuid};
 * the panel resolves whatever it needs from the daemon.
 */
import { gql } from "graphql-request";
import { http, parseTime } from "./client";
import {
	PANE_CARD_FRAGMENT,
	SESSION_CARD_FRAGMENT,
	WORKTREE_ENRICHMENT_FRAGMENT,
	type PaneCardT,
	type SessionCardT,
	type WorktreeEnrichment,
} from "./fragments";

const PANEL_QUERY = gql`
	${PANE_CARD_FRAGMENT}
	${SESSION_CARD_FRAGMENT}
	${WORKTREE_ENRICHMENT_FRAGMENT}
	query OpenPanel($paneIds: [String!]) {
		tmuxPanes(filter: { paneIdIn: $paneIds }) {
			...PaneCard
		}
		claudeInstances {
			...SessionCard
		}
		conversations {
			sessionUuid
			lastSeenAt
			firstSeenAt
			messageCount
			open
			recap
			cwd
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

export interface PanelData {
	pane: PaneCardT | null;
	session: SessionCardT | null;
	conversation: {
		sessionUuid: string;
		lastSeenAt: number;
		firstSeenAt: number;
		messageCount: number;
		open: boolean;
		recap: string | null;
		cwd: string | null;
	} | null;
	worktree: WorktreeEnrichment | null;
}

interface PanelResponse {
	tmuxPanes: PaneCardT[];
	claudeInstances: SessionCardT[];
	conversations: Array<{
		sessionUuid: string;
		lastSeenAt: string | null;
		firstSeenAt: string | null;
		messageCount: number;
		open: boolean;
		recap: string | null;
		cwd: string | null;
	}>;
	workView: {
		projects: Array<{ id: string; name: string; worktrees: WorktreeEnrichment[] }>;
	};
}

/**
 * Resolve a row identity into the full panel data. Either paneId or
 * sessionUuid must be supplied; both is fine and provides the most
 * specific match.
 */
export async function fetchPanel(args: {
	paneId?: string | null;
	sessionUuid?: string | null;
}): Promise<PanelData | null> {
	const { paneId, sessionUuid } = args;
	if (!paneId && !sessionUuid) return null;
	try {
		const data = await http().request<PanelResponse>(PANEL_QUERY, {
			paneIds: paneId ? [paneId] : null,
		});

		const pane = data.tmuxPanes[0] || null;

		// Pick the claude instance:
		//   - exact match on sessionUuid when supplied
		//   - else the instance attached to this pane (paneId match)
		let session: SessionCardT | null = null;
		if (sessionUuid) {
			session = data.claudeInstances.find((s) => s.sessionUuid === sessionUuid) || null;
		}
		if (!session && pane?.claudeInstance) {
			session =
				data.claudeInstances.find((s) => s.id === pane.claudeInstance!.id) || null;
		}
		if (!session && paneId) {
			session = data.claudeInstances.find((s) => s.pane?.paneId === paneId) || null;
		}

		// Conversation = the JSONL transcript matching session.sessionUuid.
		const targetUuid = session?.sessionUuid || sessionUuid || null;
		const convRaw = targetUuid
			? data.conversations.find((c) => c.sessionUuid === targetUuid)
			: null;
		const conversation = convRaw
			? {
					sessionUuid: convRaw.sessionUuid,
					lastSeenAt: parseTime(convRaw.lastSeenAt),
					firstSeenAt: parseTime(convRaw.firstSeenAt),
					messageCount: convRaw.messageCount,
					open: convRaw.open,
					recap: convRaw.recap,
					cwd: convRaw.cwd,
				}
			: null;

		// Worktree = deepest worktree path that contains the row's cwd.
		// Cwd source priority: pane.process.cwd > session.process.cwd > conversation.cwd.
		const cwd =
			pane?.process?.cwd || session?.process?.cwd || conversation?.cwd || null;
		let worktree: WorktreeEnrichment | null = null;
		if (cwd) {
			let best: WorktreeEnrichment | null = null;
			for (const p of data.workView.projects) {
				for (const w of p.worktrees) {
					if (cwd === w.path || cwd.startsWith(w.path + "/")) {
						if (!best || w.path.length > best.path.length) best = w;
					}
				}
			}
			worktree = best;
		}

		return { pane, session, conversation, worktree };
	} catch (err) {
		console.warn("[orchard-gui] panel fetch failed:", err);
		return null;
	}
}
