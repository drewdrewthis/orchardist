<!--
  Lens-anchored sidebar. Each of the four lenses has its own Houdini store
  with its own row layout — there is no shared Item[] that gets reshuffled.
  Channels (chat rooms from chat-core) are rendered above the lens content.
-->
<script lang="ts">
	import { onMount } from "svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import SidebarItem from "./SidebarItem.svelte";
	import ChannelRow from "./ChannelRow.svelte";
	import { getStore } from "$lib/store.svelte";
	import {
		attentionStore,
		buildAttentionSections,
	} from "$lib/data/lenses/attention";
	import { recentStore, buildRecentItems } from "$lib/data/lenses/recent";
	import { tmuxStore, buildTmuxSections, buildTmuxSnapshot } from "$lib/data/lenses/tmux";
	import { issueStore, buildIssueSections } from "$lib/data/lenses/issue";

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
		onSelect: (target: SelectTarget, ev?: MouseEvent) => void;
	};
	let { now, density, surface, onSelect }: Props = $props();

	const store = getStore();
	const lens = $derived(store.lens);

	// Houdini stores: kick off CacheAndNetwork fetches on mount; the
	// component subscribes via the `$<storeName>` reactive contract.
	// Subsequent push events into the daemon (subscribeAll → cache patch)
	// re-render the sidebar without an explicit re-fetch.
	onMount(() => {
		attentionStore.fetch();
		recentStore.fetch();
		tmuxStore.fetch();
		issueStore.fetch();
	});

	// All four lenses produce the same shape per #540 B0/B1: sections
	// of `SidebarItem[]`. The lens decides the grouping axis; the item
	// component is uniform.
	const attentionSections = $derived(
		buildAttentionSections($attentionStore.data, now),
	);
	const attentionTotal = $derived(
		attentionSections.reduce((n, s) => n + s.items.length, 0),
	);
	const attentionLoading = $derived($attentionStore.fetching);

	const recentItems = $derived(buildRecentItems($recentStore.data));
	const recentLoading = $derived($recentStore.fetching);

	const tmuxSections = $derived(buildTmuxSections($tmuxStore.data));
	const tmuxSnapshot = $derived(buildTmuxSnapshot($tmuxStore.data));
	const tmuxLoading = $derived($tmuxStore.fetching);

	const issueSections = $derived(buildIssueSections($issueStore.data));
	const issueTotal = $derived(
		issueSections.reduce((n, s) => n + s.items.length, 0),
	);
	const issueLoading = $derived($issueStore.fetching);

	// "here" derivation lifted out of `<SidebarItem>` so the row stays a
	// pure renderer (no global store coupling). Reads the same tmux
	// snapshot the lens already loaded for the tmux lens.
	function isHere(paneId: string | undefined | null): boolean {
		return !!paneId && tmuxSnapshot.activePaneIds.has(paneId);
	}

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

	// Channels (chat rooms from chat-core) live across all lenses at the
	// top — their relevance is independent of the lens filter.
	const channelRooms = $derived(store.chatRooms);
</script>

<div class="fleet-list">
	{#if channelRooms.length > 0}
		<div class="fleet-group" data-kind="channels">
			<div class="group-header">
				<span style="display: inline-flex; align-items: center; gap: 6px;">
					<Icon name="message" size={11} />
					<span>Channels</span>
				</span>
				<span class="count">{channelRooms.length}</span>
			</div>
			{#each channelRooms as ch (ch.id)}
				<ChannelRow
					roomId={ch.id}
					memberCount={ch.memberCount}
					selected={rowSelected({ channelId: ch.id })}
					{density}
					{surface}
					onSelect={(ev) => onSelect({ kind: "channel", roomId: ch.id }, ev)}
				/>
			{/each}
		</div>
	{/if}

	{#if lens === "attention"}
		{#each attentionSections as section (section.id)}
			{#if section.items.length > 0}
				<div class="fleet-group" data-kind={section.id}>
					<div class="group-header" class:attn={section.id === "blocked"}>
						<span style="display: inline-flex; align-items: center; gap: 6px;">
							<Icon name={section.id === "blocked" ? "alert" : section.id === "waiting" ? "clock" : "spark"} size={11} />
							<span>{section.label}</span>
						</span>
						<span class="count">{section.items.length}</span>
					</div>
					{#each section.items as item (item.id)}
						<SidebarItem
							{item}
							{now}
							{density}
							{surface}
							here={isHere(item.session.pane?.paneId)}
							selected={rowSelected({
								paneId: item.session.pane?.paneId,
								sessionUuid: item.session.sessionUuid,
							})}
							onSelect={(_id, ev) => onSelect({
								kind: "session",
								paneId: item.session.pane?.paneId,
								sessionUuid: item.session.sessionUuid,
							}, ev)}
						/>
					{/each}
				</div>
			{/if}
		{/each}
		{#if attentionTotal === 0}
			<div class="empty-lens">
				<span class="dimer">{attentionLoading ? "Loading…" : "No Claude sessions reported by the daemon."}</span>
			</div>
		{/if}
	{:else if lens === "recent"}
		<!-- Recent activity is the only flat lens — no grouping axis (#540 B0). -->
		<div class="fleet-group" data-kind="recent">
			<div class="group-header">
				<span style="display: inline-flex; align-items: center; gap: 6px;">
					<Icon name="clock" size={11} />
					<span>Recent</span>
				</span>
				<span class="count">{recentItems.length}</span>
			</div>
			{#each recentItems as item (item.id)}
				<SidebarItem
					{item}
					{now}
					{density}
					{surface}
					here={isHere(item.session.pane?.paneId)}
					selected={rowSelected({
						paneId: item.session.pane?.paneId,
						sessionUuid: item.session.sessionUuid,
					})}
					onSelect={(_id, ev) => onSelect({
						kind: "session",
						paneId: item.session.pane?.paneId,
						sessionUuid: item.session.sessionUuid,
					}, ev)}
				/>
			{/each}
		</div>
		{#if recentItems.length === 0}
			<div class="empty-lens">
				<span class="dimer">{recentLoading ? "Loading…" : "No Claude sessions known."}</span>
			</div>
		{/if}
	{:else if lens === "tmux"}
		{#each tmuxSections as section (section.id)}
			<div class="fleet-group" data-kind={section.id}>
				<div class="group-header">
					<span style="display: inline-flex; align-items: center; gap: 6px;">
						<Icon name="terminal" size={11} />
						<span>{section.label}</span>
					</span>
					<span class="count">{section.items.length}</span>
				</div>
				{#each section.items as item (item.id)}
					<SidebarItem
						{item}
						{now}
						{density}
						{surface}
						here={isHere(item.session.pane?.paneId)}
						selected={rowSelected({
							paneId: item.session.pane?.paneId,
							sessionUuid: item.session.sessionUuid,
						})}
						onSelect={(_id, ev) => onSelect({
							kind: "session",
							paneId: item.session.pane?.paneId,
							sessionUuid: item.session.sessionUuid,
						}, ev)}
					/>
				{/each}
			</div>
		{/each}
		{#if tmuxSections.length === 0}
			<div class="empty-lens">
				<span class="dimer">
					{#if !tmuxSnapshot.alive && !tmuxLoading}
						No tmux server reachable.
					{:else if tmuxLoading}
						Loading…
					{:else}
						No tmux sessions.
					{/if}
				</span>
			</div>
		{/if}
	{:else if lens === "issue"}
		{#each issueSections as section (section.id)}
			<div class="fleet-group" data-kind={section.id}>
				<div class="group-header">
					<span style="display: inline-flex; align-items: center; gap: 6px;">
						<Icon name="issue" size={11} />
						<span>{section.label}</span>
					</span>
					<span class="count">{section.items.length}</span>
				</div>
				{#each section.items as item (item.id)}
					<SidebarItem
						{item}
						{now}
						{density}
						{surface}
						here={isHere(item.session.pane?.paneId)}
						selected={rowSelected({
							paneId: item.session.pane?.paneId,
							sessionUuid: item.session.sessionUuid,
						})}
						onSelect={(_id, ev) => onSelect({
							kind: "session",
							paneId: item.session.pane?.paneId,
							sessionUuid: item.session.sessionUuid,
						}, ev)}
					/>
				{/each}
			</div>
		{/each}
		{#if issueTotal === 0}
			<div class="empty-lens">
				<span class="dimer">
					{issueLoading ? "Loading…" : "No issues with open PRs in scope."}
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
