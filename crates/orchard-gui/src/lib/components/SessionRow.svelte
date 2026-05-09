<!--
  Sidebar row for a Claude session. Used by both attention and recent
  lenses. The row carries:
    - session anchor (sessionUuid, state, lastActivityAt)
    - optional worktree enrichment (branch, repo, pr/issue numbers)
    - reason chips (e.g. "CI failing", "idle 12m") supplied by the lens
-->
<script lang="ts">
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import { relTime } from "$lib/util/format";
	import { getStore } from "$lib/store.svelte";
	import type {
		SessionCardT,
		WorktreeEnrichment,
	} from "$lib/data/lenses";

	type Props = {
		session: SessionCardT;
		worktree: WorktreeEnrichment | null;
		reasons: string[];
		/** ms since epoch — lens-derived (jsonl > daemon > 0). 0 means no signal. */
		lastActivityMs?: number;
		now: number;
		density: "comfortable" | "compact";
		surface: "desktop" | "mobile";
		selected: boolean;
		onSelect: (id: string, ev?: MouseEvent) => void;
	};
	let {
		session,
		worktree,
		reasons,
		lastActivityMs = 0,
		now,
		density,
		surface,
		selected,
		onSelect,
	}: Props = $props();

	const store = getStore();
	const isHere = $derived(
		!!session.pane && store.lensSnapshots.tmux.activePaneIds.has(session.pane.paneId),
	);
	const lastMs = $derived(
		lastActivityMs || (session.lastActivityAt ? Date.parse(session.lastActivityAt) || 0 : 0),
	);
	const title = $derived(worktree?.branch || session.process?.cwd || session.sessionUuid.slice(0, 8));
	const stateLabel = $derived(session.state === "no_claude" ? "no claude" : session.state);
</script>

<div
	class="fleet-item"
	data-selected={selected}
	data-density={density}
	data-here={isHere}
	onclick={(e) => onSelect(session.id, e)}
	onkeydown={(e) => {
		if (e.key === "Enter" || e.key === " ") {
			e.preventDefault();
			onSelect(session.id);
		}
	}}
	role="button"
	tabindex="0"
>
	<div class="fleet-item-main">
		<span class="pip {session.state === 'working' ? 'ok' : 'idle'}" title={stateLabel}></span>
		<div class="fleet-item-body">
			<div class="fleet-item-title-row">
				<span class="fleet-item-title">{title}</span>
				{#if isHere}
					<span class="here-badge mono" title="A tmux client is currently watching this pane">here</span>
				{/if}
			</div>
			<div class="fleet-item-sub">
				{#if worktree}
					<HostGlyph host={worktree.host} size={12} />
					{#if surface !== "mobile"}
						<span class="mono dimer">{worktree.host}</span>
						<span class="dimest">·</span>
					{/if}
				{/if}
				{#if worktree?.pr}
					<span class="mono dimer">PR #{worktree.pr.number}</span>
					<span class="dimest">·</span>
				{/if}
				{#if worktree?.issue}
					<span class="mono dimer">#{worktree.issue.number}</span>
					<span class="dimest">·</span>
				{/if}
				<span class="dimer mono" style:font-size="11px">{stateLabel}</span>
				{#if lastMs > 0}
					<span class="dimest">·</span>
					<span class="dimer mono" style:font-size="11px">{relTime(lastMs, now)}</span>
				{/if}
				{#each reasons as r}
					<span class="reason-chip mono" title={r}>{r}</span>
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
		background: rgba(255, 180, 80, 0.14);
		color: #ffb851;
		border: 0.5px solid rgba(255, 180, 80, 0.32);
	}
</style>
