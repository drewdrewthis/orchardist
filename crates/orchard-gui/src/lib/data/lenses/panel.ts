/**
 * Panel-side query — given a row's identity (paneId and/or sessionUuid),
 * fetch everything the open panel needs: pane breadcrumb, claude
 * session, conversation transcript metadata, worktree+pr+issue
 * enrichment.
 *
 * The Houdini operation lives at `lenses/houdini/OpenPanel.gql`. Tabs
 * hold `{ paneId, sessionUuid }`; this file resolves whichever is
 * supplied to a single `PanelData` shape.
 *
 * One Houdini store instance per open panel — Svelte 5 components
 * pass it back and forth via `$openPanelStore.data`. Factory-spawned
 * rather than singleton so two open panels with different paneIds
 * don't fight over a shared store.
 */
import { OpenPanelStore, type OpenPanel$result } from "$houdini";
import { parseTime } from "./client";
import type { PaneCardT, SessionCardT, WorktreeEnrichment } from "./fragments";

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
		jsonlPath: string | null;
		agentName: string | null;
		customTitle: string | null;
	} | null;
	worktree: WorktreeEnrichment | null;
}

/** Spawn a fresh OpenPanel store. Each open panel gets its own. */
export function createPanelStore(): OpenPanelStore {
	return new OpenPanelStore();
}

type Data = NonNullable<OpenPanel$result>;

/**
 * Project the Houdini result into a single `PanelData` for the row
 * identity supplied. Pure — call inside `$derived` against
 * `$openPanelStore.data`.
 */
export function buildPanelData(
	data: Data | null | undefined,
	args: { paneId?: string | null; sessionUuid?: string | null },
): PanelData | null {
	if (!data) return null;
	const { paneId, sessionUuid } = args;
	if (!paneId && !sessionUuid) return null;

	const panes = data.tmuxPanes as unknown as PaneCardT[];
	const sessions = data.claudeInstances as unknown as SessionCardT[];
	const repos = data.workView.repos as unknown as Array<{
		worktrees: WorktreeEnrichment[];
	}>;

	const pane = panes[0] || null;

	// Pick the claude instance:
	//   - exact match on sessionUuid when supplied
	//   - else the instance attached to this pane (paneId match)
	let session: SessionCardT | null = null;
	if (sessionUuid) {
		session = sessions.find((s) => s.sessionUuid === sessionUuid) || null;
	}
	if (!session && pane?.claudeInstance) {
		session = sessions.find((s) => s.id === pane.claudeInstance!.id) || null;
	}
	if (!session && paneId) {
		session = sessions.find((s) => s.pane?.paneId === paneId) || null;
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
				jsonlPath: convRaw.jsonlPath,
				agentName: convRaw.agentName ?? null,
				customTitle: convRaw.customTitle ?? null,
			}
		: null;

	// Worktree = deepest worktree path that contains the row's cwd.
	// Cwd source priority: pane.process.cwd > session.process.cwd > conversation.cwd.
	const cwd = pane?.process?.cwd || session?.process?.cwd || conversation?.cwd || null;
	let worktree: WorktreeEnrichment | null = null;
	if (cwd) {
		let best: WorktreeEnrichment | null = null;
		for (const r of repos) {
			for (const w of r.worktrees) {
				if (cwd === w.path || cwd.startsWith(w.path + "/")) {
					if (!best || w.path.length > best.path.length) best = w;
				}
			}
		}
		worktree = best;
	}

	return { pane, session, conversation, worktree };
}
