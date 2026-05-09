<script lang="ts">
	import "$lib/styles/theme.css";
	import "$lib/styles/layout.css";
	import { getStore } from "$lib/store.svelte";
	import { onMount } from "svelte";

	type Props = { children?: import("svelte").Snippet };
	let { children }: Props = $props();

	const store = getStore();

	$effect(() => {
		document.documentElement.dataset.theme = store.theme;
		document.documentElement.style.setProperty("--accent-hue", String(store.accentHue));
	});

	onMount(() => {
		const stop1 = store.startNowTick();
		const stop2 = store.startLiveTick();
		return () => {
			stop1();
			stop2();
		};
	});
</script>

{@render children?.()}
