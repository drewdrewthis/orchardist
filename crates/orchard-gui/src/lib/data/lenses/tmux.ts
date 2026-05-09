/**
 * Tmux lens — anchor: tmuxServer, structure: server → sessions → windows → panes.
 *
 * The pane is the unit. Each pane carries its own claude / process /
 * cwd enrichment. The sidebar renders a tree, not a flat list.
 */
import { gql } from "graphql-request";
import { http } from "./client";
import { PANE_CARD_FRAGMENT, type PaneCardT } from "./fragments";

const TMUX_QUERY = gql`
	${PANE_CARD_FRAGMENT}
	query TmuxLens {
		tmuxServer {
			id
			alive
			sessions {
				id
				name
				attached
				activeAttached
				lastActivityAt
				windows {
					id
					index
					name
					active
					panes {
						...PaneCard
					}
				}
			}
			clients {
				tty
				currentPane {
					paneId
				}
			}
		}
	}
`;

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

interface TmuxLensResponse {
	tmuxServer: {
		id: string;
		alive: boolean;
		sessions: TmuxSessionNode[];
		clients: Array<{ tty: string; currentPane: { paneId: string } | null }>;
	} | null;
}

export async function fetchTmux(): Promise<TmuxLensSnapshot> {
	try {
		const data = await http().request<TmuxLensResponse>(TMUX_QUERY);
		const ts = data.tmuxServer;
		if (!ts) return { sessions: [], activePaneIds: new Set(), alive: false };
		const activePaneIds = new Set<string>();
		for (const c of ts.clients) {
			if (c.currentPane?.paneId) activePaneIds.add(c.currentPane.paneId);
		}
		return { sessions: ts.sessions, activePaneIds, alive: ts.alive };
	} catch (err) {
		console.warn("[orchard-gui] tmux lens fetch failed:", err);
		return { sessions: [], activePaneIds: new Set(), alive: false };
	}
}
