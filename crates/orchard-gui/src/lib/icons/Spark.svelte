<!-- Sparkline mini-bar (20 values), used in fleet rows for an at-a-glance activity feel. -->
<script lang="ts">
	type Props = {
		values: number[];
		w?: number;
		h?: number;
		color?: string;
	};
	let { values, w = 56, h = 14, color = "currentColor" }: Props = $props();

	const max = $derived(Math.max(1, ...values));
	const step = $derived(w / (values.length || 1));
</script>

<svg width={w} height={h} aria-hidden="true">
	{#if values.length}
		{#each values as v, i}
			{@const bh = Math.max(1, (v / max) * (h - 2))}
			<rect
				x={i * step + 0.5}
				y={h - bh}
				width={Math.max(1, step - 1)}
				height={bh}
				rx="0.6"
				fill={color}
				opacity={0.5 + (v / max) * 0.5}
			/>
		{/each}
	{/if}
</svg>
