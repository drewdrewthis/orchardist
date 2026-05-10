/**
 * Tmux lens — anchor: tmuxServer, structure: server → sessions → windows → panes.
 *
 * The pane is the unit. Each pane carries its own claude / process /
 * cwd enrichment. The sidebar renders a tree, not a flat list.
 *
 * Houdini operation lives at `lenses/houdini/TmuxLens.gql`. This file
 * exposes the singleton store + the `buildTmuxSnapshot` projection
 * (which folds `clients` into a Set<paneId> for fast lookup and
 * `conversations` into a uuid→lastSeenAt map).
 */
import { TmuxLensStore, type TmuxLens$result } from "$houdini";
import type { PaneCardT, SessionCardT, WorktreeEnrichment } from "./fragments";
import type { SidebarItem, SidebarSection } from "$lib/data/sidebar-item";
import { buildSidebarItem } from "$lib/data/sidebar-item";

/** Singleton Houdini store for the tmux lens. */
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
	/** sessionUuid → ms-since-epoch from the conversations transcript. */
	lastSeenByUuid: Record<string, number>;
}

const EMPTY: TmuxLensSnapshot = {
	sessions: [],
	activePaneIds: new Set(),
	alive: false,
	lastSeenByUuid: {},
};

/**
 * Project the Houdini result into a tree-shaped lens snapshot. Pure —
 * components call this inside `$derived` against `$tmuxStore.data`.
 */
export function buildTmuxSnapshot(data: Data | null | undefined): TmuxLensSnapshot {
	if (!data) return EMPTY;
	const ts = data.tmuxServer;
	const lastSeenByUuid: Record<string, number> = {};
	for (const c of data.conversations || []) {
		const t = c.lastSeenAt ? Date.parse(c.lastSeenAt) || 0 : 0;
		if (t > 0) lastSeenByUuid[c.sessionUuid] = t;
	}
	if (!ts) return { ...EMPTY, lastSeenByUuid };
	const activePaneIds = new Set<string>();
	for (const c of ts.clients) {
		if (c.currentPane?.paneId) activePaneIds.add(c.currentPane.paneId);
	}
	return {
		sessions: ts.sessions as unknown as TmuxSessionNode[],
		activePaneIds,
		alive: ts.alive,
		lastSeenByUuid,
	};
}

/**
 * Projection into sectioned `SidebarItem[]` per #540 B0/B1/B3.
 * The tmux lens groups by tmux session — one section per session.
 * Each item is the Claude session living on a pane in that tmux
 * session; panes without a Claude session are dropped from the item
 * list (the unified item model requires a session). Empty sections
 * still surface so the user sees their tmux topology.
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
				// Build a synthetic SessionCardT from the pane's claude
				// instance + pane chain. We can't query the full SessionCard
				// fragment from a TmuxPane, so rebuild the bits we need.
				const ci = pane.claudeInstance;
				const synthetic = {
					id: ci.id,
					sessionUuid: ci.sessionUuid,
					state: ci.state,
					startedAt: null,
					lastActivityAt: ci.lastActivityAt ?? null,
					rcEnabled: false,
					account: null,
					pane: {
						paneId: pane.paneId,
						title: pane.title,
						currentCommand: pane.currentCommand,
						window: {
							id: win.id,
							index: win.index,
							name: win.name,
							active: win.active,
							session: {
								id: session.id,
								name: session.name,
								attached: session.attached,
								activeAttached: session.activeAttached,
							},
						},
					},
					process: pane.currentPid
						? { pid: pane.currentPid, cwd: pane.process?.cwd ?? null }
						: null,
				} as unknown as SessionCardT;
				const lastMs = ci.sessionUuid
					? snap.lastSeenByUuid[ci.sessionUuid] ?? 0
					: 0;
				items.push(
					buildSidebarItem(synthetic, /* worktree */ null, lastMs, []),
				);
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

