<!--
  Sidebar row for a GitHub issue (issue lens). Issue is the anchor; PR
  + worktree + claude session enrich it.
-->
<script lang="ts">
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import { relTime } from "$lib/util/format";
	import type { SessionCardT, WorktreeEnrichment } from "$lib/data/lenses";

	type Props = {
		issue: { number: number; state: string; title: string | null };
		worktree: WorktreeEnrichment;
		session: SessionCardT | null;
		now: number;
		density: "comfortable" | "compact";
		surface: "desktop" | "mobile";
		selected: boolean;
		onSelect: (id: string, ev?: MouseEvent) => void;
	};
	let { issue, worktree, session, now, density, surface, selected, onSelect }: Props = $props();

	const lastMs = $derived(
		session?.lastActivityAt ? Date.parse(session.lastActivityAt) || 0 : 0,
	);
	const ciBad = $derived(worktree.pr?.statusCheckRollup === "FAILURE");
	const reviewBad = $derived(worktree.pr?.reviewDecision === "CHANGES_REQUESTED");
	const conflict = $derived(
		worktree.pr?.mergeable === "CONFLICTING" || worktree.pr?.mergeStateStatus === "DIRTY",
	);
</script>

<div
	class="fleet-item"
	data-selected={selected}
	data-density={density}
	onclick={(e) => onSelect(worktree.id, e)}
	onkeydown={(e) => {
		if (e.key === "Enter" || e.key === " ") {
			e.preventDefault();
			onSelect(worktree.id);
		}
	}}
	role="button"
	tabindex="0"
>
	<div class="fleet-item-main">
		<span class="pip {session?.state === 'working' ? 'ok' : 'idle'}" title="claude state"></span>
		<div class="fleet-item-body">
			<div class="fleet-item-title-row">
				<span class="fleet-item-title">
					#{issue.number}
					{#if issue.title}{issue.title}{/if}
				</span>
			</div>
			<div class="fleet-item-sub">
				<HostGlyph host={worktree.host} size={12} />
				{#if surface !== "mobile"}
					<span class="mono dimer">{worktree.host}</span>
					<span class="dimest">·</span>
				{/if}
				{#if worktree.pr}
					<span class="mono dimer">PR #{worktree.pr.number}</span>
					<span class="dimest">·</span>
				{/if}
				<span class="mono dimer" style:font-size="11px">{worktree.branch}</span>
				{#if lastMs > 0}
					<span class="dimest">·</span>
					<span class="dimer mono" style:font-size="11px">{relTime(lastMs, now)}</span>
				{/if}
				{#if ciBad}
					<span class="reason-chip mono" title="CI failing">CI</span>
				{/if}
				{#if reviewBad}
					<span class="reason-chip mono" title="Review changes requested">review</span>
				{/if}
				{#if conflict}
					<span class="reason-chip mono" title="Merge conflict">conflict</span>
				{/if}
			</div>
		</div>
	</div>
</div>

<style>
	.reason-chip {
		font-size: 10.5px;
		padding: 1px 5px;
		border-radius: 3px;
		background: rgba(255, 100, 100, 0.14);
		color: #ff7272;
		border: 0.5px solid rgba(255, 100, 100, 0.32);
	}
</style>
