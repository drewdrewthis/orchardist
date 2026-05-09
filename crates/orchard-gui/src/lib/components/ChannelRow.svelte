<!--
  Channels (chat rooms) — same in every lens. Thin wrapper that defers
  to FleetItem for now since channel display hasn't changed.
-->
<script lang="ts">
	import FleetItem from "./FleetItem.svelte";
	import type { Agent, ChannelItem } from "$lib/data/types";

	type Props = {
		item: ChannelItem;
		selected: boolean;
		density: "comfortable" | "compact";
		surface: "desktop" | "mobile";
		agents: Agent[];
		onSelect: (id: string, ev?: MouseEvent) => void;
	};
	let { item, selected, density, surface, agents, onSelect }: Props = $props();
</script>

<FleetItem
	{item}
	{selected}
	now={Date.now()}
	{density}
	{surface}
	{agents}
	{onSelect}
/>
