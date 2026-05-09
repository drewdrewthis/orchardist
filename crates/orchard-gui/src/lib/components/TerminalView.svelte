<!-- Read-only terminal scrollback view. Real terminal embed comes later. -->
<script lang="ts">
	import { tick } from "svelte";
	import type { TerminalLine, WorktreeItem, ChannelItem } from "$lib/data/types";

	type Props = { lines: TerminalLine[]; item: WorktreeItem | ChannelItem };
	let { lines, item }: Props = $props();

	let scrollEl: HTMLDivElement | undefined = $state();
	$effect(() => {
		const _ = lines.length;
		void _;
		tick().then(() => {
			if (scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
		});
	});

	const live = $derived(item.kind === "worktree" && item.session?.live);
</script>

<div class="term term-pane">
	<div class="term-statusbar mono">
		<span style="color: #a1a1ac;">tmux</span>
		<span style="color: #7d7d8a;">·</span>
		{#if item.kind === "worktree"}
			<span style="color: #e9e9ee;">{item.host}</span>
			<span style="color: #7d7d8a;">:</span>
			<span style="color: #e9e9ee;">{item.branch}</span>
			<span style="color: #7d7d8a;">·</span>
			<span style="color: #a1a1ac;">{item.session?.instance || "detached"}</span>
		{:else}
			<span style="color: #e9e9ee;">{item.title}</span>
		{/if}
		<span style="margin-left: auto; color: #7d7d8a;">
			{live ? "⏵ live" : "○ detached"}
		</span>
	</div>
	<div class="term-scroll" bind:this={scrollEl}>
		{#each lines as l, i (i)}
			<div class="term-line" data-c={l.c}>{l.t || " "}</div>
		{/each}
		{#if live}
			<div class="term-line">
				<span style="color: #7e7e8a;">{">"}</span>&nbsp;<span class="caret"></span>
			</div>
		{/if}
	</div>
</div>
