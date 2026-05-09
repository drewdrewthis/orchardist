<!-- Speaker avatar (user vs agent). -->
<script lang="ts">
	type Props = { kind?: "user" | "agent"; size?: number };
	let { kind = "agent", size = 22 }: Props = $props();
</script>

{#if kind === "user"}
	<span
		class="avatar user"
		style:width="{size}px"
		style:height="{size}px"
		style:font-size="{size * 0.42}px"
	>
		D
	</span>
{:else}
	<span class="avatar agent" style:width="{size}px" style:height="{size}px">
		<svg width={size * 0.55} height={size * 0.55} viewBox="0 0 24 24" fill="none" aria-hidden="true">
			<path d="M12 4 L19 8 V16 L12 20 L5 16 V8 Z" fill="currentColor" opacity="0.3" />
			<circle cx="12" cy="12" r="2.4" fill="currentColor" />
		</svg>
	</span>
{/if}

<style>
	.avatar {
		border-radius: 50%;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		flex: none;
		line-height: 1;
		font-weight: 600;
	}
	.avatar.user {
		background: var(--surface-2);
		border: 0.5px solid var(--line-2);
		color: var(--fg-2);
	}
	.avatar.agent {
		background: var(--fg);
		color: var(--bg);
	}
</style>
