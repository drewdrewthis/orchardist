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
} from "./data/daemon";
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

export interface Tab {
	id: string;
	itemId: string;
	view: ConvView;
}

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
	chatRooms: ChatRoomSummary[] = $state([]);
	chatRoomCache: Record<string, Conversation> = $state({});
	/** Tick used by the few components that render relative timestamps. */
	now = $state(Date.now());
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

	get selectedId() {
		return this.activeTab?.itemId || null;
	}

	get view(): ConvView {
		return this.activeTab?.view || "chat";
	}

	get activeItem(): Item | null {
		const id = this.selectedId;
		if (!id) return null;
		return this.mergedItems.find((i) => i.id === id) || null;
	}

	get visibleConversation(): Conversation | null {
		const item = this.activeItem;
		if (!item) return null;
		const base =
			item.kind === "channel" ? this.chatRoomCache[item.id] || emptyConversation(item.id, true) : null;
		if (!base) return null;
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
	};

	openItem = (itemId: string, opts: { newPane?: boolean; focus?: boolean } = {}) => {
		const { newPane = false, focus = true } = opts;
		const existing = this.tabs.find((t) => t.itemId === itemId);
		if (existing && !newPane) {
			if (focus) this.activeTabId = existing.id;
			if (this.surface === "mobile") this._switchMobileItem(itemId);
			return;
		}
		const id = "t" + this.nextTabSeq++;
		const tab: Tab = { id, itemId, view: "chat" };
		const next = [...this.tabs];
		if (next.length >= MAX_PANES) {
			const activeIdx = Math.max(
				0,
				next.findIndex((t) => t.id === this.activeTabId),
			);
			next.splice(activeIdx, 1, tab);
		} else {
			next.push(tab);
		}
		this.tabs = next;
		if (focus) this.activeTabId = id;
		this._resetPaneSizes();
		this._maybeLoadChatRoom(itemId);
		this.composeText = "";
		this.sending = null;
		this.forkPreview = null;
	};

	private _switchMobileItem = (itemId: string) => {
		this.tabs = [{ id: "m1", itemId, view: "chat" }];
		this.activeTabId = "m1";
		this._maybeLoadChatRoom(itemId);
		this.composeText = "";
		this.sending = null;
		this.forkPreview = null;
	};

	mobileOpen = (itemId: string) => {
		this._switchMobileItem(itemId);
	};

	mobileBack = () => {
		this.tabs = [];
		this.activeTabId = null;
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
		const item = this.activeItem;
		if (!item || item.kind !== "channel") return;

		const tempId = "m.tmp." + Date.now();
		const ts = Date.now();
		this.composeText = "";
		this.sending = { tempId, text, ts, status: "pending" };

		const target = "#" + item.id.replace(/^#/, "");
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
		return true;
	};

	/**
	 * Heuristic match between a worktree row and a live tmux session.
	 *
	 * The daemon doesn't yet wire `pane.process.cwd` (issue #463) or
	 * `claudeInstance` pid joins (issue #468), so we can't ask "which pane
	 * is in this worktree path?" at the daemon level. Instead, we lean on
	 * the orchard naming convention: tmux windows for issue worktrees are
	 * named after the branch's last segment (e.g. branch
	 * `issue468/claudeinstances-pid-join-broken` → window
	 * `issue468-claudeinstances-pid-join-broken`). Match by substring on
	 * either window name or session name. Fallback: empty.
	 */
	/**
	 * The worktree's attached tmux session, as the daemon reports it.
	 *
	 * Today the daemon does NOT expose `Worktree.tmuxSession` or
	 * `Worktree.tmuxPanes` — that requires `pane.process.cwd` (#463) plus
	 * a join resolver, and is tracked as #506. Until that lands this
	 * always returns null and the conversation pane shows the "no
	 * session attached" state. **No client-side heuristic.** When the
	 * daemon ships the field, this collapses to a single field read.
	 */
	tmuxSessionFor = (_item: Item): TmuxSessionSummary | null => {
		return null;
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

	private _maybeLoadChatRoom = (itemId: string) => {
		const item = this.mergedItems.find((i) => i.id === itemId);
		if (item?.kind !== "channel") return;
		if (this.chatRoomCache[itemId] || this._chatRoomLoading.has(itemId)) return;
		this._chatRoomLoading.add(itemId);
		this.loadChatRoom(itemId)
			.catch(() => {})
			.finally(() => this._chatRoomLoading.delete(itemId));
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
