/**
 * Central app state — daemon-only, no mocks.
 *
 * All real-world data (worktrees, hosts, account, chat rooms) is hydrated
 * from the daemon at 127.0.0.1:7777 (HTTP queries + WS subscriptions) and
 * the chat-core watcher in the Tauri shell. There is no fallback dataset:
 * if a source is offline the corresponding UI shows empty state.
 *
 * Reactivity follows the Svelte 5 runes contract — every mutable field on
 * `AppStore` is a `$state` rune so component reads stay reactive.
 */

import {
	fetchSnapshot,
	subscribeAll,
	type ConversationSummary,
	type TmuxSessionSummary,
	type Unsub,
	type WorktreePaneSummary,
} from "./data/daemon";
import {
	fetchAttention,
	fetchRecent,
	fetchTmux,
	fetchIssues,
	type AttentionRow,
	type IssueRow,
	type LensId,
	type RecentRow,
	type TmuxLensSnapshot,
} from "./data/lenses";
import { buildPaletteEntries, PALETTE_ACTIONS } from "./data/palette";
import {
	getChatBackend,
	chatCoreToGuiMessage,
	getSelfHandle,
	type ChatBackend,
} from "./data/chat";
import type {
	Account,
	Agent,
	Conversation,
	ConvView,
	ForkPreview,
	Host,
	Item,
	Lens,
	Message,
	PaletteEntry,
	SendStatus,
	Surface,
	TerminalLine,
	Theme,
} from "./data/types";

export type { ForkPreview } from "./data/types";

/**
 * Tab identity. Two flavours:
 *   - "channel": chat-room conversation (chat-core)
 *   - "session": claude/tmux session — keyed by paneId and/or sessionUuid.
 *     Either or both may be present. The panel runs its own query
 *     against whatever is supplied; everything else (worktree, PR,
 *     transcript) follows from the graph.
 */
export type Tab =
	| { id: string; kind: "channel"; roomId: string; view: ConvView }
	| {
			id: string;
			kind: "session";
			paneId?: string;
			sessionUuid?: string;
			view: ConvView;
	  };

export interface Filter {
	kind: "host" | "status" | "repo";
	value: string;
}

export interface SendingState {
	tempId: string;
	text: string;
	ts: number;
	status: SendStatus;
}

export interface ChatRoomSummary {
	id: string;
	messageCount: number;
	memberCount: number;
}

const MAX_PANES = 3;

export class AppStore {
	items: Item[] = $state([]);
	hosts: Host[] = $state([]);
	account: Account | null = $state(null);
	conversations: ConversationSummary[] = $state([]);
	tmuxSessions: TmuxSessionSummary[] = $state([]);
	/** Server-joined Worktree → tmux panes (daemon #511). Keyed by worktree id. */
	worktreePanes: Record<string, WorktreePaneSummary[]> = $state({});
	/** Pane ids currently being watched by some attached tmux client. */
	activePaneIds: Set<string> = $state(new Set());
	chatRooms: ChatRoomSummary[] = $state([]);
	chatRoomCache: Record<string, Conversation> = $state({});
	/** Tick used by the few components that render relative timestamps. */
	now = $state(Date.now());

	/**
	 * Per-lens snapshots — each lens fetches against its own anchor and
	 * stores its own rows. Only the active lens is fetched on lens switch;
	 * other lenses keep their last snapshot until next refresh.
	 */
	lensSnapshots: {
		attention: AttentionRow[];
		recent: RecentRow[];
		tmux: TmuxLensSnapshot;
		issue: IssueRow[];
	} = $state({
		attention: [],
		recent: [],
		tmux: { sessions: [], activePaneIds: new Set(), alive: false, lastSeenByUuid: {} },
		issue: [],
	});
	/** Whether the active lens is currently mid-fetch. */
	lensLoading = $state(false);
	/** Agents currently have no daemon source — empty until wired. */
	readonly agents: Agent[] = [];
	/** Terminal scrollback has no daemon source yet — empty. */
	readonly terminalLines: TerminalLine[] = [];

	theme: Theme = $state("dark");
	surface: Surface = $state("desktop");
	accentHue = $state(215);
	density: "comfortable" | "compact" = $state("comfortable");
	lens: Lens = $state("attention");
	offline = $state(false);
	sidebarCollapsed = $state(false);
	sidebarWidth = $state(320);

	tabs: Tab[] = $state([]);
	activeTabId: string | null = $state(null);
	paneSizes: number[] = $state([]);
	fullscreen = $state(false);
	private nextTabSeq = 1;

	filters: Filter[] = $state([]);

	paletteOpen = $state(false);
	newConvOpen = $state(false);
	contractItemId: string | null = $state(null);

	composeText = $state("");
	sending: SendingState | null = $state(null);
	forkPreview: ForkPreview | null = $state(null);

	selfHandle: string | null = $state(null);

	private _chatUnsub: Unsub | null = null;
	private _daemonUnsub: Unsub | null = null;
	private _chatRoomLoading: Set<string> = new Set();

	get activeTab() {
		return this.tabs.find((t) => t.id === this.activeTabId) || null;
	}

	/**
	 * Stable identifier for the active tab — used by the sidebar to flag
	 * which row corresponds to the focused panel. For session tabs we
	 * prefer paneId, falling back to sessionUuid; for channel tabs the
	 * roomId.
	 */
	get selectedId(): string | null {
		const t = this.activeTab;
		if (!t) return null;
		if (t.kind === "channel") return t.roomId;
		return t.paneId || t.sessionUuid || null;
	}

	get view(): ConvView {
		return this.activeTab?.view || "chat";
	}

	get activeChannelRoomId(): string | null {
		const t = this.activeTab;
		return t && t.kind === "channel" ? t.roomId : null;
	}

	get visibleConversation(): Conversation | null {
		const roomId = this.activeChannelRoomId;
		if (!roomId) return null;
		const base = this.chatRoomCache[roomId] || emptyConversation(roomId, true);
		if (!this.sending) return base;
		return {
			...base,
			messages: [
				...base.messages,
				{
					id: this.sending.tempId,
					role: "user",
					text: this.sending.text,
					ts: this.sending.ts,
					status: this.sending.status,
				},
			],
		};
	}

	get mergedItems(): Item[] {
		const realChannels: Item[] = this.chatRooms.map((r) => ({
			id: r.id,
			kind: "channel",
			title: r.id.startsWith("@") ? r.id : `#${r.id}`,
			topic: "",
			participants: [],
			host: "multi",
			repo: "",
			status: "ok",
			attentionReason: null,
			lastActivity: 0,
			unread: 0,
			sparkline: [],
		}));
		const seen = new Set(realChannels.map((c) => c.id));
		const others = this.items.filter((i) => !seen.has(i.id));
		return [...realChannels, ...others];
	}

	get visibleItems(): Item[] {
		const all = this.mergedItems;
		if (this.filters.length === 0) return all;
		const by = (k: Filter["kind"]) => this.filters.filter((f) => f.kind === k).map((f) => f.value);
		const host = by("host");
		const status = by("status");
		const repo = by("repo");
		return all.filter((it) => {
			if (host.length && !host.includes((it as { host?: string }).host || "")) return false;
			if (status.length && !status.includes(it.status)) return false;
			const itRepo = "repo" in it ? it.repo : undefined;
			if (repo.length && (!itRepo || !repo.includes(itRepo))) return false;
			return true;
		});
	}

	get paletteEntries(): PaletteEntry[] {
		return buildPaletteEntries(this.items, this.hosts, this.chatRooms);
	}

	get paletteActions(): PaletteEntry[] {
		return PALETTE_ACTIONS;
	}

	setTheme = (t: Theme) => {
		this.theme = t;
	};

	toggleTheme = () => {
		this.theme = this.theme === "dark" ? "light" : "dark";
	};

	setSurface = (s: Surface) => {
		this.surface = s;
		if (s === "mobile") {
			this.tabs = [];
			this.activeTabId = null;
		}
	};

	setLens = (lens: Lens) => {
		this.lens = lens;
		this.refreshActiveLens();
	};

	/**
	 * Re-fetch the active lens. Idempotent. Called on lens switch, on
	 * any daemon subscription event, and on the 60s safety tick.
	 */
	refreshActiveLens = async (): Promise<void> => {
		const lens: LensId = this.lens as LensId;
		this.lensLoading = true;
		try {
			if (lens === "attention") {
				this.lensSnapshots.attention = await fetchAttention(this.now);
			} else if (lens === "recent") {
				this.lensSnapshots.recent = await fetchRecent();
			} else if (lens === "tmux") {
				this.lensSnapshots.tmux = await fetchTmux();
			} else if (lens === "issue") {
				this.lensSnapshots.issue = await fetchIssues();
			}
		} finally {
			this.lensLoading = false;
		}
	};

	/**
	 * Open a Claude/tmux session row. Behavior:
	 *   - split=false (default): replace the active tab's identity in
	 *     place, or open a single tab when no tabs exist.
	 *   - split=true: open a new tab alongside the existing ones (cap
	 *     at MAX_PANES; oldest tab gets evicted if at cap).
	 *
	 * Either paneId or sessionUuid must be supplied; both is fine.
	 */
	openSession = (
		key: { paneId?: string; sessionUuid?: string },
		opts: { split?: boolean } = {},
	) => {
		if (!key.paneId && !key.sessionUuid) return;

		// Mobile: always single-tab.
		if (this.surface === "mobile") {
			this.tabs = [{ id: "m1", kind: "session", paneId: key.paneId, sessionUuid: key.sessionUuid, view: "chat" }];
			this.activeTabId = "m1";
			this._resetPanelOnSwitch();
			return;
		}

		const existing = this.tabs.find(
			(t) =>
				t.kind === "session" &&
				((key.paneId && t.paneId === key.paneId) ||
					(key.sessionUuid && t.sessionUuid === key.sessionUuid)),
		);

		if (existing && !opts.split) {
			this.activeTabId = existing.id;
			return;
		}

		if (!opts.split && this.activeTab) {
			// Replace active tab in place.
			this.tabs = this.tabs.map((t) =>
				t.id === this.activeTabId
					? {
							id: t.id,
							kind: "session",
							paneId: key.paneId,
							sessionUuid: key.sessionUuid,
							view: "chat",
						}
					: t,
			);
			this._resetPanelOnSwitch();
			return;
		}

		// New tab.
		const id = "t" + this.nextTabSeq++;
		const tab: Tab = { id, kind: "session", paneId: key.paneId, sessionUuid: key.sessionUuid, view: "chat" };
		const next = [...this.tabs];
		if (next.length >= MAX_PANES) {
			next.shift();
		}
		next.push(tab);
		this.tabs = next;
		this.activeTabId = id;
		this._resetPaneSizes();
		this._resetPanelOnSwitch();
	};

	/**
	 * Open a chat-room channel tab. Same split semantics as openSession.
	 */
	openChannel = (roomId: string, opts: { split?: boolean } = {}) => {
		if (this.surface === "mobile") {
			this.tabs = [{ id: "m1", kind: "channel", roomId, view: "chat" }];
			this.activeTabId = "m1";
			this._maybeLoadChatRoom(roomId);
			this._resetPanelOnSwitch();
			return;
		}

		const existing = this.tabs.find((t) => t.kind === "channel" && t.roomId === roomId);
		if (existing && !opts.split) {
			this.activeTabId = existing.id;
			return;
		}

		if (!opts.split && this.activeTab) {
			this.tabs = this.tabs.map((t) =>
				t.id === this.activeTabId ? { id: t.id, kind: "channel", roomId, view: "chat" } : t,
			);
			this._maybeLoadChatRoom(roomId);
			this._resetPanelOnSwitch();
			return;
		}

		const id = "t" + this.nextTabSeq++;
		const tab: Tab = { id, kind: "channel", roomId, view: "chat" };
		const next = [...this.tabs];
		if (next.length >= MAX_PANES) next.shift();
		next.push(tab);
		this.tabs = next;
		this.activeTabId = id;
		this._resetPaneSizes();
		this._maybeLoadChatRoom(roomId);
		this._resetPanelOnSwitch();
	};

	mobileBack = () => {
		this.tabs = [];
		this.activeTabId = null;
	};

	private _resetPanelOnSwitch = () => {
		this.composeText = "";
		this.sending = null;
		this.forkPreview = null;
	};

	closeTab = (tabId: string) => {
		const idx = this.tabs.findIndex((t) => t.id === tabId);
		if (idx < 0) return;
		const next = this.tabs.filter((t) => t.id !== tabId);
		this.tabs = next;
		if (tabId === this.activeTabId) {
			const fallback = next[Math.max(0, idx - 1)] || null;
			this.activeTabId = fallback?.id ?? null;
		}
		this._resetPaneSizes();
	};

	cycleTab = (dir: 1 | -1) => {
		const idx = this.tabs.findIndex((t) => t.id === this.activeTabId);
		if (idx >= 0 && this.tabs.length > 1) {
			const nxt = this.tabs[(idx + dir + this.tabs.length) % this.tabs.length];
			this.activeTabId = nxt.id;
		}
	};

	jumpToTab = (i: number) => {
		const t = this.tabs[i];
		if (t) this.activeTabId = t.id;
	};

	setPaneSizes = (sizes: number[]) => {
		this.paneSizes = sizes;
	};

	private _resetPaneSizes = () => {
		const n = Math.max(1, this.tabs.length);
		this.paneSizes = Array(n).fill(1 / n);
	};

	setView = (v: ConvView) => {
		this.tabs = this.tabs.map((t) => (t.id === this.activeTabId ? { ...t, view: v } : t));
	};

	toggleView = () => {
		this.setView(this.view === "chat" ? "terminal" : "chat");
	};

	toggleSidebar = () => {
		this.sidebarCollapsed = !this.sidebarCollapsed;
	};

	setSidebarWidth = (w: number) => {
		this.sidebarWidth = w;
	};

	toggleFullscreen = () => {
		this.fullscreen = !this.fullscreen;
	};

	openPalette = () => {
		this.paletteOpen = true;
	};

	closePalette = () => {
		this.paletteOpen = false;
	};

	openNewConv = () => {
		this.newConvOpen = true;
	};

	closeNewConv = () => {
		this.newConvOpen = false;
	};

	openContract = (itemId: string | null) => {
		this.contractItemId = itemId;
	};

	addFilter = (f: Filter) => {
		if (!this.filters.some((x) => x.kind === f.kind && x.value === f.value)) {
			this.filters = [...this.filters, f];
		}
	};

	removeFilter = (idx: number) => {
		this.filters = this.filters.filter((_, i) => i !== idx);
	};

	clearFilters = () => {
		this.filters = [];
	};

	startFork = (fromIdx: number, msg: Message) => {
		this.forkPreview = { fromIdx, msg };
	};

	cancelFork = () => {
		this.forkPreview = null;
	};

	commitFork = () => {
		this.forkPreview = null;
	};

	send = () => {
		const text = this.composeText.trim();
		if (!text || this.sending) return;
		const roomId = this.activeChannelRoomId;
		if (!roomId) return;

		const tempId = "m.tmp." + Date.now();
		const ts = Date.now();
		this.composeText = "";
		this.sending = { tempId, text, ts, status: "pending" };

		const target = "#" + roomId.replace(/^#/, "");
		const b = getChatBackend();
		b.sendMessage(target, text)
			.then(() => {
				this.sending = null;
			})
			.catch((err) => {
				console.warn("[orchard-gui] chat send failed:", err);
				this.sending = null;
			});
	};

	hydrateFromDaemon = async (): Promise<boolean> => {
		const snap = await fetchSnapshot();
		if (!snap) {
			this.offline = true;
			return false;
		}
		this.offline = false;
		this.items = snap.items;
		this.hosts = snap.hosts;
		this.account = snap.account;
		this.conversations = snap.conversations;
		this.tmuxSessions = snap.tmuxSessions;
		this.worktreePanes = snap.worktreePanes;
		this.activePaneIds = snap.activePaneIds;
		// Refresh the active lens alongside the legacy hydration; cheap
		// while the lens query targets the same daemon.
		this.refreshActiveLens();
		return true;
	};

	/**
	 * The tmux panes whose foreground-process cwd sits in the worktree
	 * path, as the daemon reports it via `Worktree.tmuxPanes` (#511).
	 * A pane is the unit; window + session are context.
	 */
	tmuxPanesFor = (item: Item): WorktreePaneSummary[] => {
		if (item.kind !== "worktree") return [];
		return this.worktreePanes[item.id] || [];
	};

	/**
	 * Pick the "primary" pane for a worktree — the one we'd attach a
	 * terminal to by default. Preference order:
	 *   1. A pane that some live tmux client is currently watching
	 *      ("you are here").
	 *   2. The pane that's the active pane in an active window of an
	 *      attached session.
	 *   3. The pane in an active window (whether or not anyone's watching).
	 *   4. The first pane in the list (deterministic fallback).
	 * Returns null when no panes match.
	 */
	primaryPaneFor = (item: Item): WorktreePaneSummary | null => {
		const panes = this.tmuxPanesFor(item);
		if (panes.length === 0) return null;
		const here = panes.find((p) => this.activePaneIds.has(p.paneId));
		if (here) return here;
		const live = panes.find((p) => p.window.active && p.session.activeAttached);
		if (live) return live;
		const active = panes.find((p) => p.window.active);
		if (active) return active;
		return panes[0];
	};

	/**
	 * True when a tmux client is currently watching one of this worktree's
	 * matched panes — i.e. the user is actively in this worktree.
	 */
	isHere = (item: Item): boolean => {
		const panes = this.tmuxPanesFor(item);
		for (const p of panes) {
			if (this.activePaneIds.has(p.paneId)) return true;
		}
		return false;
	};

	/** Find the most-recent conversation summary for a worktree path. */
	conversationFor = (path: string): ConversationSummary | null => {
		let best: ConversationSummary | null = null;
		for (const c of this.conversations) {
			if (c.cwd !== path) continue;
			if (!best || c.lastSeenAt > best.lastSeenAt) best = c;
		}
		return best;
	};

	subscribeDaemon = (): Unsub => {
		if (this._daemonUnsub) return this._daemonUnsub;
		this._daemonUnsub = subscribeAll(
			() => {
				this.hydrateFromDaemon();
			},
			(err) => {
				console.warn("[orchard-gui] daemon subscription failed:", err);
				this.offline = true;
			},
		);
		return this._daemonUnsub;
	};

	hydrateChatRooms = async () => {
		const b = getChatBackend();
		try {
			this.chatRooms = await b.listRooms();
		} catch {
			this.chatRooms = [];
		}
	};

	loadChatRoom = async (roomId: string) => {
		const b = getChatBackend();
		const self = this.selfHandle ?? (await getSelfHandle());
		this.selfHandle = self;
		const full = await b.loadRoom(roomId);
		this.chatRoomCache = {
			...this.chatRoomCache,
			[roomId]: {
				itemId: roomId,
				recap: "",
				isChannel: true,
				messages: full.messages.map((m) => chatCoreToGuiMessage(m, self ?? undefined)),
			},
		};
	};

	private _maybeLoadChatRoom = (roomId: string) => {
		if (!this.chatRooms.some((r) => r.id === roomId)) return;
		if (this.chatRoomCache[roomId] || this._chatRoomLoading.has(roomId)) return;
		this._chatRoomLoading.add(roomId);
		this.loadChatRoom(roomId)
			.catch(() => {})
			.finally(() => this._chatRoomLoading.delete(roomId));
	};

	subscribeChat = async (): Promise<Unsub> => {
		if (this._chatUnsub) return this._chatUnsub;
		const b: ChatBackend = getChatBackend();
		const self = this.selfHandle ?? (await getSelfHandle());
		this.selfHandle = self;
		const unsub = await b.subscribeAppends((p) => {
			if (p.kind === "message") {
				const room = p.room;
				const cached = this.chatRoomCache[room];
				const msg = chatCoreToGuiMessage(p.line, self ?? undefined);
				const next = cached
					? { ...cached, messages: [...cached.messages, msg] }
					: { itemId: room, recap: "", isChannel: true, messages: [msg] };
				this.chatRoomCache = { ...this.chatRoomCache, [room]: next };
				this._patchRoomCount(room, +1);
			} else if (p.kind === "member_joined") {
				this._patchMemberCount(p.room, +1);
			} else if (p.kind === "member_left") {
				this._patchMemberCount(p.room, -1);
			}
		});
		this._chatUnsub = unsub;
		return unsub;
	};

	private _patchRoomCount = (roomId: string, delta: number) => {
		const idx = this.chatRooms.findIndex((r) => r.id === roomId);
		if (idx < 0) {
			this.chatRooms = [...this.chatRooms, { id: roomId, messageCount: Math.max(0, delta), memberCount: 0 }];
			return;
		}
		const next = [...this.chatRooms];
		next[idx] = { ...next[idx], messageCount: Math.max(0, next[idx].messageCount + delta) };
		this.chatRooms = next;
	};

	private _patchMemberCount = (roomId: string, delta: number) => {
		const idx = this.chatRooms.findIndex((r) => r.id === roomId);
		if (idx < 0) return;
		const next = [...this.chatRooms];
		next[idx] = { ...next[idx], memberCount: Math.max(0, next[idx].memberCount + delta) };
		this.chatRooms = next;
	};

	startNowTick = (): Unsub => {
		const id = setInterval(() => {
			this.now = Date.now();
		}, 60_000);
		return () => clearInterval(id);
	};

	teardown = () => {
		if (this._daemonUnsub) {
			this._daemonUnsub();
			this._daemonUnsub = null;
		}
		if (this._chatUnsub) {
			this._chatUnsub();
			this._chatUnsub = null;
		}
	};
}

function emptyConversation(itemId: string, isChannel: boolean): Conversation {
	return { itemId, recap: "", isChannel, messages: [] };
}

let _store: AppStore | null = null;
export function getStore(): AppStore {
	if (!_store) _store = new AppStore();
	return _store;
}
