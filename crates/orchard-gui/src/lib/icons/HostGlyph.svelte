<!--
  Tiny two-letter host glyph. Theme-aware: derives a stable hue from the hostname
  string and tints background/foreground in oklch so colors stay distinguishable
  in both light and dark modes.
-->
<script lang="ts">
	import { hostHue, hostInitials } from "$lib/util/format";

	type Props = { host: string; size?: number; dim?: boolean };
	let { host, size = 14, dim = false }: Props = $props();

	const initials = $derived(hostInitials(host));
	const hue = $derived(hostHue(host));
</script>

<span
	class="host-glyph"
	style:width="{size}px"
	style:height="{size}px"
	style:font-size="{size * 0.55}px"
	style:--hue={hue}
	style:opacity={dim ? 0.45 : 1}
>
	{initials}
</span>

<style>
	.host-glyph {
		border-radius: 3px;
		background: color-mix(in oklab, oklch(0.62 0.13 var(--hue)) 22%, var(--surface-2));
		color: color-mix(in oklab, oklch(0.62 0.13 var(--hue)) 65%, var(--fg));
		font-family: "Geist Mono", ui-monospace, monospace;
		font-weight: 600;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		letter-spacing: 0;
		flex: none;
		line-height: 1;
	}

	:global([data-theme="light"]) .host-glyph {
		background: oklch(0.94 0.03 var(--hue));
		color: oklch(0.32 0.08 var(--hue));
	}
</style>
