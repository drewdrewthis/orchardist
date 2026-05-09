<!--
  Per-message send status (pending → sent → delivered → read).
  Variant defaults to 'ticks' (single-check → double-check) but supports
  'dots', 'minimal', and 'text' for the Tweaks panel.
-->
<script lang="ts">
	import Icon from "./Icon.svelte";
	import type { SendStatus } from "$lib/data/types";

	type Variant = "ticks" | "dots" | "minimal" | "text";
	type Props = { status: SendStatus; variant?: Variant };
	let { status, variant = "ticks" }: Props = $props();

	const colorMap: Record<SendStatus, string> = {
		pending: "var(--fg-4)",
		sent: "var(--fg-3)",
		delivered: "var(--fg-2)",
		read: "var(--accent)",
	};

	const filled = $derived(
		variant === "dots"
			? ({ pending: 1, sent: 2, delivered: 3, read: 3 } as const)[status] || 0
			: 0,
	);
</script>

{#if variant === "text"}
	<span class="mono dimer" style:font-size="10px" style:color={colorMap[status]}>{status}</span>
{:else if variant === "dots"}
	<span style="display: inline-flex; gap: 2px;">
		{#each [0, 1, 2] as i}
			<i
				style:width="4px"
				style:height="4px"
				style:border-radius="50%"
				style:display="inline-block"
				style:background={i < filled ? colorMap[status] : "var(--fg-4)"}
				style:opacity={status === "read" ? 1 : i < filled ? 1 : 0.4}
			></i>
		{/each}
	</span>
{:else if variant === "minimal"}
	{#if status === "pending"}
		<i
			style:width="4px"
			style:height="4px"
			style:border-radius="50%"
			style:background={colorMap[status]}
			style:display="inline-block"
			style:opacity="0.6"
		></i>
	{:else}
		<span style:color={colorMap[status]}><Icon name="check" size={11} /></span>
	{/if}
{:else if status === "pending"}
	<span style:color={colorMap[status]}><Icon name="clock" size={11} /></span>
{:else if status === "sent"}
	<span style:color={colorMap[status]}><Icon name="check" size={11} /></span>
{:else}
	<span style:color={colorMap[status]}><Icon name="check-double" size={13} /></span>
{/if}
