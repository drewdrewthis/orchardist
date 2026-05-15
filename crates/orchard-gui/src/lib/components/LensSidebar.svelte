<!--
  Lens-anchored sidebar. Every lens has its own Houdini store; the row
  component is uniform. All five lens stores prefetch on mount so swapping
  lenses is pure render against warm cache.
-->
<script lang="ts">
	import { onMount } from "svelte";
	import SidebarItem from "./SidebarItem.svelte";
	import SidebarSectionHeader from "./SidebarSectionHeader.svelte";
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
	import type { SidebarItem as SidebarItemT, SidebarSection } from "$lib/data/sidebar-item";

	/**
	 * Click target — what the panel needs to render this row. The sidebar
	 * emits identity only (paneId + sessionUuid); the panel runs its own
	 * query.
	 */
	export type SelectTarget = { kind: "session"; paneId?: string; sessionUuid?: string };

	type Props = {
		now: number;
		density: "comfortable" | "compact";
		surface: "desktop" | "mobile";
		onSelect: (target: SelectTarget, ev?: MouseEvent) => void;
	};
	let { now, density, surface, onSelect }: Props = $props();

	const store = getStore();
	const lens = $derived(store.lens);

	// Prefetch all five lenses at mount, in parallel. Lens swap then becomes
	// pure render against the Houdini cache — no spinner-or-empty interstitial.
	// CacheAndNetwork policy revalidates each on subscription updates.
	onMount(() => {
		attentionStore.fetch();
		recentStore.fetch();
		tmuxStore.fetch();
		issueStore.fetch();
		worktreeStore.fetch();
	});

	const attentionSections = $derived(
		buildAttentionSections($attentionStore.data, now),
	);
	const attentionTotal = $derived(
		attentionSections.reduce((n, s) => n + s.items.length, 0),
	);
	const attentionLoading = $derived($attentionStore.fetching);

	// Surface attention-lens fetch errors via toast so the user isn't left
	// with a silently empty sidebar (Scenario L208 / #600). Filter out
	// developer-facing daemon strings (e.g. "use GetPull instead",
	// "EnrichPullRequest graphql errors: ...") — show a friendly message
	// to the user and log the raw daemon detail to console for debugging.
	// Track last-shown so the effect doesn't re-toast on every reactive
	// read in scope.
	let lastAttentionError: string | null = null;
	$effect(() => {
		const raw = $attentionStore.errors?.[0]?.message?.trim() ?? "";
		if (!raw) {
			lastAttentionError = null;
			return;
		}
		if (raw === lastAttentionError) return;
		lastAttentionError = raw;
		console.error("[orchard] attention lens daemon error:", raw);
		const userMsg = friendlyDaemonError(raw);
		if (userMsg) toast.error(userMsg);
	});

	/**
	 * Map raw daemon error strings to user-friendly toasts. Returns null
	 * when the error is purely developer noise that shouldn't reach the
	 * user (it stays in the console log above). Keep the mapping small —
	 * unmapped messages get a generic fallback so the user knows something
	 * is wrong but doesn't see internals.
	 */
	function friendlyDaemonError(raw: string): string | null {
		if (raw.includes("rate limit")) {
			return "GitHub rate limit reached — PR data will catch up shortly.";
		}
		if (raw.includes("use GetPull") || raw.includes("is a pull request")) {
			// Daemon-internal API misuse — don't surface to user, just log.
			return null;
		}
		if (raw.includes("EnrichPullRequest")) {
			return "Couldn't refresh PR status — showing the last known state.";
		}
		// Generic fallback — terse, doesn't leak internals.
		return "Sidebar data is incomplete.";
	}

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

	/**
	 * "here" derivation lifted out of <SidebarItem> so the row stays a
	 * pure renderer. Reads the same tmux snapshot the lens already loaded.
	 */
	function isHere(paneId: string | undefined | null): boolean {
		return !!paneId && tmuxSnapshot.activePaneIds.has(paneId);
	}

	const sel = $derived(store.selection);
	function rowSelected(keys: { paneId?: string | null; sessionUuid?: string | null }) {
		if (!sel || sel.kind !== "session") return false;
		return (
			(!!keys.paneId && !!sel.paneId && keys.paneId === sel.paneId) ||
			(!!keys.sessionUuid && !!sel.sessionUuid && keys.sessionUuid === sel.sessionUuid)
		);
	}

	// ── Collapsible sections ────────────────────────────────────────────
	// Single localStorage key stores all collapsed states as a JSON object.
	// Key schema: "<lens>:<section-id>", e.g. "attention:blocked".
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
		localStorage.setItem(LS_KEY, JSON.stringify(collapsed));
	});

	function toggleCollapse(key: string): void {
		collapsed = { ...collapsed, [key]: !collapsed[key] };
	}

	function attentionIcon(id: string): string {
		if (id === "blocked") return "alert";
		if (id === "waiting") return "clock";
		if (id === "active") return "bolt";
		return "dot";
	}

	function emitSelect(item: SidebarItemT, ev?: MouseEvent) {
		onSelect(
			{
				kind: "session",
				paneId: item.session.pane?.paneId ?? undefined,
				sessionUuid: item.session.sessionUuid ?? undefined,
			},
			ev,
		);
	}
</script>

<div class="sidebar-list">
	{#if lens === "attention"}
		{#each attentionSections as section (section.id)}
			{#if section.items.length > 0}
				{@const key = "attention:" + section.id}
				<section class="sidebar-group" data-kind={section.id}>
					<SidebarSectionHeader
						icon={attentionIcon(section.id)}
						label={section.label}
						count={section.items.length}
						collapsed={!!collapsed[key]}
						attn={section.id === "blocked"}
						onToggle={() => toggleCollapse(key)}
					/>
					{#if !collapsed[key]}
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
								onSelect={(_id, ev) => emitSelect(item, ev)}
							/>
						{/each}
					{/if}
				</section>
			{/if}
		{/each}
		{#if attentionTotal === 0}
			<div class="empty-lens">
				<span>{attentionLoading ? "Loading…" : "No Claude sessions reported by the daemon."}</span>
			</div>
		{/if}
	{:else if lens === "recent"}
		{#if recentItems.length > 0}
			{@const key = "recent:recent"}
			<section class="sidebar-group" data-kind="recent">
				<SidebarSectionHeader
					icon="clock"
					label="recent"
					count={recentItems.length}
					collapsed={!!collapsed[key]}
					onToggle={() => toggleCollapse(key)}
				/>
				{#if !collapsed[key]}
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
							onSelect={(_id, ev) => emitSelect(item, ev)}
						/>
					{/each}
				{/if}
			</section>
		{/if}
		{#if recentItems.length === 0}
			<div class="empty-lens">
				<span>{recentLoading ? "Loading…" : "No Claude sessions known."}</span>
			</div>
		{/if}
	{:else if lens === "tmux"}
		{#each tmuxSections as section (section.id)}
			{#if section.items.length > 0}
				{@const key = "tmux:" + section.id}
				<section class="sidebar-group" data-kind={section.id}>
					<SidebarSectionHeader
						icon="terminal"
						label={section.label}
						count={section.items.length}
						collapsed={!!collapsed[key]}
						onToggle={() => toggleCollapse(key)}
					/>
					{#if !collapsed[key]}
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
								onSelect={(_id, ev) => emitSelect(item, ev)}
							/>
						{/each}
					{/if}
				</section>
			{/if}
		{/each}
		{#if tmuxSections.every((s) => s.items.length === 0)}
			<div class="empty-lens">
				<span>
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
			{#if section.items.length > 0}
				{@const key = "issue:" + section.id}
				<section class="sidebar-group" data-kind={section.id}>
					<SidebarSectionHeader
						icon="issue"
						label={section.label}
						count={section.items.length}
						collapsed={!!collapsed[key]}
						onToggle={() => toggleCollapse(key)}
					/>
					{#if !collapsed[key]}
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
								onSelect={(_id, ev) => emitSelect(item, ev)}
							/>
						{/each}
					{/if}
				</section>
			{/if}
		{/each}
		{#if issueTotal === 0}
			<div class="empty-lens">
				<span>{issueLoading ? "Loading…" : "No issues with open PRs in scope."}</span>
			</div>
		{/if}
	{:else if lens === "worktree"}
		{#each worktreeSections as section (section.id)}
			{#if section.items.length > 0}
				{@const key = "worktree:" + section.id}
				<section class="sidebar-group" data-kind={section.id}>
					<SidebarSectionHeader
						icon="git-branch"
						label={section.label}
						count={section.items.length}
						collapsed={!!collapsed[key]}
						onToggle={() => toggleCollapse(key)}
					/>
					{#if !collapsed[key]}
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
								onSelect={(_id, ev) => emitSelect(item, ev)}
							/>
						{/each}
					{/if}
				</section>
			{/if}
		{/each}
		{#if worktreeTotal === 0}
			<div class="empty-lens">
				<span>{worktreeLoading ? "Loading…" : "No repos in config — run `orchard config init`."}</span>
			</div>
		{/if}
	{/if}
</div>

<style>
	.sidebar-list {
		display: flex;
		flex-direction: column;
		flex: 1 1 auto;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		/* Momentum scroll on mobile Safari/Chrome. */
		-webkit-overflow-scrolling: touch;
		/* Reserve space for mobile FAB so the last row isn't trapped under it. */
		padding-bottom: 80px;
	}
	.sidebar-group {
		display: flex;
		flex-direction: column;
		padding-bottom: 4px;
	}
	.empty-lens {
		padding: 32px 16px;
		text-align: center;
		font-size: 12px;
		color: var(--color-text-dim, #6c707a);
	}
</style>
