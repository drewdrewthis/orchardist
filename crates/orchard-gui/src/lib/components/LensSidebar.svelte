<!--
  Lens-anchored sidebar. Each of the four lenses has its own snapshot
  in the store and its own row layout — there is no shared Item[] that
  gets reshuffled. Channels are rendered above the lens content.
-->
<script lang="ts">
	import { onMount } from "svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import SessionRow from "./SessionRow.svelte";
	import TmuxPaneRow from "./TmuxPaneRow.svelte";
	import IssueRow from "./IssueRow.svelte";
	import ChannelRow from "./ChannelRow.svelte";
	import { getStore } from "$lib/store.svelte";
	import { attentionStore, buildAttentionRows } from "$lib/data/lenses/attention";
	import { recentStore, buildRecentRows } from "$lib/data/lenses/recent";
	import { relTime } from "$lib/util/format";
	import type { Agent } from "$lib/data/types";

	/**
	 * Click target — what the panel needs to render this row. Either a
	 * channel roomId or a session keyed by paneId + sessionUuid. The
	 * sidebar emits identity only; the panel runs its own query.
	 */
	export type SelectTarget =
		| { kind: "channel"; roomId: string }
		| { kind: "session"; paneId?: string; sessionUuid?: string };

	type Props = {
		now: number;
		density: "comfortable" | "compact";
		surface: "desktop" | "mobile";
		selectedId: string | null;
		agents: Agent[];
		onSelect: (target: SelectTarget, ev?: MouseEvent) => void;
	};
	let { now, density, surface, selectedId, agents, onSelect }: Props = $props();

	const store = getStore();
	const lens = $derived(store.lens);

	// Houdini stores: kick off CacheAndNetwork fetches on mount; the
	// component subscribes via the `$<storeName>` reactive contract.
	// Subsequent push events into the daemon (subscribeAll → cache patch)
	// re-render the sidebar without an explicit re-fetch.
	onMount(() => {
		attentionStore.fetch();
		recentStore.fetch();
	});

	// Tier-classified rows derived from the Houdini store + the parent's
	// `now` tick (so "idle 5m" updates as time passes).
	const attentionRows = $derived(buildAttentionRows($attentionStore.data, now));
	const blocked = $derived(attentionRows.filter((r) => r.tier === "blocked"));
	const waiting = $derived(attentionRows.filter((r) => r.tier === "waiting"));
	const active = $derived(attentionRows.filter((r) => r.tier === "active"));
	const attentionLoading = $derived($attentionStore.fetching);

	const recentRows = $derived(buildRecentRows($recentStore.data));
	const recentLoading = $derived($recentStore.fetching);

	/**
	 * A sidebar row matches the active tab if EITHER its paneId or its
	 * sessionUuid is in the active tab's selection keys. Different lens
	 * snapshots populate different handles for the same conversation.
	 */
	const sel = $derived(store.selection);
	function rowSelected(keys: { paneId?: string | null; sessionUuid?: string | null; channelId?: string | null }) {
		if (!sel) return false;
		if (sel.kind === "channel") return !!keys.channelId && keys.channelId === sel.roomId;
		const sessionKeyMatch =
			(!!keys.paneId && !!sel.paneId && keys.paneId === sel.paneId) ||
			(!!keys.sessionUuid && !!sel.sessionUuid && keys.sessionUuid === sel.sessionUuid);
		return sessionKeyMatch;
	}

	// Channels (chat rooms) are shown across all lenses at the top —
	// their relevance is independent of the lens filter.
	const channelItems = $derived(
		store.mergedItems.filter((it) => it.kind === "channel"),
	);
</script>

<div class="fleet-list">
	{#if channelItems.length > 0}
		<div class="fleet-group" data-kind="channels">
			<div class="group-header">
				<span style="display: inline-flex; align-items: center; gap: 6px;">
					<Icon name="message" size={11} />
					<span>Channels</span>
				</span>
				<span class="count">{channelItems.length}</span>
			</div>
			{#each channelItems as ch (ch.id)}
				{#if ch.kind === "channel"}
					<ChannelRow
						item={ch}
						selected={rowSelected({ channelId: ch.id })}
						{density}
						{surface}
						{agents}
						onSelect={(_id, ev) => onSelect({ kind: "channel", roomId: ch.id }, ev)}
					/>
				{/if}
			{/each}
		</div>
	{/if}

	{#if lens === "attention"}
		{#each [
			{ key: "blocked", label: "Blocked", icon: "alert", rows: blocked },
			{ key: "waiting", label: "Waiting", icon: "clock", rows: waiting },
			{ key: "active", label: "Active", icon: "spark", rows: active },
		] as group (group.key)}
			{#if group.rows.length > 0}
				<div class="fleet-group" data-kind={group.key}>
					<div class="group-header" class:attn={group.key === "blocked"}>
						<span style="display: inline-flex; align-items: center; gap: 6px;">
							<Icon name={group.icon} size={11} />
							<span>{group.label}</span>
						</span>
						<span class="count">{group.rows.length}</span>
					</div>
					{#each group.rows as row (row.session.id)}
						<SessionRow
							session={row.session}
							worktree={row.worktree}
							reasons={row.reasons}
							lastActivityMs={row.lastActivityMs}
							{now}
							{density}
							{surface}
							selected={rowSelected({
								paneId: row.session.pane?.paneId,
								sessionUuid: row.session.sessionUuid,
							})}
							onSelect={(_id, ev) => onSelect({
								kind: "session",
								paneId: row.session.pane?.paneId,
								sessionUuid: row.session.sessionUuid,
							}, ev)}
						/>
					{/each}
				</div>
			{/if}
		{/each}
		{#if attentionRows.length === 0}
			<div class="empty-lens">
				<span class="dimer">{attentionLoading ? "Loading…" : "No Claude sessions reported by the daemon."}</span>
			</div>
		{/if}
	{:else if lens === "recent"}
		<div class="fleet-group" data-kind="recent">
			<div class="group-header">
				<span style="display: inline-flex; align-items: center; gap: 6px;">
					<Icon name="clock" size={11} />
					<span>Recent</span>
				</span>
				<span class="count">{recentRows.length}</span>
			</div>
			{#each recentRows as row (row.session.id)}
				<SessionRow
					session={row.session}
					worktree={null}
					reasons={row.messageCount > 0 ? [`${row.messageCount} msgs`] : []}
					lastActivityMs={row.lastActivityMs}
					{now}
					{density}
					{surface}
					selected={rowSelected({
						paneId: row.session.pane?.paneId,
						sessionUuid: row.session.sessionUuid,
					})}
					onSelect={(_id, ev) => onSelect({
						kind: "session",
						paneId: row.session.pane?.paneId,
						sessionUuid: row.session.sessionUuid,
					}, ev)}
				/>
			{/each}
		</div>
		{#if recentRows.length === 0}
			<div class="empty-lens">
				<span class="dimer">{recentLoading ? "Loading…" : "No Claude sessions known."}</span>
			</div>
		{/if}
	{:else if lens === "tmux"}
		{#each store.lensSnapshots.tmux.sessions as sess (sess.id)}
			<div class="fleet-group" data-kind="tmux-session">
				<div class="group-header">
					<span style="display: inline-flex; align-items: center; gap: 6px;">
						<Icon name="terminal" size={11} />
						<span>{sess.name}</span>
						{#if sess.activeAttached}
							<span class="here-pip" title="A client is currently watching this session"></span>
						{/if}
					</span>
					<span class="count">
						{sess.windows.reduce((n, w) => n + w.panes.length, 0)}
					</span>
				</div>
				{#each sess.windows as win (win.id)}
					<div class="fleet-subgroup">
						<div class="subgroup-header">
							<span class="subgroup-rule"></span>
							<span class="mono dimer" style:font-size="10.5px" style:letter-spacing="0.2px">
								window {win.index} · {win.name}
							</span>
							<span class="dimest mono" style:font-size="10.5px">{win.panes.length}</span>
						</div>
						{#each win.panes as pane (pane.paneId)}
							<div class="fleet-nested">
								<TmuxPaneRow
									pane={pane}
									here={store.lensSnapshots.tmux.activePaneIds.has(pane.paneId)}
									{now}
									{density}
									{surface}
									selected={rowSelected({
										paneId: pane.paneId,
										sessionUuid: pane.claudeInstance?.sessionUuid,
									})}
									onSelect={(_id, ev) => onSelect({
										kind: "session",
										paneId: pane.paneId,
										sessionUuid: pane.claudeInstance?.sessionUuid,
									}, ev)}
								/>
							</div>
						{/each}
					</div>
				{/each}
			</div>
		{/each}
		{#if store.lensSnapshots.tmux.sessions.length === 0}
			<div class="empty-lens">
				<span class="dimer">
					{#if !store.lensSnapshots.tmux.alive && !store.lensLoading}
						No tmux server reachable.
					{:else if store.lensLoading}
						Loading…
					{:else}
						No tmux sessions.
					{/if}
				</span>
			</div>
		{/if}
	{:else if lens === "issue"}
		<div class="fleet-group" data-kind="issue">
			<div class="group-header">
				<span style="display: inline-flex; align-items: center; gap: 6px;">
					<Icon name="issue" size={11} />
					<span>Open work</span>
				</span>
				<span class="count">{store.lensSnapshots.issue.length}</span>
			</div>
			{#each store.lensSnapshots.issue as row (row.worktree.id)}
				<IssueRow
					issue={row.issue}
					worktree={row.worktree}
					session={row.session}
					{now}
					{density}
					{surface}
					selected={rowSelected({
						paneId: row.session?.pane?.paneId,
						sessionUuid: row.session?.sessionUuid,
					})}
					onSelect={(_id, ev) => onSelect({
						kind: "session",
						paneId: row.session?.pane?.paneId,
						sessionUuid: row.session?.sessionUuid,
					}, ev)}
				/>
			{/each}
		</div>
		{#if store.lensSnapshots.issue.length === 0}
			<div class="empty-lens">
				<span class="dimer">
					{store.lensLoading ? "Loading…" : "No issues with open PRs in scope."}
				</span>
			</div>
		{/if}
	{/if}
</div>

<style>
	.empty-lens {
		padding: 36px 16px;
		text-align: center;
		font-size: 13px;
	}
	.here-pip {
		display: inline-block;
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--ok-fg, #6fd391);
		box-shadow: 0 0 6px var(--ok-fg, rgba(111, 211, 145, 0.6));
	}
</style>
