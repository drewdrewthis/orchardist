/**
 * UI state — Houdini's normalized cache is the daemon-data store, this
 * class is purely for surface state (theme, tabs, dialogs, chat caches,
 * compose drafts).
 *
 * Anything that lives in the daemon goes through Houdini stores
 * (`$lib/data/lenses/*`, `$lib/data/daemon-stores`); this file does
 * not import from `data/daemon` and has no `hydrateFromDaemon` /
 * `subscribeAll` glue. Earlier iterations carried `items: Item[]` /
 * `hosts: Host[]` / `account: Account` / `lensSnapshots` / `worktreePanes`
 * / `activePaneIds` / `mergedItems` / `paletteEntries` / `agents` /
 * `terminalLines`; all of that has been deleted in favour of Houdini.
 */
import {
	getChatBackend,
	chatCoreToGuiMessage,
	getSelfHandle,
	type ChatBackend,
} from "./data/chat";
import type {
	Conversation,
	ConvView,
	ForkPreview,
	Lens,
	Message,
	SendStatus,
	Surface,
	Theme,
} from "./data/types";

export type { ForkPreview } from "./data/types";

/** A subscription cleanup handle; mirrored from chat-core/Houdini. */
export type Unsub = () => void;

/**
 * Tab identity. Two flavours:
 *   - "channel": chat-room conversation (chat-core)
 *   - "session": claude/tmux session — keyed by paneId and/or sessionUuid.
 *     Either or both may be present. The panel runs its own Houdini query
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
	chatRooms: ChatRoomSummary[] = $state([]);
	chatRoomCache: Record<string, Conversation> = $state({});
	/** Tick used by the few components that render relative timestamps. */
	now = $state(Date.now());

	theme: Theme = $state("dark");
	surface: Surface = $state("desktop");
	accentHue = $state(215);
	density: "comfortable" | "compact" = $state("comfortable");
	lens: Lens = $state("attention");
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

	composeText = $state("");
	sending: SendingState | null = $state(null);
	forkPreview: ForkPreview | null = $state(null);

	selfHandle: string | null = $state(null);

	private _chatUnsub: Unsub | null = null;
	private _chatRoomLoading: Set<string> = new Set();

	get activeTab() {
		return this.tabs.find((t) => t.id === this.activeTabId) || null;
	}

	/**
	 * Full selection keys for the active tab. The sidebar matches a row
	 * if EITHER its paneId or its sessionUuid is in this set — both are
	 * valid handles for the same conversation, and lens snapshots differ
	 * on which one is populated (a tmux row only has paneId; a recent
	 * row keyed off a dead pane only has sessionUuid).
	 */
	get selection():
		| { kind: "channel"; roomId: string }
		| { kind: "session"; paneId: string | null; sessionUuid: string | null }
		| null {
		const t = this.activeTab;
		if (!t) return null;
		if (t.kind === "channel") return { kind: "channel", roomId: t.roomId };
		return {
			kind: "session",
			paneId: t.paneId ?? null,
			sessionUuid: t.sessionUuid ?? null,
		};
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

		// Default view: when the row has a Claude session attached (sessionUuid),
		// the chat transcript is what the user wants first; raw tmux panes
		// without a session default to the terminal.
		const defaultView: ConvView = key.sessionUuid ? "chat" : "terminal";

		// Mobile: always single-tab.
		if (this.surface === "mobile") {
			this.tabs = [
				{ id: "m1", kind: "session", paneId: key.paneId, sessionUuid: key.sessionUuid, view: defaultView },
			];
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
			this.tabs = this.tabs.map((t) =>
				t.id === this.activeTabId
					? { id: t.id, kind: "session", paneId: key.paneId, sessionUuid: key.sessionUuid, view: defaultView }
					: t,
			);
			this._resetPanelOnSwitch();
			return;
		}

		const id = "t" + this.nextTabSeq++;
		const tab: Tab = { id, kind: "session", paneId: key.paneId, sessionUuid: key.sessionUuid, view: defaultView };
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

	setTabView = (tabId: string, v: ConvView) => {
		this.tabs = this.tabs.map((t) => (t.id === tabId ? { ...t, view: v } : t));
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
			this.chatRooms = [
				...this.chatRooms,
				{ id: roomId, messageCount: Math.max(0, delta), memberCount: 0 },
			];
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
