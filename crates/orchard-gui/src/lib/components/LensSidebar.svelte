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
	import { toast } from "$lib/util/toast";
	import {
		attentionStore,
		buildAttentionSections,
	} from "$lib/data/lenses/attention";
	import { recentStore, buildRecentItems } from "$lib/data/lenses/recent";
	import { tmuxStore, buildTmuxSections, buildTmuxSnapshot } from "$lib/data/lenses/tmux";
	import { issueStore, buildIssueSections } from "$lib/data/lenses/issue";
	import { worktreeStore, buildWorktreeSections } from "$lib/data/lenses/worktree";

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
		worktreeStore.fetch();
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

	// Surface attention-lens fetch errors via toast so the user isn't left
	// with a silently empty sidebar (Scenario L208 / #600).
	$effect(() => {
		const errs = $attentionStore.errors;
		if (errs && errs.length > 0) toast.error(errs[0].message);
	});

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

	const worktreeSections = $derived(buildWorktreeSections($worktreeStore.data));
	const worktreeTotal = $derived(
		worktreeSections.reduce((n, s) => n + s.items.length, 0),
	);
	const worktreeLoading = $derived($worktreeStore.fetching);

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

	// ── Collapsible sections ────────────────────────────────────────────
	// Single localStorage key stores all collapsed states as a JSON object.
	// Key schema: "<lens>:<section-id>", e.g. "attention:blocked".
	// Special singles: "recent:recent", "channels:channels".
	// Tauri SPA — no SSR, so localStorage is always available synchronously.
	const LS_KEY = "orchard:sidebar:collapsed";

	function hydrateCollapsed(): Record<string, boolean> {
		try {
			const raw = localStorage.getItem(LS_KEY);
			if (raw) return JSON.parse(raw) as Record<string, boolean>;
		} catch {
			// Malformed JSON — start fresh.
		}
		return {};
	}

	let collapsed: Record<string, boolean> = $state(hydrateCollapsed());

	$effect(() => {
		// Write the current collapsed map back to localStorage whenever it changes.
		localStorage.setItem(LS_KEY, JSON.stringify(collapsed));
	});

	function toggleCollapse(key: string): void {
		collapsed = { ...collapsed, [key]: !collapsed[key] };
	}
</script>

<div class="fleet-list">
	{#if channelRooms.length > 0}
		{@const channelsKey = "channels:channels"}
		<div class="fleet-group" data-kind="channels">
			<button
				class="group-header group-header--btn"
				aria-expanded={!collapsed[channelsKey]}
				onclick={() => toggleCollapse(channelsKey)}
			>
				<span style="display: inline-flex; align-items: center; gap: 6px;">
					<Icon name="message" size={11} />
					<span>Channels</span>
				</span>
				<span style="display: inline-flex; align-items: center; gap: 4px;">
					<span class="count">{channelRooms.length}</span>
					<Icon name={collapsed[channelsKey] ? "chevron-right" : "chevron-down"} size={11} />
				</span>
			</button>
			{#if !collapsed[channelsKey]}
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
			{/if}
		</div>
	{/if}

	{#if lens === "attention"}
		{#each attentionSections as section (section.id)}
			{#if section.items.length > 0}
				{@const attnKey = "attention:" + section.id}
				<div class="fleet-group" data-kind={section.id}>
					<button
						class="group-header group-header--btn"
						class:attn={section.id === "blocked"}
						aria-expanded={!collapsed[attnKey]}
						onclick={() => toggleCollapse(attnKey)}
					>
						<span style="display: inline-flex; align-items: center; gap: 6px;">
							<Icon name={section.id === "blocked" ? "alert" : section.id === "waiting" ? "clock" : "spark"} size={11} />
							<span>{section.label}</span>
						</span>
						<span style="display: inline-flex; align-items: center; gap: 4px;">
							<span class="count">{section.items.length}</span>
							<Icon name={collapsed[attnKey] ? "chevron-right" : "chevron-down"} size={11} />
						</span>
					</button>
					{#if !collapsed[attnKey]}
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
					{/if}
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
		{@const recentKey = "recent:recent"}
		<div class="fleet-group" data-kind="recent">
			<button
				class="group-header group-header--btn"
				aria-expanded={!collapsed[recentKey]}
				onclick={() => toggleCollapse(recentKey)}
			>
				<span style="display: inline-flex; align-items: center; gap: 6px;">
					<Icon name="clock" size={11} />
					<span>Recent</span>
				</span>
				<span style="display: inline-flex; align-items: center; gap: 4px;">
					<span class="count">{recentItems.length}</span>
					<Icon name={collapsed[recentKey] ? "chevron-right" : "chevron-down"} size={11} />
				</span>
			</button>
			{#if !collapsed[recentKey]}
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
			{/if}
		</div>
		{#if recentItems.length === 0}
			<div class="empty-lens">
				<span class="dimer">{recentLoading ? "Loading…" : "No Claude sessions known."}</span>
			</div>
		{/if}
	{:else if lens === "tmux"}
		{#each tmuxSections as section (section.id)}
			{@const tmuxKey = "tmux:" + section.id}
			<div class="fleet-group" data-kind={section.id}>
				<button
					class="group-header group-header--btn"
					aria-expanded={!collapsed[tmuxKey]}
					onclick={() => toggleCollapse(tmuxKey)}
				>
					<span style="display: inline-flex; align-items: center; gap: 6px;">
						<Icon name="terminal" size={11} />
						<span>{section.label}</span>
					</span>
					<span style="display: inline-flex; align-items: center; gap: 4px;">
						<span class="count">{section.items.length}</span>
						<Icon name={collapsed[tmuxKey] ? "chevron-right" : "chevron-down"} size={11} />
					</span>
				</button>
				{#if !collapsed[tmuxKey]}
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
				{/if}
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
			{@const issueKey = "issue:" + section.id}
			<div class="fleet-group" data-kind={section.id}>
				<button
					class="group-header group-header--btn"
					aria-expanded={!collapsed[issueKey]}
					onclick={() => toggleCollapse(issueKey)}
				>
					<span style="display: inline-flex; align-items: center; gap: 6px;">
						<Icon name="issue" size={11} />
						<span>{section.label}</span>
					</span>
					<span style="display: inline-flex; align-items: center; gap: 4px;">
						<span class="count">{section.items.length}</span>
						<Icon name={collapsed[issueKey] ? "chevron-right" : "chevron-down"} size={11} />
					</span>
				</button>
				{#if !collapsed[issueKey]}
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
				{/if}
			</div>
		{/each}
		{#if issueTotal === 0}
			<div class="empty-lens">
				<span class="dimer">
					{issueLoading ? "Loading…" : "No issues with open PRs in scope."}
				</span>
			</div>
		{/if}
	{:else if lens === "worktree"}
		{#each worktreeSections as section (section.id)}
			{@const worktreeKey = "worktree:" + section.id}
			<div class="fleet-group" data-kind={section.id}>
				<button
					class="group-header group-header--btn"
					aria-expanded={!collapsed[worktreeKey]}
					onclick={() => toggleCollapse(worktreeKey)}
				>
					<span style="display: inline-flex; align-items: center; gap: 6px;">
						<Icon name="git-branch" size={11} />
						<span>{section.label}</span>
					</span>
					<span style="display: inline-flex; align-items: center; gap: 4px;">
						<span class="count">{section.items.length}</span>
						<Icon name={collapsed[worktreeKey] ? "chevron-right" : "chevron-down"} size={11} />
					</span>
				</button>
				{#if !collapsed[worktreeKey]}
					{#if section.items.length === 0}
						<div class="empty-section">
							<span class="dimer">No active sessions in this repo.</span>
						</div>
					{:else}
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
					{/if}
				{/if}
			</div>
		{/each}
		{#if worktreeSections.length === 0}
			<div class="empty-lens">
				<span class="dimer">
					{worktreeLoading ? "Loading…" : "No repos in config — run `orchard config init`."}
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
	.empty-section {
		padding: 8px 16px;
		font-size: 11.5px;
	}
	/* Section headers are now <button> elements for a11y (click + Enter/Space). */
	.group-header--btn {
		display: flex;
		align-items: center;
		justify-content: space-between;
		width: 100%;
		background: none;
		border: none;
		padding: 0;
		margin: 0;
		font: inherit;
		color: inherit;
		cursor: pointer;
		text-align: left;
	}
	.group-header--btn:focus-visible {
		outline: 2px solid var(--color-accent, #6366f1);
		outline-offset: -2px;
		border-radius: 2px;
	}
</style>
