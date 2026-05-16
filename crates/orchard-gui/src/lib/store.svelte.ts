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
import { toast } from "./util/toast";
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
 * State of an in-flight or recently-resolved user message in the iMessage
 * indicator model:
 *   sending  — optimistic insert, mutation not yet returned
 *   sent     — mutation returned true (tmux ack)
 *   received — conversationChanged subscription fired + turns.length grew
 *   seen     — first assistant turn appeared after this message
 *   stalled  — 90s elapsed in "sent" without receiving
 */
export type PendingTurnStatus = "sending" | "sent" | "received" | "seen" | "stalled";

export interface PendingTurn {
	id: string;
	text: string;
	sentAt: number;
	/** turns.length at send time — used to match the earliest new turn. */
	turnsLengthAtSend: number;
	status: PendingTurnStatus;
}

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
			/** Title from the sidebar row — displayed while OpenPanel loads. */
			titleHint?: string;
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

/**
 * Synchronous localStorage hydration helper. Tauri/SPA only — no SSR, so
 * `localStorage` is always defined at module load time. Wrapped in
 * try/catch because Safari can throw `SecurityError` in private-browsing.
 */
function hydrateLocalStorage(key: string, fallback: string): string {
	try {
		return localStorage.getItem(key) ?? fallback;
	} catch {
		return fallback;
	}
}

export class AppStore {
	chatRooms: ChatRoomSummary[] = $state([]);
	chatRoomCache: Record<string, Conversation> = $state({});
	/** Tick used by the few components that render relative timestamps. */
	now = $state(Date.now());

	// Hydrate theme + a few other UI preferences from localStorage at boot
	// so they survive reloads. SPA-only (Tauri / browser), no SSR.
	theme: Theme = $state(hydrateLocalStorage("orchard:ui:theme", "dark") as Theme);
	surface: Surface = $state("desktop");
	accentHue = $state(Number(hydrateLocalStorage("orchard:ui:accent-hue", "215")) || 215);
	density: "comfortable" | "compact" = $state(
		hydrateLocalStorage("orchard:ui:density", "comfortable") as "comfortable" | "compact",
	);
	lens: Lens = $state(hydrateLocalStorage("orchard:ui:lens", "attention") as Lens);
	sidebarCollapsed = $state(false);
	sidebarWidth = $state(320);

	/**
	 * Mute the in-app audio tick. Persists in localStorage.
	 * When true, playPing() and the REPL pill pulse are suppressed.
	 */
	chatMute = $state(hydrateLocalStorage("orchard:ui:chat-mute", "false") === "true");

	/**
	 * Web Notification opt-in. Persists in localStorage.
	 * When true (and permission granted), fireWebNotification() fires on background tabs.
	 */
	chatNotify = $state(hydrateLocalStorage("orchard:ui:chat-notify", "false") === "true");

	tabs: Tab[] = $state([]);
	activeTabId: string | null = $state(null);
	/**
	 * Pending user messages keyed by sessionUuid (or paneId when no uuid
	 * is known yet). Each entry is an ordered list of in-flight bubbles.
	 */
	pendingTurns: Record<string, PendingTurn[]> = $state({});
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
		return this.activeTab?.view || "terminal";
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
		key: { paneId?: string; sessionUuid?: string; titleHint?: string },
		opts: { split?: boolean } = {},
	) => {
		if (!key.paneId && !key.sessionUuid) return;

		// Default view rule:
		//   - mobile: always chat (browser can't host a PTY, terminal view
		//     would just show "desktop app required").
		//   - desktop + live pane: terminal (the substrate when attached).
		//   - desktop + no pane: chat (the jsonl is the only thing to show).
		// Drew (2026-05-10): "if no tmux sessions still okay, right, we have
		// the jsonl."
		const defaultView: ConvView =
			this.surface === "mobile" ? "chat" : key.paneId ? "terminal" : "chat";

		// Mobile: always single-tab.
		if (this.surface === "mobile") {
			this.tabs = [
				{ id: "m1", kind: "session", paneId: key.paneId, sessionUuid: key.sessionUuid, view: defaultView, titleHint: key.titleHint },
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
					? { id: t.id, kind: "session", paneId: key.paneId, sessionUuid: key.sessionUuid, view: defaultView, titleHint: key.titleHint }
					: t,
			);
			this._resetPanelOnSwitch();
			return;
		}

		const id = "t" + this.nextTabSeq++;
		const tab: Tab = { id, kind: "session", paneId: key.paneId, sessionUuid: key.sessionUuid, view: defaultView, titleHint: key.titleHint };
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
		this.setView(this.view === "terminal" ? "chat" : "terminal");
	};

	/**
	 * Add an optimistic pending turn for a session. Called synchronously
	 * before the mutation fires so the bubble appears instantly.
	 */
	addPendingTurn = (sessionKey: string, turn: PendingTurn) => {
		const existing = this.pendingTurns[sessionKey] ?? [];
		this.pendingTurns = { ...this.pendingTurns, [sessionKey]: [...existing, turn] };
	};

	/**
	 * Patch the status of a specific pending turn by id.
	 */
	patchPendingTurn = (sessionKey: string, id: string, status: PendingTurnStatus) => {
		const list = this.pendingTurns[sessionKey];
		if (!list) return;
		const next = list.map((t) => (t.id === id ? { ...t, status } : t));
		this.pendingTurns = { ...this.pendingTurns, [sessionKey]: next };
	};

	/**
	 * Remove a pending turn once it's fully resolved (seen state displayed
	 * for a moment, then fades). Callers handle the fade timing before calling.
	 */
	removePendingTurn = (sessionKey: string, id: string) => {
		const list = this.pendingTurns[sessionKey];
		if (!list) return;
		const next = list.filter((t) => t.id !== id);
		this.pendingTurns = { ...this.pendingTurns, [sessionKey]: next };
	};

	/** Clear all pending turns for a session (e.g. when tab is closed). */
	clearPendingTurns = (sessionKey: string) => {
		const next = { ...this.pendingTurns };
		delete next[sessionKey];
		this.pendingTurns = next;
	};

	/** Toggle in-app audio ping mute. Persists to localStorage. */
	toggleChatMute = () => {
		this.chatMute = !this.chatMute;
		try {
			localStorage.setItem("orchard:ui:chat-mute", String(this.chatMute));
		} catch { /* private browsing */ }
	};

	/** Toggle Web Notification opt-in. Persists to localStorage. */
	setChatNotify = (value: boolean) => {
		this.chatNotify = value;
		try {
			localStorage.setItem("orchard:ui:chat-notify", String(value));
		} catch { /* private browsing */ }
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
				toast.error(err);
				this.sending = null;
			});
	};

	hydrateChatRooms = async () => {
		const b = getChatBackend();
		try {
			this.chatRooms = await b.listRooms();
		} catch {
			// intentional swallow: chat-core may not be running; sidebar degrades to empty rooms list silently on startup
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
			// intentional swallow: lazy background room load; failure leaves the room absent from cache, user can re-select to retry
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
