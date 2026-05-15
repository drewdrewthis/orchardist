<!--
  SidebarItem — uniform row used by every sidebar lens.

    ┌───┬─────────────────────────────────────────────────┐
    │ ● │ title text                              [badge] │  row 1
    │   │ path · branch                                   │  row 2
    │   │ #PR · age              [● ● ●] reasons          │  row 3
    └───┴─────────────────────────────────────────────────┘

  Left gutter is a fixed 14px column with a state dot — colored by state,
  no text. Title row always starts at the same x regardless of state.
  Three metadata tiers below: identity (path/branch), refs+age, and
  status glyphs / reasons. Hover-title carries host/pid/tmux address.
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
		here?: boolean;
		onSelect: (id: string, ev?: MouseEvent) => void;
	};
	let { item, now, density, surface, selected, here = false, onSelect }: Props = $props();

	const stateLabel = $derived(
		item.state === "no_claude" ? "no session" : item.state,
	);

	// PR signal derivation (single source of truth — lenses don't re-emit).
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

	// Identity tier — what the row IS. Path/branch.
	const branch = $derived(item.worktree?.branch ?? null);
	const cwdFull = $derived(
		item.worktree?.path ?? item.session?.process?.cwd ?? null,
	);
	const cwdBase = $derived(
		cwdFull ? cwdFull.split("/").filter(Boolean).pop() || cwdFull : null,
	);
	const identityPath = $derived(
		branch && branch !== item.title ? branch
			: cwdBase && cwdBase !== item.title ? cwdBase
			: null,
	);

	const repo = $derived(item.worktree?.repo ?? null);

	// Hover tooltip — the absolute secondary metadata that doesn't earn
	// pixels: host, repo, pid, tmux address, full cwd.
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

	// Does the row have ANY tier-3 signal (status glyph or reason chip)?
	// Drives whether we render row 3 at all.
	const hasStatusRow = $derived(
		ciBad || ciPending || reviewBad || reviewNeeded || reviewApproved ||
			conflict || blocked || item.reasons.length > 0 ||
			!!item.worktree?.pr || (item.lastActivityMs > 0),
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
	<!-- Left gutter: state dot, always present (transparent for no_claude
	     to keep the title-start column aligned). -->
	<div class="sidebar-item__gutter" title={stateLabel}>
		<span class="state-dot state-dot--{item.state}" aria-label={stateLabel}></span>
	</div>

	<div class="sidebar-item__body">
		<!-- Row 1: title (winning typography) + PR state badge on the far right. -->
		<div class="sidebar-item__title-row">
			<span class="sidebar-item__title">{item.title}</span>
			{#if here}
				<span class="title-badge title-badge--here" title="A tmux client is currently watching this pane">here</span>
			{/if}
			{#if isDraft}
				<span class="title-badge title-badge--draft" title="Draft PR">draft</span>
			{:else if prState === "MERGED"}
				<span class="title-badge title-badge--merged" title="PR merged">merged</span>
			{:else if prState === "CLOSED"}
				<span class="title-badge title-badge--closed" title="PR closed">closed</span>
			{/if}
		</div>

		<!-- Row 2: identity tier — what the row IS. Path / branch. Truncates. -->
		{#if identityPath}
			<div class="sidebar-item__identity-row">
				<span class="identity-path mono" title={cwdFull ?? branch ?? undefined}>{identityPath}</span>
			</div>
		{/if}

		<!-- Row 3: refs + age + status glyphs. Distinct visual tier — mono,
		     dim. Status glyphs are colored dots/letters; reasons stay text. -->
		{#if hasStatusRow}
			<div class="sidebar-item__status-row">
				<div class="status-refs">
					{#if surface !== "mobile" && item.worktree?.host}
						<HostGlyph host={item.worktree.host} size={9} />
					{/if}
					{#if item.worktree?.pr}
						<span class="mono ref">#{item.worktree.pr.number}</span>
					{/if}
					{#if item.worktree?.issue}
						<span class="mono ref">i{item.worktree.issue.number}</span>
					{/if}
					{#if item.lastActivityMs > 0}
						<span class="mono age">{relTime(item.lastActivityMs, now)}</span>
					{/if}
				</div>

				<div class="status-glyphs">
					{#if ciBad}
						<span class="glyph glyph--red" title="CI failing">CI</span>
					{:else if ciPending}
						<span class="glyph glyph--amber" title="CI in progress">CI</span>
					{/if}
					{#if reviewBad}
						<span class="glyph glyph--red" title="Review: changes requested">R</span>
					{:else if reviewNeeded}
						<span class="glyph glyph--amber" title="Review needed">R</span>
					{:else if reviewApproved}
						<span class="glyph glyph--green" title="Review approved">R</span>
					{/if}
					{#if conflict}
						<span class="glyph glyph--red" title="Merge conflict">M</span>
					{:else if blocked}
						<span class="glyph glyph--amber" title="Merge blocked">M</span>
					{/if}
					{#if issueClosed}
						<span class="glyph glyph--red" title="Issue closed">i</span>
					{/if}
					{#each item.reasons as r}
						<span class="glyph glyph--amber glyph--text" title={r}>{r}</span>
					{/each}
				</div>
			</div>
		{/if}
	</div>
</div>

<style>
	.sidebar-item {
		display: grid;
		grid-template-columns: 14px 1fr;
		column-gap: 8px;
		padding: 8px 12px 8px 10px;
		border-left: 2px solid transparent;
		cursor: pointer;
		min-width: 0;
		transition: background-color 80ms ease, border-color 80ms ease;
	}
	.sidebar-item[data-density="compact"] {
		padding: 5px 12px 5px 10px;
	}
	.sidebar-item:hover {
		background: var(--color-surface-2, rgba(255, 255, 255, 0.025));
	}
	.sidebar-item[data-selected="true"] {
		background: var(--color-surface-2, rgba(255, 255, 255, 0.045));
	}
	.sidebar-item[data-here="true"] {
		border-left-color: rgba(110, 211, 145, 0.55);
	}
	.sidebar-item[data-selected="true"][data-here="false"] {
		border-left-color: var(--color-accent, #6366f1);
	}

	/* ── Left gutter ───────────────────────────────────────── */
	.sidebar-item__gutter {
		display: flex;
		align-items: flex-start;
		justify-content: center;
		padding-top: 6px;
		min-width: 14px;
	}
	.state-dot {
		display: inline-block;
		width: 7px;
		height: 7px;
		border-radius: 50%;
		flex: none;
	}
	.state-dot--working {
		background: #6fd391;
		box-shadow: 0 0 6px rgba(110, 211, 145, 0.55);
		animation: pulse-green 2.2s ease-in-out infinite;
	}
	.state-dot--idle {
		background: transparent;
		border: 1.5px solid #6c707a;
	}
	.state-dot--input {
		background: #ffb451;
		box-shadow: 0 0 6px rgba(255, 180, 80, 0.55);
		animation: pulse-amber 1.2s ease-in-out infinite;
	}
	.state-dot--stalled {
		background: #ff7272;
		box-shadow: 0 0 6px rgba(255, 100, 100, 0.55);
	}
	.state-dot--dead {
		background: transparent;
		border: 1.5px solid #4f535b;
	}
	.state-dot--no_claude {
		background: transparent;
		border: 1px dashed rgba(120, 124, 134, 0.35);
	}
	@keyframes pulse-green {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.55; }
	}
	@keyframes pulse-amber {
		0%, 100% { opacity: 1; transform: scale(1); }
		50% { opacity: 0.7; transform: scale(0.85); }
	}

	/* ── Body ──────────────────────────────────────────────── */
	.sidebar-item__body {
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	/* Row 1 — title. The visual winner. */
	.sidebar-item__title-row {
		display: flex;
		align-items: baseline;
		gap: 6px;
		min-width: 0;
	}
	.sidebar-item__title {
		flex: 1 1 auto;
		min-width: 0;
		font-family: "Geist", ui-sans-serif, system-ui, sans-serif;
		font-size: 13px;
		font-weight: 500;
		letter-spacing: -0.005em;
		color: var(--color-text, #e8eaed);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		line-height: 1.3;
	}

	/* Row 2 — identity. What the row IS. */
	.sidebar-item__identity-row {
		display: flex;
		min-width: 0;
		font-size: 10.5px;
		color: var(--color-text-dim, #797d86);
		line-height: 1.4;
	}
	.identity-path {
		min-width: 0;
		max-width: 100%;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.mono {
		font-family: "Geist Mono", ui-monospace, SFMono-Regular, monospace;
	}

	/* Row 3 — refs + status. Two halves: left=facts, right=alerts. */
	.sidebar-item__status-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		min-width: 0;
		font-size: 10.5px;
		color: var(--color-text-dimmer, #5f6370);
		line-height: 1.4;
		margin-top: 1px;
	}
	.status-refs {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
		flex: 0 1 auto;
		overflow: hidden;
	}
	.status-refs > * {
		flex: none;
	}
	.status-refs .ref {
		font-size: 10px;
		color: var(--color-text-dim, #797d86);
	}
	.status-refs .age {
		font-size: 10px;
	}
	.status-glyphs {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		flex: none;
	}

	/* Status glyphs — single letter or short word. Color carries severity. */
	.glyph {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 14px;
		height: 14px;
		padding: 0 3px;
		border-radius: 3px;
		font-family: "Geist Mono", ui-monospace, monospace;
		font-size: 9px;
		font-weight: 600;
		line-height: 1;
		letter-spacing: 0;
	}
	.glyph--text {
		padding: 0 5px;
		font-weight: 500;
		letter-spacing: 0.02em;
		text-transform: lowercase;
	}
	.glyph--amber {
		background: rgba(255, 180, 80, 0.10);
		color: #ffb851;
		border: 0.5px solid rgba(255, 180, 80, 0.30);
	}
	.glyph--red {
		background: rgba(255, 100, 100, 0.10);
		color: #ff8585;
		border: 0.5px solid rgba(255, 100, 100, 0.30);
	}
	.glyph--green {
		background: rgba(120, 200, 130, 0.10);
		color: #7bd99c;
		border: 0.5px solid rgba(120, 200, 130, 0.30);
	}

	/* Title-row badges — same compact shape as glyphs but with full words
	   like "here" / "draft" that don't shorten well. */
	.title-badge {
		font-size: 9px;
		padding: 1px 5px;
		border-radius: 3px;
		flex: none;
		font-family: "Geist Mono", ui-monospace, monospace;
		font-weight: 500;
		line-height: 1.4;
		text-transform: lowercase;
		letter-spacing: 0.02em;
	}
	.title-badge--here {
		background: rgba(110, 211, 145, 0.14);
		color: #7bd99c;
		border: 0.5px solid rgba(110, 211, 145, 0.36);
	}
	.title-badge--draft {
		background: rgba(140, 140, 140, 0.12);
		color: #9ea2aa;
		border: 0.5px solid rgba(140, 140, 140, 0.26);
	}
	.title-badge--merged {
		background: rgba(120, 80, 200, 0.14);
		color: #b990ff;
		border: 0.5px solid rgba(120, 80, 200, 0.30);
	}
	.title-badge--closed {
		background: rgba(255, 100, 100, 0.14);
		color: #ff8585;
		border: 0.5px solid rgba(255, 100, 100, 0.28);
	}
</style>
