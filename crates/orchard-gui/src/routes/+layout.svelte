<script lang="ts">
	import "$lib/styles/tailwind.css";
	import "$lib/styles/theme.css";
	import "$lib/styles/layout.css";
	import { getStore } from "$lib/store.svelte";
	import { onMount } from "svelte";
	import { Toaster } from "svelte-sonner";

	// Houdini's client at `src/client.ts` is wired by the houdini-svelte
	// plugin (see `houdini.config.js:client`); it's lazy-imported the
	// first time a Houdini store fetches. Daemon-data subscriptions are
	// owned by individual Houdini stores — no global hydrate/subscribe
	// from here.

	type Props = { children?: import("svelte").Snippet };
	let { children }: Props = $props();

	const store = getStore();

	$effect(() => {
		document.documentElement.dataset.theme = store.theme;
		document.documentElement.style.setProperty("--accent-hue", String(store.accentHue));
		// Persist UI prefs across reloads. localStorage writes are cheap; the
		// $effect only re-runs when one of the tracked reactive reads changes.
		try {
			localStorage.setItem("orchard:ui:theme", store.theme);
			localStorage.setItem("orchard:ui:accent-hue", String(store.accentHue));
			localStorage.setItem("orchard:ui:density", store.density);
			localStorage.setItem("orchard:ui:lens", store.lens);
		} catch {
			// Safari private mode — ignore.
		}
	});

	onMount(() => {
		const stopTick = store.startNowTick();
		store.hydrateChatRooms();
		const subPromise = store.subscribeChat();

		return () => {
			stopTick();
			// intentional swallow: cleanup-time unsubscribe; if the promise never resolved the subscription was never established
			subPromise.then((u) => u()).catch(() => {});
			store.teardown();
		};
	});
</script>

<Toaster richColors />
{@render children?.()}
