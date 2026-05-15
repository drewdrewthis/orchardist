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
	// Bump the version suffix whenever the GraphQL fragment surface
	// changes shape. Old snapshots from a previous schema can confuse
	// Houdini's hydrate (records reference fields the runtime no longer
	// asks for, or vice-versa). Bumping the key effectively flushes the
	// stale snapshot for every connected client.
	const CACHE_KEY = "orchard:houdini:cache:v2";
	const STALE_CACHE_KEYS = ["orchard:houdini:cache:v1"];

	function hydrateHoudiniCache() {
		// Always purge known stale keys first — defensive cleanup.
		try {
			for (const k of STALE_CACHE_KEYS) localStorage.removeItem(k);
		} catch {}
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
			// Cap at 2MB — well below localStorage's 5MB ceiling on every
			// major browser, comfortably over typical orchard cache size
			// (210KB at 363 conversations, scales linearly).
			if (json.length > 2 * 1024 * 1024) return;
			localStorage.setItem(CACHE_KEY, json);
		} catch {
			// Quota exceeded or denied (Safari private mode). Drop the
			// stale snapshot so future loads don't try to hydrate corrupt
			// half-written data.
			try { localStorage.removeItem(CACHE_KEY); } catch {}
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
		// Multiple hooks because each browser/platform fires a different
		// subset reliably:
		//   - pagehide: most reliable on iOS Safari (incl. back/forward cache)
		//   - visibilitychange:hidden: backgrounding, tab swap, lock screen
		//   - beforeunload: desktop reload / close
		//   - flushIv: backstop every 10s in case all three are missed
		const onHide = () => { if (document.visibilityState === "hidden") persistHoudiniCache(); };
		document.addEventListener("visibilitychange", onHide);
		window.addEventListener("beforeunload", persistHoudiniCache);
		window.addEventListener("pagehide", persistHoudiniCache);
		// Tighter flush (10s, not 30s) so a user who reloads quickly still
		// gets the most recent data on the cold path.
		const flushIv = window.setInterval(persistHoudiniCache, 10_000);

		return () => {
			stopTick();
			document.removeEventListener("visibilitychange", onHide);
			window.removeEventListener("beforeunload", persistHoudiniCache);
			window.removeEventListener("pagehide", persistHoudiniCache);
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
