/**
 * Tmux lens — anchor: tmuxServer, structure: server → sessions → windows → panes.
 *
 * The pane is the unit. Each pane carries its claude instance + process
 * + cwd enrichment, all daemon-joined (PaneCard.claudeInstance is now
 * the full SessionCard, including worktree + conversation).
 *
 * Houdini operation lives at `lenses/houdini/TmuxLens.gql`. This file
 * exposes the singleton store + the `buildTmuxSnapshot` projection.
 */
import { TmuxLensStore, type TmuxLens$result } from "$houdini";
import { parseTime } from "./client";
import type { PaneCardT, SessionCardT, WorktreeEnrichment } from "./fragments";
import type { SidebarItem, SidebarSection } from "$lib/data/sidebar-item";
import { buildSidebarItem } from "$lib/data/sidebar-item";

/** Singleton store for the tmux lens. */
export const tmuxStore = new TmuxLensStore();

type Data = NonNullable<TmuxLens$result>;

export interface TmuxWindowNode {
	id: string;
	index: number;
	name: string;
	active: boolean;
	panes: PaneCardT[];
}

export interface TmuxSessionNode {
	id: string;
	name: string;
	attached: boolean;
	activeAttached: boolean;
	lastActivityAt: string | null;
	windows: TmuxWindowNode[];
}

export interface TmuxLensSnapshot {
	sessions: TmuxSessionNode[];
	/** Pane ids currently being watched by some attached client. */
	activePaneIds: Set<string>;
	/** Whether the tmux server is reachable. */
	alive: boolean;
}

const EMPTY: TmuxLensSnapshot = {
	sessions: [],
	activePaneIds: new Set(),
	alive: false,
};

/**
 * Project the Houdini result into a tree-shaped lens snapshot. Pure —
 * components call this inside `$derived` against `$tmuxStore.data`.
 */
export function buildTmuxSnapshot(data: Data | null | undefined): TmuxLensSnapshot {
	if (!data) return EMPTY;
	const ts = data.tmuxServer;
	if (!ts) return EMPTY;
	const activePaneIds = new Set<string>();
	for (const c of ts.clients) {
		if (c.currentPane?.paneId) activePaneIds.add(c.currentPane.paneId);
	}
	return {
		sessions: ts.sessions as unknown as TmuxSessionNode[],
		activePaneIds,
		alive: ts.alive,
	};
}

/**
 * Projection into sectioned `SidebarItem[]` per #540 B0/B1/B3.
 * The tmux lens groups by tmux session — one section per session.
 * Items are the Claude sessions living on a pane in that tmux session.
 * Panes without a Claude session are dropped from the item list.
 */
export function buildTmuxSections(
	data: Data | null | undefined,
): SidebarSection[] {
	const snap = buildTmuxSnapshot(data);
	if (!snap.alive) return [];
	const sections: SidebarSection[] = [];
	for (const session of snap.sessions) {
		const items: SidebarItem[] = [];
		for (const win of session.windows) {
			for (const pane of win.panes) {
				if (!pane.claudeInstance) continue;
				const ci = pane.claudeInstance as unknown as SessionCardT;
				const worktree = (ci.worktree ?? null) as WorktreeEnrichment | null;
				const conv = ci.conversation;
				const lastActivityMs =
					parseTime(conv?.lastSeenAt) || parseTime(ci.lastActivityAt);
				const hints = conv
					? { agentName: conv.agentName ?? null, customTitle: conv.customTitle ?? null }
					: null;
				items.push(buildSidebarItem(ci, worktree, lastActivityMs, [], hints));
			}
		}
		sections.push({
			id: `tmux-${session.id}`,
			label: session.name,
			items,
		});
	}
	return sections;
}
