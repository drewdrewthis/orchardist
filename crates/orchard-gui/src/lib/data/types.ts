/**
 * Core data shapes for the Orchard GUI.
 *
 * These mirror what the daemon's GraphQL surface will expose. During the visual
 * port and offline development, the same types are populated from `mock.ts`.
 * When real wiring lands (`graphql.ts`), the types are unchanged — only the
 * source of the data changes.
 */

export type ItemKind = "worktree" | "channel";
export type ItemStatus = "attn" | "ok" | "bad" | "idle" | "stale";
export type ConvView = "chat" | "terminal";
/**
 * The four lenses. Each is a separate query against its own anchor,
 * not a reshuffling of a unified item list.
 *
 *   attention — claudeInstances + worktree enrichment, derive blocked/waiting/active tiers
 *   recent    — claudeInstances sorted by lastActivityAt desc
 *   tmux      — tmuxServer.sessions[].windows[].panes[] tree
 *   issue     — worktree.issue rows where worktree.pr is OPEN/DRAFT
 */
export type Lens = "attention" | "recent" | "tmux" | "issue";
export type Theme = "dark" | "light";
export type Surface = "desktop" | "mobile";
export type SendStatus = "pending" | "sent" | "delivered" | "read";

export interface Host {
	id: string;
	hostname: string;
	os: string;
	kernel: string;
	reachable: boolean;
	load: { cpu: number; mem: number; disk: number };
	lastSeenAt: number;
	tag: string;
}

export interface Account {
	email: string;
	quotaUsed: number;
	quotaCap: number;
	quotaResetsAt: number;
}

export interface SessionInfo {
	uuid: string;
	live: boolean;
	instance: string | null;
	model: string;
}

export interface TmuxAddr {
	server: string;
	session: string;
	window: { idx: number; name: string };
	pane: number;
}

export interface PrInfo {
	number: number;
	state: "open" | "draft" | "merged" | "closed";
	ci: "passing" | "failing" | "pending";
	reviews: "approved" | "changes-requested" | "commented" | "pending";
}

export interface IssueInfo {
	number: number;
	state: "open" | "closed";
}

export interface ContractInfo {
	id: string;
	status: "open" | "closed" | "delivered" | "abandoned";
	openQuestions: number;
}

export interface WorktreeItem {
	id: string;
	kind: "worktree";
	repo: string;
	branch: string;
	path: string;
	host: string;
	title: string;
	status: ItemStatus;
	attentionReason: string | null;
	lastActivity: number;
	unread: number;
	bare?: boolean;
	session: SessionInfo | null;
	tmux?: TmuxAddr;
	pr: PrInfo | null;
	issue: IssueInfo | null;
	contract: ContractInfo | null;
	sparkline: number[];
}

export interface ChannelItem {
	id: string;
	kind: "channel";
	title: string;
	topic: string;
	participants: string[];
	host: "multi" | string;
	repo: string;
	status: ItemStatus;
	attentionReason: string | null;
	lastActivity: number;
	unread: number;
	pinned?: boolean;
	sparkline: number[];
}

export type Item = WorktreeItem | ChannelItem;

export interface Agent {
	id: string;
	name: string;
	hue: number;
	model: string;
	role: string;
	host: string;
	avatar: string;
}

export interface Message {
	id: string;
	role: "user" | "agent";
	agentId?: string;
	status: SendStatus;
	ts: number;
	text: string;
	tools?: string[];
	diff?: { plus: number; minus: number; files: number };
	isQuestion?: boolean;
	isPaused?: boolean;
}

export interface Conversation {
	itemId: string;
	recap: string;
	isChannel?: boolean;
	messages: Message[];
}

export interface TerminalLine {
	c: "" | "p" | "dim" | "ok" | "attn" | "live";
	t: string;
}

export interface ForkPreview {
	fromIdx: number;
	msg: Message;
}

export interface PaletteEntry {
	kind:
		| "worktree"
		| "session"
		| "pr"
		| "issue"
		| "contract"
		| "host"
		| "channel"
		| "action";
	label: string;
	sub?: string;
	host?: string;
	anchor?: string;
	itemId?: string;
	view?: ConvView;
	group: string;
	keywords: string;
	shortcut?: string[];
	action?: string;
}
