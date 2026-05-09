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
	import { onMount } from "svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import TerminalAttach from "./TerminalAttach.svelte";
	import { fetchPanel, type PanelData } from "$lib/data/lenses";
	import { relTime } from "$lib/util/format";
	import { getStore } from "$lib/store.svelte";

	type Props = {
		paneId?: string;
		sessionUuid?: string;
		active: boolean;
		paneCount: number;
		isLast: boolean;
		fullscreen: boolean | null;
		now: number;
		surface: "desktop" | "mobile";
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
		onActivate,
		onClose,
		onToggleFullscreen,
	}: Props = $props();

	const store = getStore();

	let data = $state<PanelData | null>(null);
	let loading = $state(true);

	async function refresh() {
		loading = true;
		data = await fetchPanel({ paneId, sessionUuid });
		loading = false;
	}

	// Re-fetch when the row identity changes.
	$effect(() => {
		// Tracking variables so $effect re-runs.
		void paneId;
		void sessionUuid;
		refresh();
	});

	// 60s safety refresh on the active pane.
	$effect(() => {
		if (!active) return;
		const id = setInterval(refresh, 60_000);
		return () => clearInterval(id);
	});

	const pane = $derived(data?.pane ?? null);
	const session = $derived(data?.session ?? null);
	const conversation = $derived(data?.conversation ?? null);
	const worktree = $derived(data?.worktree ?? null);

	const title = $derived(
		worktree?.branch ||
			conversation?.cwd ||
			pane?.window.name ||
			session?.sessionUuid.slice(0, 8) ||
			paneId ||
			"session",
	);
	const isHere = $derived(
		!!pane && store.lensSnapshots.tmux.activePaneIds.has(pane.paneId),
	);

	function attachArgv(): string[] | null {
		if (!pane) return null;
		const sessName = pane.window.session.name;
		return ["sh", "-c", `tmux select-pane -t ${pane.paneId} 2>/dev/null; exec tmux attach-session -t ${sessName}`];
	}
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
		{:else if pane}
			{@const argv = attachArgv()}
			{#if argv}
				<!--
					Key on paneId so a click that swaps the row identity
					tears down the old PTY and spawns a fresh one. Without
					this the TerminalAttach instance lives across props
					changes and keeps the original tmux client.
				-->
				{#key pane.paneId}
					<TerminalAttach {argv} label={`${pane.window.session.name} → ${pane.window.name} · ${pane.paneId}`} />
				{/key}
			{/if}
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
	.conv-recap {
		margin-top: 4px;
		font-size: 11.5px;
		line-height: 1.4;
		max-height: 2.8em;
		overflow: hidden;
		text-overflow: ellipsis;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		-webkit-box-orient: vertical;
	}
</style>
