<!-- Horizontal stack of agent avatars used in channel rows. -->
<script lang="ts">
	import type { Agent } from "$lib/data/types";

	type Props = { agents: Agent[]; size?: number; max?: number };
	let { agents, size = 16, max = 4 }: Props = $props();

	const shown = $derived(agents.slice(0, max));
	const overflow = $derived(Math.max(0, agents.length - max));
</script>

<span class="agent-stack" style:--avatar-size="{size}px">
	{#each shown as a (a.id)}
		<span
			class="agent-avatar"
			style:background="oklch(0.62 0.13 {a.hue})"
			style:width="{size}px"
			style:height="{size}px"
			style:font-size="{size * 0.5}px"
			title="{a.name} · {a.role}"
		>
			{a.avatar}
		</span>
	{/each}
	{#if overflow > 0}
		<span
			class="agent-avatar more"
			style:width="{size}px"
			style:height="{size}px"
			style:font-size="{size * 0.45}px"
		>
			+{overflow}
		</span>
	{/if}
</span>

<style>
	.agent-stack {
		display: inline-flex;
		align-items: center;
	}
	.agent-stack > :global(.agent-avatar) {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border-radius: 4px;
		color: white;
		font-weight: 600;
		line-height: 1;
		flex: none;
		margin-left: -2px;
		border: 0.5px solid var(--bg);
	}
	.agent-stack > :global(.agent-avatar:first-child) {
		margin-left: 0;
	}
	.agent-avatar.more {
		background: var(--surface-2);
		color: var(--fg-2);
	}
</style>
