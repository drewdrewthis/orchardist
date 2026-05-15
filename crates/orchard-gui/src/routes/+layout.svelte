<script lang="ts">
	import "$lib/styles/tailwind.css";
	import "$lib/styles/theme.css";
	import "$lib/styles/layout.css";
	import { getStore } from "$lib/store.svelte";
	import { onMount } from "svelte";
	import { Toaster } from "svelte-sonner";
	import houdiniCache from "$houdini/runtime/cache";

	// Houdini's client at `src/client.ts` is wired by the houdini-svelte
	// plugin (see `houdini.config.js:client`); it's lazy-imported the
	// first time a Houdini store fetches. Daemon-data subscriptions are
	// owned by individual Houdini stores — no global hydrate/subscribe
	// from here.

	type Props = { children?: import("svelte").Snippet };
	let { children }: Props = $props();

	const store = getStore();

	// ── Persist Houdini cache snapshot ─────────────────────────────────────
	// Default cache is in-memory only → every page reload re-fetches all 5
	// lens stores → "Loading…" flash for ~800ms even when the data hasn't
	// changed. Snapshot to localStorage on visibility change / unload, and
	// hydrate at boot BEFORE the lens stores fetch. The CacheAndNetwork
	// policy means we still revalidate on network — we just paint instantly
	// from the snapshot first.
	const CACHE_KEY = "orchard:houdini:cache:v1";

	function hydrateHoudiniCache() {
		try {
			const raw = localStorage.getItem(CACHE_KEY);
			if (!raw) return;
			const snapshot = JSON.parse(raw);
			houdiniCache.hydrate(snapshot);
		} catch (e) {
			// Corrupt snapshot — drop it and let the network fill.
			try { localStorage.removeItem(CACHE_KEY); } catch {}
		}
	}
	function persistHoudiniCache() {
		try {
			const json = houdiniCache.serialize();
			// Cap snapshot size (~256KB) so a runaway cache doesn't blow
			// localStorage's 5MB ceiling.
			if (json.length > 256 * 1024) return;
			localStorage.setItem(CACHE_KEY, json);
		} catch {
			// localStorage may be full or denied (Safari private mode).
		}
	}

	// Hydrate IMMEDIATELY (before any lens store mounts and fetches).
	if (typeof window !== "undefined") hydrateHoudiniCache();

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

		// Persist cache when the page is about to be hidden or unloaded.
		// `visibilitychange:hidden` fires reliably on mobile (lock screen,
		// tab swap); `beforeunload` is the desktop / reload path. Also
		// periodic flush every 30s so we don't lose recent state if the
		// page is killed without firing either hook (mobile Safari can).
		const onHide = () => { if (document.visibilityState === "hidden") persistHoudiniCache(); };
		document.addEventListener("visibilitychange", onHide);
		window.addEventListener("beforeunload", persistHoudiniCache);
		const flushIv = window.setInterval(persistHoudiniCache, 30_000);

		return () => {
			stopTick();
			document.removeEventListener("visibilitychange", onHide);
			window.removeEventListener("beforeunload", persistHoudiniCache);
			window.clearInterval(flushIv);
			// One last flush on teardown so dev HMR reload doesn't lose recent state.
			persistHoudiniCache();
			// intentional swallow: cleanup-time unsubscribe; if the promise never resolved the subscription was never established
			subPromise.then((u) => u()).catch(() => {});
			store.teardown();
		};
	});
</script>

<Toaster richColors />
{@render children?.()}
