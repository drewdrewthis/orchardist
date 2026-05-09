/**
 * GraphQL client for the local daemon at 127.0.0.1:7777.
 *
 * The daemon exposes Query + Subscription only (no Mutation). Reads use the
 * HTTP endpoint via `graphql-request`; live updates use WebSocket subscriptions
 * via `graphql-ws`. Mutations (worktree create/remove, etc.) are NOT here —
 * those go through `$lib/tauri.ts` to `worktree-core` via Tauri commands.
 *
 * v1 hydrates the GUI store from `workView` + listens to `worktreeChanged`
 * and `tmuxSessionsChanged` for live updates. The mock data layer remains
 * the fallback when the daemon is offline (graceful: visual stays usable).
 */

import { GraphQLClient, gql } from "graphql-request";
import { createClient as createWsClient, type Client as WsClient } from "graphql-ws";

const HTTP_URL = "http://127.0.0.1:7777/graphql";
const WS_URL = "ws://127.0.0.1:7777/graphql";

let _http: GraphQLClient | null = null;
let _ws: WsClient | null = null;

function http(): GraphQLClient {
	if (!_http) _http = new GraphQLClient(HTTP_URL);
	return _http;
}

function ws(): WsClient {
	if (!_ws) {
		_ws = createWsClient({ url: WS_URL });
	}
	return _ws;
}

export interface DaemonWorktree {
	id: string;
	path: string;
	branch: string;
	head: string;
	bare: boolean;
	host: string;
	repo: string | null;
	pr: { number: number; state: string; ci: string | null; reviewDecision: string | null } | null;
	issue: { number: number; state: string } | null;
}

export interface DaemonProject {
	id: string;
	directory: string;
	name: string;
	worktrees: DaemonWorktree[];
}

export interface DaemonHost {
	id: string;
	hostname: string;
	reachable: boolean;
	lastSeenAt: string | null;
}

const WORK_VIEW = gql`
	query WorkView {
		workView {
			projects {
				id
				directory
				name
				worktrees {
					id
					path
					branch
					head
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
				}
			}
		}
		hosts {
			id
			hostname
			reachable
		}
	}
`;

export interface WorkViewData {
	workView: { projects: DaemonProject[] };
	hosts: DaemonHost[];
}

export async function fetchWorkView(): Promise<WorkViewData | null> {
	try {
		return await http().request<WorkViewData>(WORK_VIEW);
	} catch (err) {
		console.warn("[orchard-gui] daemon unreachable:", err);
		return null;
	}
}

const WORKTREE_CHANGED = gql`
	subscription WorktreeChanged($project: ID) {
		worktreeChanged(project: $project) {
			id
			path
			branch
			head
			bare
			host
			repo
		}
	}
`;

export function subscribeWorktreeChanged(
	onNext: (wt: DaemonWorktree) => void,
	onErr?: (err: unknown) => void,
): () => void {
	const dispose = ws().subscribe(
		{ query: WORKTREE_CHANGED, variables: { project: null } },
		{
			next: (d) => {
				const wt = (d.data as { worktreeChanged?: DaemonWorktree } | null)?.worktreeChanged;
				if (wt) onNext(wt);
			},
			error: (e) => onErr?.(e),
			complete: () => {},
		},
	);
	return dispose;
}

const TMUX_SESSIONS_CHANGED = gql`
	subscription TmuxSessionsChanged {
		tmuxSessionsChanged {
			id
		}
	}
`;

export function subscribeTmuxChanged(
	onNext: () => void,
	onErr?: (err: unknown) => void,
): () => void {
	const dispose = ws().subscribe(
		{ query: TMUX_SESSIONS_CHANGED },
		{
			next: () => onNext(),
			error: (e) => onErr?.(e),
			complete: () => {},
		},
	);
	return dispose;
}

/** Map daemon → GUI item shape, padding fields the daemon doesn't yet expose. */
export function mapDaemonToGui(data: WorkViewData) {
	const items = data.workView.projects.flatMap((p) =>
		p.worktrees.map((w) => ({
			id: w.id,
			kind: "worktree" as const,
			repo: w.repo || p.name,
			branch: w.branch,
			path: w.path,
			host: w.host,
			title: w.branch,
			status: "ok" as const,
			attentionReason: null,
			lastActivity: Date.now(),
			unread: 0,
			bare: w.bare,
			session: null,
			pr: w.pr
				? {
						number: w.pr.number,
						state: (w.pr.state.toLowerCase() as "open" | "draft" | "merged" | "closed") || "open",
						ci: "passing" as const,
						reviews: "pending" as const,
					}
				: null,
			issue: w.issue ? { number: w.issue.number, state: w.issue.state.toLowerCase() as "open" | "closed" } : null,
			contract: null,
			sparkline: [],
		})),
	);
	const hosts = data.hosts.map((h) => ({
		id: h.id,
		hostname: h.hostname,
		os: "",
		kernel: "",
		reachable: h.reachable,
		load: { cpu: 0, mem: 0, disk: 0 },
		lastSeenAt: Date.now(),
		tag: "",
	}));
	return { items, hosts };
}
