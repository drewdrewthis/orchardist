<!--
  Session pane — given a row identity (paneId and/or sessionUuid), runs
  its own OpenPanel query against the daemon and renders:
    - Header with worktree breadcrumb + PR/issue chips + pane chips
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
	import { onMount } from "svelte";
	import { createPanelStore, buildPanelData } from "$lib/data/lenses/panel";
	import { tmuxStore, buildTmuxSnapshot } from "$lib/data/lenses/tmux";
	import { relTime } from "$lib/util/format";
	import type { ConvView } from "$lib/data/types";

	type Props = {
		paneId?: string;
		sessionUuid?: string;
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

	// `here` flag still needs the tmux server's client → currentPane
	// map. Read straight from the tmux Houdini store (already kicked
	// off by LensSidebar; the cache means this is free).
	const tmuxSnapshot = $derived(buildTmuxSnapshot($tmuxStore.data));

	const title = $derived(
		conversation?.agentName ||
			conversation?.customTitle ||
			worktree?.branch ||
			conversation?.cwd ||
			pane?.window.name ||
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
</script>

<div
	class="pane"
	class:active
	style:flex="1 1 0"
	style:min-width="0"
	onmousedown={onActivate}
	role="region"
>
	{#if paneCount > 1}
		<div class="pane-header-bar">
			<button class="pane-close iconbtn" onclick={(e) => { e.stopPropagation(); onClose(); }} aria-label="Close pane">
				<Icon name="close" size={11} />
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
						<span class="pip {session?.state === 'working' ? 'ok' : 'idle'}"></span>
						<span class="conv-title">{title}</span>
						{#if isHere}
							<span class="here-badge mono">here</span>
						{/if}
						{#if pane || hasTranscript}
							<span style="margin-left: auto;">
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
									<span class="signal-badge draft" title="Draft PR">draft</span>
								{:else if prState === "MERGED"}
									<span class="signal-badge merged" title="PR merged">merged</span>
								{:else if prState === "CLOSED"}
									<span class="signal-badge closed" title="PR closed">closed</span>
								{/if}
								{#if ciBad}
									<span class="signal-badge red" title="CI failing">CI</span>
								{/if}
								{#if reviewBad}
									<span class="signal-badge red" title="Review changes requested">review</span>
								{/if}
								{#if conflict}
									<span class="signal-badge red" title="Merge conflict">conflict</span>
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
								<span style:opacity="0.7">id</span>
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
						<div class="conv-recap mono dimer" title={conversation.recap}>{conversation.recap}</div>
					{/if}
				</div>
			</div>
		</div>

		{#if loading && !data}
			<div class="conv-empty"><span class="dimer">Loading…</span></div>
		{:else if view === "chat" && hasTranscript && conversation?.jsonlPath}
			<div class="chat-stack">
				<TranscriptView path={conversation.jsonlPath} sessionUuid={conversation.sessionUuid} />
				{#if pane?.paneId}
					<SessionComposer paneId={pane.paneId} sessionLabel={pane.window.session.name} />
				{:else}
					<div class="composer-disabled mono dimer">
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
				<div style="font-size: 13px; font-weight: 500; color: var(--fg-2);">
					{#if session && !pane}
						No live tmux pane for this session.
					{:else}
						No tmux pane resolved.
					{/if}
				</div>
				{#if conversation?.cwd}
					<div class="dimer mono" style="font-size: 11.5px; margin-top: 4px;">{conversation.cwd}</div>
				{/if}
			</div>
		{/if}
	</div>
</div>

<style>
	.signal-badge {
		font-size: 10px;
		padding: 1px 6px;
		border-radius: 3px;
		font-family: var(--font-mono);
	}
	.signal-badge.draft {
		background: rgba(140, 140, 140, 0.18);
		color: #aaa;
		border: 0.5px solid rgba(140, 140, 140, 0.32);
	}
	.signal-badge.merged {
		background: rgba(120, 80, 200, 0.18);
		color: #b990ff;
		border: 0.5px solid rgba(120, 80, 200, 0.32);
	}
	.signal-badge.closed {
		background: rgba(255, 100, 100, 0.18);
		color: #ff7272;
		border: 0.5px solid rgba(255, 100, 100, 0.32);
	}
	.signal-badge.red {
		background: rgba(255, 100, 100, 0.14);
		color: #ff7272;
		border: 0.5px solid rgba(255, 100, 100, 0.32);
	}
	.chat-stack {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
	}
	.composer-disabled {
		padding: 10px 14px;
		border-top: 0.5px solid var(--line);
		background: var(--surface-1);
		font-size: 11.5px;
		text-align: center;
	}
	.conv-recap {
		margin-top: 4px;
		font-size: 11.5px;
		line-height: 1.4;
		max-height: 2.8em;
		overflow: hidden;
		text-overflow: ellipsis;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
	}
</style>
