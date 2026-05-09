/**
 * Central app state. Svelte 5 runes-based class so reactivity follows the
 * `$state` rules everywhere it's read. Mock data is the v1 source; once the
 * GraphQL client lands, mock loaders here switch to subscription handlers
 * without changing component callers.
 */

import {
	allItems,
	conversation as mockConversation,
	channelConversations,
	hosts as mockHosts,
	account as mockAccount,
	terminalLines as mockTerminal,
	agents as mockAgents,
	paletteEntries,
	paletteActions,
} from "./data/mock";
import type {
	Account,
	Agent,
	ConvView,
	Conversation,
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

const MAX_PANES = 3;

export class AppStore {
	now = $state(Date.now());

	items: Item[] = $state(allItems);
	hosts: Host[] = $state(mockHosts);
	account: Account = $state(mockAccount);
	terminalLines: TerminalLine[] = $state(mockTerminal);
	agents: Agent[] = $state(mockAgents);
	paletteEntries: PaletteEntry[] = $state(paletteEntries);
	paletteActions: PaletteEntry[] = $state(paletteActions);

	theme: Theme = $state("dark");
	surface: Surface = $state("desktop");
	accentHue = $state(215);
	density: "comfortable" | "compact" = $state("comfortable");
	lens: Lens = $state("attention");
	offline = $state(false);
	sidebarCollapsed = $state(false);
	sidebarWidth = $state(320);

	tabs: Tab[] = $state([{ id: "t1", itemId: "w.orchard.api-pagination", view: "chat" }]);
	activeTabId: string | null = $state("t1");
	paneSizes: number[] = $state([1]);
	fullscreen = $state(false);
	private nextTabSeq = 2;

	filters: Filter[] = $state([]);

	paletteOpen = $state(false);
	newConvOpen = $state(false);
	contractItemId: string | null = $state(null);

	composeText = $state("");
	sending: SendingState | null = $state(null);
	forkPreview: ForkPreview | null = $state(null);

	conversation: Conversation = $state(structuredClone(mockConversation));

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
		return id ? this.items.find((i) => i.id === id) || null : null;
	}

	get visibleConversation(): Conversation {
		const item = this.activeItem;
		if (!item) return this.conversation;
		const base =
			item.kind === "channel"
				? channelConversations[item.id] || {
						itemId: item.id,
						recap: "",
						isChannel: true,
						messages: [],
					}
				: this.conversation;
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

	get visibleItems(): Item[] {
		if (this.filters.length === 0) return this.items;
		const by = (k: Filter["kind"]) =>
			this.filters.filter((f) => f.kind === k).map((f) => f.value);
		const host = by("host");
		const status = by("status");
		const repo = by("repo");
		return this.items.filter((it) => {
			if (host.length && !host.includes((it as { host?: string }).host || "")) return false;
			if (status.length && !status.includes(it.status)) return false;
			const itRepo = "repo" in it ? it.repo : undefined;
			if (repo.length && (!itRepo || !repo.includes(itRepo))) return false;
			return true;
		});
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
		} else if (this.tabs.length === 0) {
			const id = "t" + this.nextTabSeq++;
			this.tabs = [{ id, itemId: "w.orchard.api-pagination", view: "chat" }];
			this.activeTabId = id;
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
		this._resetConversationFor(itemId);
	};

	private _switchMobileItem = (itemId: string) => {
		this.tabs = [{ id: "m1", itemId, view: "chat" }];
		this.activeTabId = "m1";
		this._resetConversationFor(itemId);
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

	private _resetConversationFor = (itemId: string) => {
		const item = this.items.find((i) => i.id === itemId);
		if (item?.kind === "channel") {
			this.conversation = structuredClone(
				channelConversations[item.id] || {
					itemId: item.id,
					recap: "",
					isChannel: true,
					messages: [],
				},
			);
		} else {
			this.conversation = structuredClone(mockConversation);
		}
		this.sending = null;
		this.forkPreview = null;
		this.composeText = "";
	};

	send = () => {
		const text = this.composeText.trim();
		if (!text || this.sending) return;
		const tempId = "m.tmp." + Date.now();
		const ts = Date.now();
		this.composeText = "";
		this.sending = { tempId, text, ts, status: "pending" };

		const stages: { delay: number; status: SendStatus }[] = [
			{ delay: 350, status: "sent" },
			{ delay: 800, status: "delivered" },
			{ delay: 1700, status: "read" },
		];
		stages.forEach((s) => {
			setTimeout(() => {
				if (this.sending?.tempId === tempId) {
					this.sending = { ...this.sending, status: s.status };
				}
				if (s.status === "read") {
					setTimeout(() => {
						this.conversation = {
							...this.conversation,
							messages: [
								...this.conversation.messages,
								{
									id: tempId,
									role: "user",
									status: "read",
									ts,
									text,
								},
							],
						};
						this.sending = null;
						setTimeout(() => {
							const item = this.activeItem;
							const isChannel = item?.kind === "channel";
							const participants = isChannel
								? this.agents.filter((a) => item.participants?.includes(a.id))
								: [];
							const responder =
								isChannel && participants.length
									? participants[Math.floor(Math.random() * participants.length)]
									: null;
							this.conversation = {
								...this.conversation,
								messages: [
									...this.conversation.messages,
									{
										id: "m.agent." + Date.now(),
										role: "agent",
										agentId: responder?.id,
										status: "read",
										ts: Date.now(),
										text: agentReplyFor(text, responder),
									},
								],
							};
						}, 1400);
					}, 250);
				}
			}, s.delay);
		});
	};

	startNowTick = () => {
		const id = setInterval(() => {
			this.now = Date.now();
		}, 5000);
		return () => clearInterval(id);
	};

	hydrateFromDaemon = async () => {
		const { fetchWorkView, mapDaemonToGui } = await import("./data/graphql");
		const data = await fetchWorkView();
		if (!data) {
			this.offline = true;
			return false;
		}
		this.offline = false;
		const { items: liveWorktrees, hosts: liveHosts } = mapDaemonToGui(data);
		const channels = this.items.filter((i) => i.kind === "channel");
		this.items = [...channels, ...(liveWorktrees as Item[])];
		if (liveHosts.length) this.hosts = liveHosts as Host[];
		return true;
	};

	subscribeDaemon = async () => {
		const { subscribeWorktreeChanged, subscribeTmuxChanged } = await import("./data/graphql");
		const stops: Array<() => void> = [];
		stops.push(
			subscribeWorktreeChanged(
				() => {
					this.hydrateFromDaemon();
				},
				(err) => {
					console.warn("[orchard-gui] worktree subscription failed:", err);
					this.offline = true;
				},
			),
		);
		stops.push(
			subscribeTmuxChanged(
				() => {
					this.hydrateFromDaemon();
				},
				(err) => {
					console.warn("[orchard-gui] tmux subscription failed:", err);
				},
			),
		);
		return () => {
			for (const stop of stops) stop();
		};
	};

	startLiveTick = () => {
		const id = setInterval(() => {
			const idx = Math.floor(Math.random() * this.items.length);
			const next = [...this.items];
			next[idx] = {
				...next[idx],
				lastActivity: Date.now() - Math.floor(Math.random() * 30000),
			} as Item;
			this.items = next;
		}, 12_000);
		return () => clearInterval(id);
	};
}

function agentReplyFor(text: string, agent: Agent | null): string {
	const lower = text.toLowerCase();
	if (agent?.role === "Reviewer") return "Reading the diff now — I'll flag anything I'd block on before approving.";
	if (agent?.role === "Tester") return "Kicking off the relevant tests on my host. Will post counts when they settle.";
	if (agent?.role === "Patcher") return "On it — drafting the change against the worktree. Patch + tests incoming.";
	if (agent?.role === "Planner") return "Let me lay out the moves before we commit anyone's hands to a keyboard. Two options I see…";
	if (agent?.role === "Researcher") return "Pulling references — I'll cross-check against the existing pattern in `list_runs.rs` and report back.";
	if (agent?.role === "Writer") return "I can capture the decision in ADR form once you all settle on the shape.";
	if (lower.includes("test")) return "Running the test suite — give me 30s. I'll report counts and any failures.";
	if (lower.includes("push") || lower.includes("pr"))
		return "I'll stage the change, run pre-push hooks, and push to the worktree's branch. Want me to open the PR after?";
	if (lower.includes("?")) return "Good question. Let me check the current state and come back with specifics rather than guess.";
	return "Got it — picking that up next. I'll keep you posted as I make progress.";
}

let _store: AppStore | null = null;
export function getStore(): AppStore {
	if (!_store) _store = new AppStore();
	return _store;
}
