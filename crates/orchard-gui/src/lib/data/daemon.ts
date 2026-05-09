/**
 * GraphQL client for the local daemon at 127.0.0.1:7777. The daemon is the
 * single source of truth for everything the GUI shows: worktrees, hosts,
 * tmux sessions, contracts, claude accounts. There is NO mock layer — if
 * the daemon is offline the GUI shows empty state, not fake content.
 *
 * Reads use the HTTP endpoint via `graphql-request`; live updates use
 * WebSocket subscriptions via `graphql-ws`. Mutations (create worktree
 * etc.) go through Tauri/`worktree-core`, not here.
 *
 * Surface kept minimal — every field returned MUST be displayed somewhere.
 */

import { GraphQLClient, gql } from "graphql-request";
import { createClient as createWsClient, type Client as WsClient } from "graphql-ws";
import type { Account, Host, Item, ItemStatus } from "./types";

/**
 * Endpoint resolution. We always go through the served origin (`/__daemon`)
 * when running in a real page — the Vite dev server proxies it in dev, and
 * a future production bundler can do the same. This avoids the daemon's
 * missing `Access-Control-Allow-Origin` header tripping WebKit's CORS check
 * inside the Tauri webview, whose origin is `tauri://localhost` rather than
 * `127.0.0.1:7777`. The only direct-to-daemon fallback is server-side
 * rendering / Node tests, where there's no `window`.
 */
function endpoints(): { http: string; ws: string } {
	if (typeof window === "undefined") {
		return { http: "http://127.0.0.1:7777/graphql", ws: "ws://127.0.0.1:7777/graphql" };
	}
	const wsProto = window.location.protocol === "https:" ? "wss:" : "ws:";
	return {
		http: `${window.location.origin}/__daemon/graphql`,
		ws: `${wsProto}//${window.location.host}/__daemon/graphql`,
	};
}

let _http: GraphQLClient | null = null;
let _ws: WsClient | null = null;

function http(): GraphQLClient {
	if (!_http) _http = new GraphQLClient(endpoints().http);
	return _http;
}

function ws(): WsClient {
	if (!_ws) _ws = createWsClient({ url: endpoints().ws, lazy: true });
	return _ws;
}

const DASHBOARD = gql`
	query Dashboard {
		workView {
			projects {
				id
				name
				worktrees {
					id
					path
					branch
					bare
					host
					repo
					pr {
						number
						state
					}
					issue {
						number
						state
					}
					tmuxPanes {
						paneId
						title
						currentCommand
						currentPid
						window {
							id
							index
							name
							active
							session {
								id
								name
								attached
								activeAttached
								lastActivityAt
							}
						}
					}
				}
			}
		}
		hosts {
			id
			hostname
			os
			reachable
			lastSeenAt
		}
		claudeAccounts {
			id
			email
			quotaUsed
			quotaCap
			quotaResetsAt
		}
		conversations {
			id
			sessionUuid
			cwd
			messageCount
			lastSeenAt
			firstSeenAt
			open
			recap
		}
		tmuxSessions {
			id
			name
			lastActivityAt
			windows {
				name
			}
		}
	}
`;

interface DashboardResponse {
	workView: {
		projects: Array<{
			id: string;
			name: string;
			worktrees: Array<{
				id: string;
				path: string;
				branch: string;
				bare: boolean;
				host: string;
				repo: string | null;
				pr: { number: number; state: string } | null;
				issue: { number: number; state: string } | null;
				tmuxPanes: Array<{
					paneId: string;
					title: string;
					currentCommand: string;
					currentPid: number | null;
					window: {
						id: string;
						index: number;
						name: string;
						active: boolean;
						session: {
							id: string;
							name: string;
							attached: boolean;
							activeAttached: boolean;
							lastActivityAt: string | null;
						};
					};
				}>;
			}>;
		}>;
	};
	hosts: Array<{
		id: string;
		hostname: string;
		os: string;
		reachable: boolean;
		lastSeenAt: string | null;
	}>;
	claudeAccounts: Array<{
		id: string;
		email: string;
		quotaUsed: number | null;
		quotaCap: number | null;
		quotaResetsAt: string | null;
	}>;
	conversations: Array<{
		id: string;
		sessionUuid: string;
		cwd: string | null;
		messageCount: number;
		lastSeenAt: string | null;
		firstSeenAt: string | null;
		open: boolean;
		recap: string | null;
	}>;
	tmuxSessions: Array<{
		id: string;
		name: string;
		lastActivityAt: string | null;
		windows: Array<{ name: string }>;
	}>;
}

export interface TmuxSessionSummary {
	id: string;
	name: string;
	lastActivityAt: number;
	windowNames: string[];
}

/**
 * One tmux pane that the daemon's CWD-join attached to a worktree (#511).
 * The pane is the unit — window/session are breadcrumb context.
 */
export interface WorktreePaneSummary {
	paneId: string;            // "%104"
	title: string;             // pane_title (often the foreground command's title)
	command: string;           // current_command (may be a Claude version string)
	pid: number | null;
	window: {
		id: string;            // "TmuxWindow:host:session:idx"
		index: number;
		name: string;
		active: boolean;       // current window in its session
	};
	session: {
		id: string;
		name: string;
		attached: boolean;             // any client attached anywhere
		activeAttached: boolean;       // a client is currently watching this session
		lastActivityAt: number;        // session-level (best signal until pane-level lands)
	};
}

export interface ConversationSummary {
	id: string;
	sessionUuid: string;
	cwd: string | null;
	messageCount: number;
	lastSeenAt: number;
	firstSeenAt: number;
	open: boolean;
	recap: string | null;
}

export interface Snapshot {
	items: Item[];
	hosts: Host[];
	account: Account | null;
	conversations: ConversationSummary[];
	tmuxSessions: TmuxSessionSummary[];
	/**
	 * Server-joined Worktree → tmux panes (#511). Keyed by worktree id;
	 * each entry is the list of panes whose foreground-process cwd sits
	 * inside the worktree path. A pane is the unit (touch point); window
	 * + session are breadcrumb context. The GUI uses this directly — no
	 * client-side join.
	 */
	worktreePanes: Record<string, WorktreePaneSummary[]>;
}

export async function fetchSnapshot(): Promise<Snapshot | null> {
	try {
		const data = await http().request<DashboardResponse>(DASHBOARD);
		return mapSnapshot(data);
	} catch (err) {
		console.warn("[orchard-gui] daemon unreachable:", err);
		return null;
	}
}

function mapSnapshot(d: DashboardResponse): Snapshot {
	const conversations: ConversationSummary[] = d.conversations.map((c) => ({
		id: c.id,
		sessionUuid: c.sessionUuid,
		cwd: c.cwd,
		messageCount: c.messageCount,
		lastSeenAt: c.lastSeenAt ? Date.parse(c.lastSeenAt) || 0 : 0,
		firstSeenAt: c.firstSeenAt ? Date.parse(c.firstSeenAt) || 0 : 0,
		open: c.open,
		recap: c.recap,
	}));

	// Pick the most-recently-active conversation per cwd. A worktree with
	// multiple Claude sessions in its history shows the freshest one.
	const byCwd = new Map<string, ConversationSummary>();
	for (const c of conversations) {
		if (!c.cwd) continue;
		const existing = byCwd.get(c.cwd);
		if (!existing || c.lastSeenAt > existing.lastSeenAt) byCwd.set(c.cwd, c);
	}

	const worktreePanes: Record<string, WorktreePaneSummary[]> = {};

	const items: Item[] = d.workView.projects.flatMap((p) =>
		p.worktrees
			.filter((w) => !w.bare)
			.map((w): Item => {
				const conv = byCwd.get(w.path) || null;
				if (w.tmuxPanes && w.tmuxPanes.length > 0) {
					worktreePanes[w.id] = w.tmuxPanes.map((tp) => ({
						paneId: tp.paneId,
						title: tp.title,
						command: tp.currentCommand,
						pid: tp.currentPid,
						window: {
							id: tp.window.id,
							index: tp.window.index,
							name: tp.window.name,
							active: tp.window.active,
						},
						session: {
							id: tp.window.session.id,
							name: tp.window.session.name,
							attached: tp.window.session.attached,
							activeAttached: tp.window.session.activeAttached,
							lastActivityAt: tp.window.session.lastActivityAt
								? Date.parse(tp.window.session.lastActivityAt) || 0
								: 0,
						},
					}));
				}
				return {
					id: w.id,
					kind: "worktree",
					repo: w.repo || p.name,
					branch: w.branch,
					path: w.path,
					host: w.host,
					title: w.branch,
					status: deriveStatus(w),
					attentionReason: null,
					lastActivity: conv?.lastSeenAt || 0,
					unread: 0,
					bare: w.bare,
					session: conv
						? {
								uuid: conv.sessionUuid,
								live: conv.open,
								instance: null,
								model: "",
							}
						: null,
					pr: w.pr
						? {
								number: w.pr.number,
								state: normalisePrState(w.pr.state),
								ci: "pending",
								reviews: "pending",
							}
						: null,
					issue: w.issue
						? { number: w.issue.number, state: w.issue.state.toLowerCase() === "closed" ? "closed" : "open" }
						: null,
					contract: null,
					sparkline: [],
				};
			}),
	);

	const hosts: Host[] = d.hosts.map((h) => ({
		id: h.id,
		hostname: h.hostname,
		os: h.os || "",
		kernel: "",
		reachable: h.reachable,
		load: { cpu: 0, mem: 0, disk: 0 },
		lastSeenAt: h.lastSeenAt ? Date.parse(h.lastSeenAt) || 0 : 0,
		tag: "",
	}));

	const acc = d.claudeAccounts[0];
	const account: Account | null = acc
		? {
				email: acc.email,
				quotaUsed: acc.quotaUsed ?? 0,
				quotaCap: acc.quotaCap ?? 0,
				quotaResetsAt: acc.quotaResetsAt ? Date.parse(acc.quotaResetsAt) || 0 : 0,
			}
		: null;

	const tmuxSessions: TmuxSessionSummary[] = d.tmuxSessions.map((s) => ({
		id: s.id,
		name: s.name,
		lastActivityAt: s.lastActivityAt ? Date.parse(s.lastActivityAt) || 0 : 0,
		windowNames: s.windows.map((w) => w.name),
	}));

	return { items, hosts, account, conversations, tmuxSessions, worktreePanes };
}

function deriveStatus(w: { pr: { state: string } | null }): ItemStatus {
	if (!w.pr) return "ok";
	const s = w.pr.state.toUpperCase();
	if (s === "MERGED" || s === "CLOSED") return "stale";
	return "ok";
}

function normalisePrState(s: string): "open" | "draft" | "merged" | "closed" {
	const v = s.toLowerCase();
	if (v === "merged") return "merged";
	if (v === "closed") return "closed";
	if (v === "draft") return "draft";
	return "open";
}

/**
 * The daemon's "global watch" subscriptions — the only ones that take no
 * arguments and so can be subscribed to once for the whole dashboard.
 * Per-key subscriptions (`worktreeChanged(project:)`, `pullRequestChanged(repo:,
 * number:)`) require IDs the GUI doesn't statically know; instead we layer
 * a slow `setInterval` refresh on top to catch PR/worktree drift without
 * fanning out to a sub per row.
 */
const TMUX_CHANGED = gql`
	subscription TmuxChanged {
		tmuxSessionsChanged {
			id
		}
	}
`;

const PROCESSES = gql`
	subscription Processes {
		processes {
			id
		}
	}
`;

export type Unsub = () => void;

const REFRESH_INTERVAL_MS = 60_000;

export function subscribeAll(onChange: () => void, onErr?: (e: unknown) => void): Unsub {
	const stops: Unsub[] = [];
	for (const query of [TMUX_CHANGED, PROCESSES]) {
		const dispose = ws().subscribe(
			{ query },
			{
				next: () => onChange(),
				error: (e) => onErr?.(e),
				complete: () => {},
			},
		);
		stops.push(dispose);
	}
	const id = setInterval(onChange, REFRESH_INTERVAL_MS);
	stops.push(() => clearInterval(id));
	return () => {
		for (const s of stops) s();
	};
}
