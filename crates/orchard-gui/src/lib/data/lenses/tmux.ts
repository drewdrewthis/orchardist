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
import type { PaneCardT } from "./fragments";

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
 * Legacy facade for `AppStore.refreshActiveLens`. Phase 3 retires it.
 */
export async function fetchTmux(): Promise<TmuxLensSnapshot> {
	try {
		const { data } = await tmuxStore.fetch({ policy: "NetworkOnly" });
		return buildTmuxSnapshot(data);
	} catch (err) {
		console.warn("[orchard-gui] tmux lens fetch failed:", err);
		return EMPTY;
	}
}
