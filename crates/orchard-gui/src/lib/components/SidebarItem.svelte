<!--
  SidebarItem — the unified row used by every sidebar lens.

  Per #540 B1: "All lenses must use the same `Item` component". Each
  lens projects its native data into `SidebarItem` (see
  `data/sidebar-item.ts`); this component is pure rendering.

  The row carries (per B5):
    - derived title (from `deriveItemTitle`)
    - branch / host / repo / PR / issue (from worktree)
    - tmux address (session:window.pane) as secondary metadata
    - pid + lifecycle state
    - lastActivityAt as "12m" relative time
    - PR status indicators (per B6: CI block, conflicts, review,
      pr state)
    - lens-supplied reason chips
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

	const isHere = $derived(here);

	const stateLabel = $derived(
		item.state === "no_claude" ? "no claude" : item.state,
	);

	// Per-PR status flags (B6). Derived once; rendered as colored chips.
	const ciBad = $derived(item.worktree?.pr?.statusCheckRollup === "FAILURE");
	const reviewBad = $derived(
		item.worktree?.pr?.reviewDecision === "CHANGES_REQUESTED",
	);
	const conflict = $derived(
		item.worktree?.pr?.mergeable === "CONFLICTING" ||
			item.worktree?.pr?.mergeStateStatus === "DIRTY",
	);
	const prState = $derived(item.worktree?.pr?.state?.toUpperCase() ?? null);
	// `state` carries DRAFT; the underlying schema's `draft` boolean isn't
	// exposed via the WorktreeEnrichment fragment so we rely on state only.
	const isDraft = $derived(prState === "DRAFT");
	const issueClosed = $derived(
		item.worktree?.issue?.state?.toUpperCase() === "CLOSED",
	);

	// Directory chip: prefer worktree.path (canonical), fall back to the
	// session's recorded cwd. Render only the basename to keep the row
	// short — full path is in the title attribute.
	const cwdFull = $derived(
		item.worktree?.path ?? item.session?.process?.cwd ?? null,
	);
	const cwdBase = $derived(
		cwdFull ? cwdFull.split("/").filter(Boolean).pop() || cwdFull : null,
	);
</script>

<div
	class="fleet-item"
	data-selected={selected}
	data-density={density}
	data-here={isHere}
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
	<div class="fleet-item-main">
		<span
			class="pip {item.state === 'working' ? 'ok' : 'idle'}"
			title={stateLabel}
		></span>
		<div class="fleet-item-body">
			<div class="fleet-item-title-row">
				<span class="fleet-item-title">{item.title}</span>
				{#if isHere}
					<span class="here-badge mono" title="A tmux client is currently watching this pane">here</span>
				{/if}
				{#if isDraft}
					<span class="badge draft mono" title="Draft PR">draft</span>
				{:else if prState === "MERGED"}
					<span class="badge merged mono" title="PR merged">merged</span>
				{:else if prState === "CLOSED"}
					<span class="badge closed mono" title="PR closed">closed</span>
				{/if}
			</div>
			<div class="fleet-item-sub">
				{#if item.worktree}
					<HostGlyph host={item.worktree.host} size={12} />
					{#if surface !== "mobile"}
						<span class="mono dimer">{item.worktree.host}</span>
						<span class="dimest">·</span>
					{/if}
				{/if}
				{#if cwdBase && cwdBase !== item.title}
					<span class="mono dimer" title={cwdFull}>{cwdBase}</span>
					<span class="dimest">·</span>
				{/if}
				{#if item.worktree?.branch && item.worktree.branch !== item.title}
					<span class="mono dimer" title="Branch">{item.worktree.branch}</span>
					<span class="dimest">·</span>
				{/if}
				{#if item.worktree?.pr}
					<span class="mono dimer">PR #{item.worktree.pr.number}</span>
					<span class="dimest">·</span>
				{/if}
				{#if item.worktree?.issue}
					<span class="mono dimer">
						#{item.worktree.issue.number}
						{#if issueClosed}
							<span class="reason-chip mono red" style:margin-left="3px" title="Issue closed">closed</span>
						{/if}
					</span>
					<span class="dimest">·</span>
				{/if}
				{#if item.tmuxAddress && surface !== "mobile"}
					<span class="mono dimest" style:font-size="10.5px" title="tmux address">{item.tmuxAddress}</span>
					<span class="dimest">·</span>
				{/if}
				{#if item.pid != null && surface !== "mobile"}
					<span class="mono dimest" style:font-size="10.5px" title="claude pid">{item.pid}</span>
					<span class="dimest">·</span>
				{/if}
				<span class="dimer mono" style:font-size="11px">{stateLabel}</span>
				{#if item.lastActivityMs > 0}
					<span class="dimest">·</span>
					<span class="dimer mono" style:font-size="11px">{relTime(item.lastActivityMs, now)}</span>
				{/if}
				{#if ciBad}
					<span class="reason-chip mono red" title="CI failing">CI</span>
				{/if}
				{#if reviewBad}
					<span class="reason-chip mono red" title="Review changes requested">review</span>
				{/if}
				{#if conflict}
					<span class="reason-chip mono red" title="Merge conflict">conflict</span>
				{/if}
				{#each item.reasons as r}
					<span class="reason-chip mono amber" title={r}>{r}</span>
				{/each}
			</div>
		</div>
	</div>
</div>

<style>
	.reason-chip {
		font-size: 10.5px;
		padding: 1px 5px;
		border-radius: 3px;
	}
	.reason-chip.amber {
		background: rgba(255, 180, 80, 0.14);
		color: #ffb851;
		border: 0.5px solid rgba(255, 180, 80, 0.32);
	}
	.reason-chip.red {
		background: rgba(255, 100, 100, 0.14);
		color: #ff7272;
		border: 0.5px solid rgba(255, 100, 100, 0.32);
	}
	.badge {
		font-size: 10px;
		padding: 1px 5px;
		border-radius: 3px;
		margin-left: 6px;
	}
	.badge.draft {
		background: rgba(140, 140, 140, 0.18);
		color: #aaa;
		border: 0.5px solid rgba(140, 140, 140, 0.32);
	}
	.badge.merged {
		background: rgba(120, 80, 200, 0.18);
		color: #b990ff;
		border: 0.5px solid rgba(120, 80, 200, 0.32);
	}
	.badge.closed {
		background: rgba(255, 100, 100, 0.18);
		color: #ff7272;
		border: 0.5px solid rgba(255, 100, 100, 0.32);
	}
</style>
