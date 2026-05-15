/**
 * Panel-side query — given a row's identity (paneId and/or sessionUuid),
 * resolves to the pane breadcrumb, claude session, conversation, and
 * worktree (all daemon-joined).
 *
 * The Houdini operation lives at `lenses/houdini/OpenPanel.gql`. Tabs
 * hold `{ paneId, sessionUuid }`; this file resolves whichever is
 * supplied to a single `PanelData` shape.
 *
 * Worktree resolution: trust `session.worktree` (daemon-joined). The
 * conversation top-level is still queried because the panel surfaces
 * messageCount / open / recap / firstSeenAt + jsonlPath that the
 * SessionCard fragment doesn't carry.
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

	// Pick the pane that matches our row identity. When the caller has a
	// paneId, find it in the filtered result; otherwise stay null —
	// previously this took panes[0] which could be ANY pane (filter is
	// off when paneId is null), and the panel would mis-render the title.
	const pane = paneId
		? panes.find((p) => p.paneId === paneId) || null
		: null;

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

	// Worktree from the daemon's join — no client-side cwd matching.
	const worktree =
		(session?.worktree as WorktreeEnrichment | undefined | null) ??
		(pane?.process?.worktree as WorktreeEnrichment | undefined | null) ??
		null;

	return { pane, session, conversation, worktree };
}
