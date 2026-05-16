<!--
  Session pane — given a row identity (paneId and/or sessionUuid), runs
  its own OpenPanel query against the daemon and renders:
    - Header with worktree breadcrumb + PR/issue chips + pane chips
    - REPL state pill (idle / working / responding / thinking / stalled / dead)
    - Live attached terminal (when a tmux pane exists)
    - Empty placeholder when only a sessionUuid is known and the pane
      has been killed.

  The sidebar emits row identity on click; everything else follows
  from the graph.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import TerminalAttach from "./TerminalAttach.svelte";
	import TranscriptView from "./TranscriptView.svelte";
	import SessionComposer from "./SessionComposer.svelte";
	import ViewSwitcher from "./ViewSwitcher.svelte";
	import { createPanelStore, buildPanelData } from "$lib/data/lenses/panel";
	import { tmuxStore, buildTmuxSnapshot } from "$lib/data/lenses/tmux";
	import { relTime } from "$lib/util/format";
	import { getStore } from "$lib/store.svelte";
	import type { ConvView } from "$lib/data/types";

	type Props = {
		paneId?: string;
		sessionUuid?: string;
		/** Optimistic title from the sidebar row; rendered while OpenPanel loads. */
		titleHint?: string;
		active: boolean;
		paneCount: number;
		isLast: boolean;
		fullscreen: boolean | null;
		now: number;
		surface: "desktop" | "mobile";
		view: ConvView;
		onSetView: (v: ConvView) => void;
		onActivate: () => void;
		onClose: () => void;
		onToggleFullscreen?: () => void;
	};
	let {
		paneId,
		sessionUuid,
		titleHint,
		active,
		paneCount,
		isLast,
		fullscreen,
		now,
		surface,
		view,
		onSetView,
		onActivate,
		onClose,
		onToggleFullscreen,
	}: Props = $props();

	const store = getStore();

	// One Houdini panel store per open pane. The tab identity
	// (paneId, sessionUuid) feeds the query variables; the `paneIds`
	// filter narrows the daemon's pane snapshot to this row.
	const panelStore = createPanelStore();

	$effect(() => {
		panelStore.fetch({ variables: { paneIds: paneId ? [paneId] : null } });
	});

	const data = $derived(
		buildPanelData($panelStore.data, { paneId, sessionUuid }),
	);
	const loading = $derived($panelStore.fetching);
	const pane = $derived(data?.pane ?? null);
	const session = $derived(data?.session ?? null);
	const conversation = $derived(data?.conversation ?? null);
	const worktree = $derived(data?.worktree ?? null);

	/**
	 * Stable session key for pending turns. Prefer sessionUuid (stable
	 * across pane respawns); fall back to paneId when no uuid is known.
	 */
	const sessionKey = $derived(
		conversation?.sessionUuid ?? session?.sessionUuid ?? sessionUuid ?? paneId ?? "",
	);

	/**
	 * Pending turns count — used to pass current turnsLength to the composer
	 * so it can capture turnsLengthAtSend correctly.
	 * TranscriptView owns the real turns array; we proxy via the store's
	 * pendingTurns count only as a length hint. The real count comes from
	 * the transcript loaded inside TranscriptView.
	 *
	 * We thread `turnsLength` as a reactive prop into TranscriptView via
	 * a binding — but since Svelte 5 doesn't have two-way primitive bindings
	 * for state derived inside child components, we use a shared ref.
	 */
	let transcriptTurnsLength = $state(0);

	// `here` flag still needs the tmux server's client → currentPane
	// map. Read straight from the tmux Houdini store (already kicked
	// off by LensSidebar; the cache means this is free).
	const tmuxSnapshot = $derived(buildTmuxSnapshot($tmuxStore.data));

	// Fallback pane derivation: when the OpenPanel resolver can't attach
	// a pane (most often because daemon.claudeInstances doesn't include
	// this session — see ADR-022's note on the claudeinstance subsystem
	// rewrite), find a pane in the live tmux snapshot whose process cwd
	// matches the conversation's cwd AND whose currentCommand is claude.
	// The pane id is enough for the SessionComposer's mutation; we don't
	// need to fully synthesise a TmuxPane object.
	const fallbackPaneId = $derived.by((): string | null => {
		if (pane?.paneId) return null; // OpenPanel already resolved one
		const cwd = conversation?.cwd;
		if (!cwd) return null;
		for (const s of tmuxSnapshot.sessions) {
			for (const w of s.windows ?? []) {
				for (const p of w.panes ?? []) {
					if (p.process?.cwd !== cwd) continue;
					const cmd = (p.process?.command ?? "").toLowerCase();
					if (!cmd.includes("claude")) continue;
					return p.paneId;
				}
			}
		}
		return null;
	});

	const effectivePaneId = $derived(pane?.paneId ?? fallbackPaneId);
	const effectiveSessionLabel = $derived.by((): string | null => {
		if (pane?.window?.session?.name) return pane.window.session.name;
		if (!fallbackPaneId) return null;
		for (const s of tmuxSnapshot.sessions) {
			for (const w of s.windows ?? []) {
				for (const p of w.panes ?? []) {
					if (p.paneId === fallbackPaneId) return s.name;
				}
			}
		}
		return null;
	});

	const title = $derived(
		conversation?.agentName ||
			conversation?.customTitle ||
			worktree?.branch ||
			conversation?.cwd ||
			pane?.window.name ||
			titleHint ||
			session?.sessionUuid.slice(0, 8) ||
			paneId ||
			"session",
	);
	const isHere = $derived(
		!!pane && tmuxSnapshot.activePaneIds.has(pane.paneId),
	);
	const hasTranscript = $derived(!!conversation?.jsonlPath);

	// PR signal flags — same source-of-truth as SidebarItem so the focus
	// view header matches what the sidebar row showed.
	const ciBad = $derived(worktree?.pr?.statusCheckRollup === "FAILURE");
	const reviewBad = $derived(worktree?.pr?.reviewDecision === "CHANGES_REQUESTED");
	const conflict = $derived(
		worktree?.pr?.mergeable === "CONFLICTING" ||
			worktree?.pr?.mergeStateStatus === "DIRTY",
	);
	const prState = $derived(worktree?.pr?.state?.toUpperCase() ?? null);
	const isDraft = $derived(prState === "DRAFT");

	const attachArgv = $derived.by((): string[] | null => {
		if (!pane) return null;
		const sessName = pane.window.session.name;
		// `select-pane` repoints the *current* tmux client to the right
		// pane (in case the user had something else focused), and the
		// `attach-session` is what the local PTY runs to mirror it.
		return [
			"sh",
			"-c",
			`tmux select-pane -t ${pane.paneId} 2>/dev/null; exec tmux attach-session -t ${sessName}`,
		];
	});

	const signalBadgeBase =
		"text-[10px] px-1.5 py-px rounded-[3px] font-[var(--font-mono)] border-[0.5px]";
	const signalBadgeRed =
		"bg-[rgba(255,100,100,0.14)] text-[#ff7272] border-[rgba(255,100,100,0.32)]";

	/**
	 * REPL state pill derivation.
	 *
	 * Maps daemon ClaudeInstance fields to a human label + visual variant:
	 *   idle       — green dot. state === "idle"
	 *   working    — pulsing green. state === "working" AND inflightToolCount === 0
	 *   responding — pulsing amber. state === "working" AND inflightToolCount > 0
	 *   thinking   — slow amber. state === "input" (Claude waiting on user)
	 *   stalled    — red dot. state === "stalled"
	 *   dead       — grey line-through. state === "dead" or "no_claude"
	 *   derived    — grey dot, no label. no live claudeInstance
	 */
	type ReplState = "idle" | "working" | "responding" | "thinking" | "stalled" | "dead" | "derived";

	const replState = $derived.by((): ReplState => {
		if (!session) return "derived";
		const st = session.state;
		if (st === "dead" || st === "no_claude") return "dead";
		if (st === "stalled") return "stalled";
		if (st === "input") return "thinking";
		if (st === "working") {
			return (session.inflightToolCount ?? 0) > 0 ? "responding" : "working";
		}
		if (st === "idle") return "idle";
		// Unknown state — treat as derived.
		return "derived";
	});

	const replLabel: Record<ReplState, string> = {
		idle:       "idle",
		working:    "working",
		responding: "responding",
		thinking:   "thinking",
		stalled:    "stalled",
		dead:       "dead",
		derived:    "",
	};
</script>

<div
	class="pane"
	class:active
	style:flex="1 1 0"
	style:min-width="0"
	onmousedown={onActivate}
	role="region"
>
	{#if paneCount > 1 || surface === "mobile"}
		<div class="pane-header-bar">
			<button
				class="pane-close iconbtn"
				onclick={(e) => { e.stopPropagation(); onClose(); }}
				aria-label={surface === "mobile" ? "Back to sidebar" : "Close pane"}
			>
				<Icon name={surface === "mobile" ? "arrow-left" : "close"} size={surface === "mobile" ? 14 : 11} />
			</button>
			{#if isLast && onToggleFullscreen}
				<button
					class="pane-fs iconbtn"
					onclick={(e) => { e.stopPropagation(); onToggleFullscreen(); }}
					title={fullscreen ? "Exit focus mode" : "Focus mode"}
				>
					<Icon name={fullscreen ? "minimize" : "maximize"} size={12} />
				</button>
			{/if}
		</div>
	{/if}

	<div class="conv">
		<div class="conv-header">
			<div class="conv-header-row">
				<div class="conv-title-block">
					<div class="conv-title-row">
						<!-- REPL state pill -->
						<span class="repl-pill repl-pill--{replState}" title="REPL state: {replState}" data-repl-state={replState}>
							<span class="repl-dot"></span>
							{#if replLabel[replState]}
								<span class="repl-label">{replLabel[replState]}</span>
							{/if}
						</span>
						<span class="conv-title">{title}</span>
						{#if isHere}
							<span class="here-badge mono">here</span>
						{/if}
						{#if pane || hasTranscript}
							<span class="ml-auto">
								<ViewSwitcher
									value={view}
									onChange={(v) => onSetView(v)}
									variant="segmented"
								/>
							</span>
						{/if}
					</div>
					<div class="conv-sub mono dimer">
						{#if worktree}
							<span class="conv-chip" title="Host">
								<HostGlyph host={worktree.host} size={11} />
								<span>{worktree.host}</span>
							</span>
							<span class="conv-chip" title="Branch">
								<Icon name="git-branch" size={10} />
								<span>{worktree.branch}</span>
							</span>
							{#if worktree.pr}
								<a class="conv-chip" href="https://github.com/{worktree.repo}/pull/{worktree.pr.number}" target="_blank" rel="noreferrer">
									<Icon name="pull-request" size={10} />
									<span>#{worktree.pr.number}</span>
								</a>
								{#if isDraft}
									<span
										class="{signalBadgeBase} bg-[rgba(140,140,140,0.18)] text-[#aaa] border-[rgba(140,140,140,0.32)]"
										title="Draft PR"
									>draft</span>
								{:else if prState === "MERGED"}
									<span
										class="{signalBadgeBase} bg-[rgba(120,80,200,0.18)] text-[#b990ff] border-[rgba(120,80,200,0.32)]"
										title="PR merged"
									>merged</span>
								{:else if prState === "CLOSED"}
									<span
										class="{signalBadgeBase} bg-[rgba(255,100,100,0.18)] text-[#ff7272] border-[rgba(255,100,100,0.32)]"
										title="PR closed"
									>closed</span>
								{/if}
								{#if ciBad}
									<span class="{signalBadgeBase} {signalBadgeRed}" title="CI failing">CI</span>
								{/if}
								{#if reviewBad}
									<span class="{signalBadgeBase} {signalBadgeRed}" title="Review changes requested">review</span>
								{/if}
								{#if conflict}
									<span class="{signalBadgeBase} {signalBadgeRed}" title="Merge conflict">conflict</span>
								{/if}
							{/if}
							{#if worktree.issue}
								<a class="conv-chip" href="https://github.com/{worktree.repo}/issues/{worktree.issue.number}" target="_blank" rel="noreferrer">
									<Icon name="issue" size={10} />
									<span>#{worktree.issue.number}</span>
								</a>
							{/if}
						{/if}
						{#if pane}
							<span class="conv-chip mono" title="{pane.window.session.name} → {pane.window.name} · {pane.paneId}">
								<Icon name="terminal" size={10} />
								<span>{pane.paneId}</span>
								<span class="dimer">·</span>
								<span>{pane.window.name}</span>
							</span>
						{/if}
						{#if session?.sessionUuid}
							<span class="conv-chip mono" title="Session UUID">
								<span class="opacity-70">id</span>
								<span>{session.sessionUuid.slice(0, 6)}…</span>
							</span>
						{/if}
						{#if conversation}
							<span class="conv-chip" title="{conversation.messageCount} messages">
								<Icon name="message" size={10} />
								<span class="tnum">{conversation.messageCount}</span>
							</span>
							{#if conversation.lastSeenAt > 0}
								<span class="conv-chip" title={new Date(conversation.lastSeenAt).toLocaleString()}>
									<Icon name="clock" size={10} />
									<span>{relTime(conversation.lastSeenAt, now)}</span>
								</span>
							{/if}
							{#if conversation.open}
								<span class="conv-chip">
									<span class="pip live"></span>
									<span>open</span>
								</span>
							{/if}
						{/if}
					</div>
					{#if conversation?.recap}
						<div
							class="mono dimer mt-1 text-[11.5px] leading-[1.4] line-clamp-2"
							title={conversation.recap}
						>{conversation.recap}</div>
					{/if}
				</div>
			</div>
		</div>

		{#if loading && !data}
			<div class="conv-empty"><span class="dimer">Loading…</span></div>
		{:else if view === "chat" && hasTranscript && conversation?.jsonlPath}
			<div class="flex-1 min-h-0 flex flex-col">
				<TranscriptView
					path={conversation.jsonlPath}
					sessionUuid={conversation.sessionUuid}
					sessionKey={sessionKey || undefined}
					bind:turnsLength={transcriptTurnsLength}
				/>
				{#if effectivePaneId}
					<SessionComposer
						paneId={effectivePaneId}
						sessionLabel={effectiveSessionLabel ?? undefined}
						sessionKey={sessionKey}
						turnsLength={transcriptTurnsLength}
					/>
				{:else}
					<div class="mono dimer text-center text-[11.5px] px-3.5 py-2.5 border-t-[0.5px] border-line bg-surface">
						No live tmux pane — open Terminal view to attach a fresh client.
					</div>
				{/if}
			</div>
		{:else if view === "terminal" && pane && attachArgv}
			<!--
				TerminalAttach reuses the xterm canvas across argv changes
				and respawns just the PTY child — see TerminalAttach.svelte.
				No keyed remount needed.
			-->
			<TerminalAttach
				argv={attachArgv}
				label={`${pane.window.session.name} → ${pane.window.name} · ${pane.paneId}`}
			/>
		{:else if pane && attachArgv}
			<!-- View=chat fallback when there's no transcript yet. -->
			<TerminalAttach
				argv={attachArgv}
				label={`${pane.window.session.name} → ${pane.window.name} · ${pane.paneId}`}
			/>
		{:else}
			<div class="conv-empty">
				<div class="text-[13px] font-medium text-fg-2">
					{#if session && !pane}
						No live tmux pane for this session.
					{:else}
						No tmux pane resolved.
					{/if}
				</div>
				{#if conversation?.cwd}
					<div class="dimer mono text-[11.5px] mt-1">{conversation.cwd}</div>
				{/if}
			</div>
		{/if}
	</div>
</div>

<style>
	/**
	 * REPL state pill — sits at the left of the title row in the conv header.
	 * Small inline indicator with a colored dot and a label.
	 */
	.repl-pill {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 1px 6px 1px 4px;
		border-radius: 8px;
		font-size: 10px;
		font-family: var(--font-mono, monospace);
		letter-spacing: 0.02em;
		border: 0.5px solid transparent;
		flex: none;
	}
	.repl-dot {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		flex: none;
	}
	.repl-label {
		line-height: 1;
	}

	/* idle — green dot */
	.repl-pill--idle {
		color: #6fd391;
		background: color-mix(in oklab, #6fd391 10%, transparent);
		border-color: color-mix(in oklab, #6fd391 25%, transparent);
	}
	.repl-pill--idle .repl-dot {
		background: #6fd391;
	}

	/* working — pulsing green */
	.repl-pill--working {
		color: #6fd391;
		background: color-mix(in oklab, #6fd391 10%, transparent);
		border-color: color-mix(in oklab, #6fd391 25%, transparent);
	}
	.repl-pill--working .repl-dot {
		background: #6fd391;
		animation: repl-pulse-green 1.6s ease-in-out infinite;
	}

	/* responding — pulsing amber */
	.repl-pill--responding {
		color: #f5c94e;
		background: color-mix(in oklab, #f5c94e 10%, transparent);
		border-color: color-mix(in oklab, #f5c94e 25%, transparent);
	}
	.repl-pill--responding .repl-dot {
		background: #f5c94e;
		animation: repl-pulse-amber 1.6s ease-in-out infinite;
	}

	/* thinking — slow amber pulse */
	.repl-pill--thinking {
		color: #f5c94e;
		background: color-mix(in oklab, #f5c94e 8%, transparent);
		border-color: color-mix(in oklab, #f5c94e 20%, transparent);
	}
	.repl-pill--thinking .repl-dot {
		background: #f5c94e;
		animation: repl-pulse-amber 3s ease-in-out infinite;
	}

	/* stalled — red dot */
	.repl-pill--stalled {
		color: #ff7272;
		background: color-mix(in oklab, #ff7272 10%, transparent);
		border-color: color-mix(in oklab, #ff7272 25%, transparent);
	}
	.repl-pill--stalled .repl-dot {
		background: #ff7272;
	}

	/* dead — grey, label gets line-through */
	.repl-pill--dead {
		color: var(--color-fg-3, #6c707a);
		background: transparent;
		border-color: color-mix(in oklab, var(--color-fg-3, #6c707a) 20%, transparent);
	}
	.repl-pill--dead .repl-dot {
		background: var(--color-fg-3, #6c707a);
	}
	.repl-pill--dead .repl-label {
		text-decoration: line-through;
	}

	/* derived — grey dot only, no label shown */
	.repl-pill--derived {
		color: var(--color-fg-4, #44484f);
		background: transparent;
		border-color: transparent;
		padding-right: 4px;
	}
	.repl-pill--derived .repl-dot {
		background: var(--color-fg-4, #44484f);
	}

	@keyframes repl-pulse-green {
		0%, 100% { box-shadow: 0 0 0 0 color-mix(in oklab, #6fd391 50%, transparent); }
		50%       { box-shadow: 0 0 0 4px transparent; }
	}
	@keyframes repl-pulse-amber {
		0%, 100% { box-shadow: 0 0 0 0 color-mix(in oklab, #f5c94e 50%, transparent); }
		50%       { box-shadow: 0 0 0 4px transparent; }
	}
</style>
