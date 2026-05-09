<!-- Chat ⇄ Terminal segmented control. Icon-only at v1; variant for tweaks. -->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import type { ConvView } from "$lib/data/types";

	type Props = {
		value: ConvView;
		onChange: (v: ConvView) => void;
		variant?: "segmented" | "icon-toggle";
	};
	let { value, onChange, variant = "segmented" }: Props = $props();

	const items: { id: ConvView; label: string; icon: string }[] = [
		{ id: "chat", label: "Chat", icon: "chat" },
		{ id: "terminal", label: "Terminal", icon: "terminal" },
	];

	const idx = $derived(items.findIndex((i) => i.id === value));
</script>

{#if variant === "icon-toggle"}
	<button
		class="iconbtn"
		data-active="true"
		onclick={() => onChange(value === "chat" ? "terminal" : "chat")}
		title={value === "chat" ? "Switch to terminal · ⌘\\" : "Switch to chat · ⌘\\"}
	>
		<Icon name={value === "chat" ? "terminal" : "chat"} size={15} />
	</button>
{:else}
	<div class="seg seg-icon" role="tablist">
		<div
			class="seg-thumb"
			style:left="calc(2px + {idx} * (100% - 4px) / 2)"
			style:width="calc((100% - 4px) / 2)"
		></div>
		{#each items as i (i.id)}
			<button
				role="tab"
				aria-selected={i.id === value}
				data-on={i.id === value}
				title={i.label}
				onclick={() => onChange(i.id)}
			>
				<Icon name={i.icon} size={15} />
			</button>
		{/each}
	</div>
{/if}
