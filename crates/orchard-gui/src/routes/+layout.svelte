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
		let stopSub: (() => void) | null = null;

		(async () => {
			const ok = await store.hydrateFromDaemon();
			if (ok) {
				stopSub = await store.subscribeDaemon();
			}
			await store.hydrateChatRooms();
			await store.subscribeChatAppends();
		})();

		return () => {
			stop1();
			stop2();
			stopSub?.();
		};
	});
</script>

{@render children?.()}
