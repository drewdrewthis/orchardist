<!--
  Header for a channel/room conversation. The legacy version showed a
  per-agent roster pulled from the hand-rolled `agents` list — that
  dataset has been retired (no daemon source for "Agent"), so the
  header is now just the room banner + view switcher.

  When the schema grows a real `chatRoom.members` field, this rail can
  be brought back driven by Houdini.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import ViewSwitcher from "./ViewSwitcher.svelte";
	import type { ConvView, Surface } from "$lib/data/types";

	type Props = {
		roomId: string;
		memberCount: number;
		view: ConvView;
		switcherVariant?: "segmented" | "icon-toggle";
		surface: Surface;
		onView: (v: ConvView) => void;
		onFork: () => void;
		onClose?: () => void;
	};
	let {
		roomId,
		memberCount,
		view,
		switcherVariant = "segmented",
		surface,
		onView,
		onFork,
		onClose,
	}: Props = $props();

	const display = $derived(roomId.startsWith("@") ? roomId : `#${roomId}`);
</script>

<div class="conv-header is-channel">
	<div class="conv-header-row">
		{#if surface === "mobile" && onClose}
			<button class="iconbtn" onclick={onClose} aria-label="Back" style="margin-left: -6px;">
				<Icon name="arrow-left" size={16} />
			</button>
		{/if}
		<div class="conv-title-block">
			<div class="conv-title-row">
				<span
					class="channel-hash"
					style="width: 18px; height: 18px; font-size: 12px; line-height: 16px;"
				>#</span>
				<span class="conv-title">{display}</span>
				<span class="dimer mono" style="font-size: 11px; margin-left: 6px;">
					{memberCount} member{memberCount === 1 ? "" : "s"}
				</span>
			</div>
		</div>
		<div class="conv-header-actions">
			<button class="iconbtn" onclick={onFork} title="Fork conversation">
				<Icon name="git-fork" size={15} />
			</button>
			<button class="iconbtn" title="More"><Icon name="more" size={15} /></button>
			{#if surface === "desktop"}
				<span class="conv-header-divider" aria-hidden="true"></span>
				<ViewSwitcher value={view} onChange={onView} variant={switcherVariant} />
			{/if}
		</div>
	</div>
</div>
