<script lang="ts">
	import "$lib/styles/theme.css";
	import "$lib/styles/layout.css";
	import { getStore } from "$lib/store.svelte";
	import { onMount } from "svelte";

	// Houdini's client at `src/client.ts` is wired by the houdini-svelte
	// plugin (see `houdini.config.js:client`); it's lazy-imported the
	// first time a Houdini store fetches. Nothing to call here.

	type Props = { children?: import("svelte").Snippet };
	let { children }: Props = $props();

	const store = getStore();

	$effect(() => {
		document.documentElement.dataset.theme = store.theme;
		document.documentElement.style.setProperty("--accent-hue", String(store.accentHue));
	});

	onMount(() => {
		const stopTick = store.startNowTick();
		store.hydrateFromDaemon();
		store.hydrateChatRooms();
		store.subscribeDaemon();
		const subPromise = store.subscribeChat();

		return () => {
			stopTick();
			subPromise.then((u) => u()).catch(() => {});
			store.teardown();
		};
	});
</script>

{@render children?.()}
