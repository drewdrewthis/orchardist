<!--
  A chat-room row in the lens sidebar. Rendered above each lens's content,
  so picking #general doesn't depend on which lens is active. Identity is
  the room id; clicking opens it in a tab via store.openChannel().
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";

	type Props = {
		roomId: string;
		memberCount: number;
		selected: boolean;
		density: "comfortable" | "compact";
		surface: "desktop" | "mobile";
		onSelect: (ev?: MouseEvent) => void;
	};
	let { roomId, memberCount, selected, density, surface, onSelect }: Props = $props();

	const display = $derived(roomId.startsWith("@") ? roomId : `#${roomId}`);
</script>

<div
	class="fleet-item is-channel"
	data-selected={selected}
	data-density={density}
	onclick={(e) => onSelect(e)}
	onkeydown={(e) => {
		if (e.key === "Enter" || e.key === " ") {
			e.preventDefault();
			onSelect();
		}
	}}
	role="button"
	tabindex="0"
>
	<div class="fleet-item-main">
		<span class="channel-hash" title="Channel">#</span>
		<div class="fleet-item-body">
			<div class="fleet-item-title-row">
				<span class="fleet-item-title">{display}</span>
			</div>
			{#if surface !== "mobile"}
				<div class="fleet-item-sub">
					<Icon name="message" size={11} />
					<span class="dimer" style:font-size="11.5px">
						{memberCount} member{memberCount === 1 ? "" : "s"}
					</span>
				</div>
			{/if}
		</div>
	</div>
</div>
