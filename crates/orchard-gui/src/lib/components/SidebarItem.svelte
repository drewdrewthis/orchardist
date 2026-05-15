<!--
  SidebarItem — the uniform row used by every sidebar lens (#540 B1).

  Layout shape (single source of truth across lenses):

    ┌─────────────────────────────────────────────┐
    │ [state] title                here  [badges] │  ← title row
    │           branch · #PR · 12m   [reasons]    │  ← meta row
    └─────────────────────────────────────────────┘

  The state pill anchors INLINE in the title row — no more floating. Long
  branches truncate with ellipsis (tooltip carries the full string). Host /
  pid / tmux address fold into the row's `title` attribute on hover; they
  do not eat real estate.
-->
<script lang="ts">
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import { relTime } from "$lib/util/format";
	import type { SidebarItem } from "$lib/data/sidebar-item";

	type Props = {
		item: SidebarItem;
		now: number;
		density: "comfortable" | "compact";
		surface: "desktop" | "mobile";
		selected: boolean;
		/**
		 * True when a tmux client is currently watching this session's
		 * pane. Caller derives from tmux state — this component stays a
		 * pure renderer with no global store coupling.
		 */
		here?: boolean;
		onSelect: (id: string, ev?: MouseEvent) => void;
	};
	let { item, now, density, surface, selected, here = false, onSelect }: Props = $props();

	const stateLabel = $derived(
		item.state === "no_claude" ? "no claude" : item.state,
	);
	const stateGlyph = $derived(
		item.state === "working" ? "●"
			: item.state === "idle" ? "·"
			: item.state === "input" ? "→"
			: item.state === "stalled" ? "⚠"
			: item.state === "dead" ? "✕"
			: "",
	);

	const ci = $derived(item.worktree?.pr?.statusCheckRollup ?? null);
	const ciBad = $derived(ci === "FAILURE" || ci === "ERROR");
	const ciPending = $derived(ci === "PENDING" || ci === "EXPECTED");
	const review = $derived(item.worktree?.pr?.reviewDecision ?? null);
	const reviewBad = $derived(review === "CHANGES_REQUESTED");
	const reviewNeeded = $derived(review === "REVIEW_REQUIRED");
	const reviewApproved = $derived(review === "APPROVED");
	const mergeable = $derived(item.worktree?.pr?.mergeable ?? null);
	const mergeState = $derived(item.worktree?.pr?.mergeStateStatus ?? null);
	const conflict = $derived(
		mergeable === "CONFLICTING" || mergeState === "DIRTY",
	);
	const blocked = $derived(mergeState === "BLOCKED" && !conflict);
	const prState = $derived(item.worktree?.pr?.state?.toUpperCase() ?? null);
	const isDraft = $derived(prState === "DRAFT");
	const issueClosed = $derived(
		item.worktree?.issue?.state?.toUpperCase() === "CLOSED",
	);

	// Branch wins over cwd for meta-row identity. Both fall back through the
	// title (which already follows agentName → customTitle → branch → cwd).
	const branch = $derived(item.worktree?.branch ?? null);
	const cwdFull = $derived(
		item.worktree?.path ?? item.session?.process?.cwd ?? null,
	);
	const cwdBase = $derived(
		cwdFull ? cwdFull.split("/").filter(Boolean).pop() || cwdFull : null,
	);
	const metaPath = $derived(
		branch && branch !== item.title ? branch
			: cwdBase && cwdBase !== item.title ? cwdBase
			: null,
	);

	const repo = $derived(item.worktree?.repo ?? null);

	// Hover tooltip — the secondary metadata folded out of the visible row.
	const hoverTitle = $derived(
		[
			item.worktree?.host,
			repo,
			item.pid != null ? `pid ${item.pid}` : null,
			item.tmuxAddress,
			cwdFull && cwdFull !== branch ? cwdFull : null,
		]
			.filter(Boolean)
			.join(" · "),
	);
</script>

<div
	class="sidebar-item"
	data-selected={selected}
	data-density={density}
	data-here={here}
	data-state={item.state}
	title={hoverTitle}
	onclick={(e) => onSelect(item.id, e)}
	onkeydown={(e) => {
		if (e.key === "Enter" || e.key === " ") {
			e.preventDefault();
			onSelect(item.id);
		}
	}}
	role="button"
	tabindex="0"
>
	<div class="sidebar-item__title-row">
		{#if item.state !== "no_claude"}
			<span class="state-pill state-pill--{item.state}" title={stateLabel}>
				<span class="state-pill__glyph" aria-hidden="true">{stateGlyph}</span>
				<span class="state-pill__label">{item.state}</span>
			</span>
		{/if}
		<span class="sidebar-item__title">{item.title}</span>
		{#if here}
			<span class="badge badge--here" title="A tmux client is currently watching this pane">here</span>
		{/if}
		{#if isDraft}
			<span class="badge badge--draft" title="Draft PR">draft</span>
		{:else if prState === "MERGED"}
			<span class="badge badge--merged" title="PR merged">merged</span>
		{:else if prState === "CLOSED"}
			<span class="badge badge--closed" title="PR closed">closed</span>
		{/if}
	</div>

	<div class="sidebar-item__meta-row">
		{#if surface !== "mobile" && item.worktree?.host}
			<HostGlyph host={item.worktree.host} size={10} />
		{/if}
		{#if metaPath}
			<span class="meta-path mono" title={cwdFull ?? branch ?? undefined}>{metaPath}</span>
		{/if}
		{#if item.worktree?.pr}
			<span class="meta-sep" aria-hidden="true">·</span>
			<span class="mono meta-ref">#{item.worktree.pr.number}</span>
		{/if}
		{#if item.worktree?.issue}
			<span class="meta-sep" aria-hidden="true">·</span>
			<span class="mono meta-ref">
				#{item.worktree.issue.number}{#if issueClosed}<span class="chip chip--red" title="Issue closed">closed</span>{/if}
			</span>
		{/if}
		{#if item.lastActivityMs > 0}
			<span class="meta-sep" aria-hidden="true">·</span>
			<span class="mono meta-age">{relTime(item.lastActivityMs, now)}</span>
		{/if}

		{#if ciBad}
			<span class="chip chip--red" title="CI failing">CI</span>
		{:else if ciPending}
			<span class="chip chip--amber" title="CI in progress">CI…</span>
		{/if}
		{#if reviewBad}
			<span class="chip chip--red" title="Review changes requested">changes</span>
		{:else if reviewNeeded}
			<span class="chip chip--amber" title="Awaiting review">review</span>
		{:else if reviewApproved}
			<span class="chip chip--green" title="Review approved">approved</span>
		{/if}
		{#if conflict}
			<span class="chip chip--red" title="Merge conflict">conflict</span>
		{:else if blocked}
			<span class="chip chip--amber" title="Merge blocked (required checks / branch protection)">blocked</span>
		{/if}
		{#each item.reasons as r}
			<span class="chip chip--amber" title={r}>{r}</span>
		{/each}
	</div>
</div>

<style>
	.sidebar-item {
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: 7px 12px 7px 14px;
		border-left: 2px solid transparent;
		cursor: pointer;
		min-width: 0;
		transition: background-color 80ms ease, border-color 80ms ease;
	}
	.sidebar-item[data-density="compact"] {
		padding: 5px 12px 5px 14px;
	}
	.sidebar-item:hover {
		background: var(--color-surface-2, rgba(255, 255, 255, 0.025));
	}
	.sidebar-item[data-selected="true"] {
		background: var(--color-surface-2, rgba(255, 255, 255, 0.04));
	}
	.sidebar-item[data-here="true"] {
		border-left-color: rgba(110, 211, 145, 0.55);
	}
	.sidebar-item[data-selected="true"][data-here="false"] {
		border-left-color: var(--color-accent, #6366f1);
	}

	.sidebar-item__title-row {
		display: flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
	}
	.sidebar-item__title {
		flex: 1 1 auto;
		min-width: 0;
		font-size: 13px;
		font-weight: 500;
		color: var(--color-text, #e4e6eb);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.sidebar-item__meta-row {
		display: flex;
		align-items: center;
		gap: 5px;
		min-width: 0;
		font-size: 11px;
		color: var(--color-text-dim, #797d86);
		overflow: hidden;
		padding-left: 0;
	}
	.meta-path {
		min-width: 0;
		flex: 1 1 auto;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		font-size: 10.5px;
	}
	.meta-ref {
		font-size: 10.5px;
		flex: none;
	}
	.meta-age {
		font-size: 10.5px;
		flex: none;
		color: var(--color-text-dim, #6c707a);
	}
	.meta-sep {
		color: var(--color-text-dimmer, #4f535b);
		flex: none;
	}
	.mono {
		font-family: var(--font-mono, ui-monospace, SFMono-Regular, monospace);
	}

	/* State pill anchored inline with the title — no float. */
	.state-pill {
		display: inline-flex;
		align-items: center;
		gap: 3px;
		font-size: 9.5px;
		padding: 1px 5px;
		border-radius: 3px;
		flex: none;
		font-family: var(--font-mono, ui-monospace, monospace);
		line-height: 1.4;
		text-transform: lowercase;
		letter-spacing: 0.02em;
	}
	.state-pill__glyph {
		font-size: 10px;
		line-height: 1;
	}
	.state-pill--working {
		background: rgba(110, 211, 145, 0.10);
		color: #7bd99c;
		border: 0.5px solid rgba(110, 211, 145, 0.28);
	}
	.state-pill--idle {
		background: rgba(160, 160, 160, 0.06);
		color: #8e9098;
		border: 0.5px solid rgba(160, 160, 160, 0.18);
	}
	.state-pill--input {
		background: rgba(255, 170, 50, 0.16);
		color: #ffb451;
		border: 0.5px solid rgba(255, 170, 50, 0.40);
		font-weight: 600;
	}
	.state-pill--stalled {
		background: rgba(255, 100, 100, 0.10);
		color: #ff8585;
		border: 0.5px solid rgba(255, 100, 100, 0.28);
	}
	.state-pill--dead {
		background: rgba(100, 100, 100, 0.08);
		color: #6a6d76;
		border: 0.5px solid rgba(100, 100, 100, 0.20);
		text-decoration: line-through;
	}

	/* Chips for reasons and PR/CI signals. Compact, color carries meaning. */
	.chip {
		font-size: 9.5px;
		padding: 0 5px;
		border-radius: 3px;
		flex: none;
		font-family: var(--font-mono, ui-monospace, monospace);
		line-height: 1.5;
		text-transform: lowercase;
	}
	.chip--amber {
		background: rgba(255, 180, 80, 0.10);
		color: #ffb851;
		border: 0.5px solid rgba(255, 180, 80, 0.28);
	}
	.chip--red {
		background: rgba(255, 100, 100, 0.10);
		color: #ff8585;
		border: 0.5px solid rgba(255, 100, 100, 0.28);
		margin-left: 3px;
	}
	.chip--green {
		background: rgba(120, 200, 130, 0.10);
		color: #7bd99c;
		border: 0.5px solid rgba(120, 200, 130, 0.28);
	}

	/* Title-row badges */
	.badge {
		font-size: 9.5px;
		padding: 0 5px;
		border-radius: 3px;
		flex: none;
		font-family: var(--font-mono, ui-monospace, monospace);
		line-height: 1.5;
		text-transform: lowercase;
		letter-spacing: 0.02em;
	}
	.badge--here {
		background: rgba(110, 211, 145, 0.14);
		color: #7bd99c;
		border: 0.5px solid rgba(110, 211, 145, 0.38);
	}
	.badge--draft {
		background: rgba(140, 140, 140, 0.12);
		color: #9ea2aa;
		border: 0.5px solid rgba(140, 140, 140, 0.26);
	}
	.badge--merged {
		background: rgba(120, 80, 200, 0.14);
		color: #b990ff;
		border: 0.5px solid rgba(120, 80, 200, 0.30);
	}
	.badge--closed {
		background: rgba(255, 100, 100, 0.14);
		color: #ff8585;
		border: 0.5px solid rgba(255, 100, 100, 0.28);
	}
</style>
