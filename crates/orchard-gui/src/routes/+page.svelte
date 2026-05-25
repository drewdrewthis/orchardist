<!--
  App root. Daemon-only — every visible row of data is hydrated from the
  local daemon via Houdini queries (HTTP + WS) or the chat-core bridge.
-->
<script lang="ts">
	import { onMount } from "svelte";
	import { getStore } from "$lib/store.svelte";
	import DesktopLayout from "$lib/components/DesktopLayout.svelte";
	import MobileLayout from "$lib/components/MobileLayout.svelte";
	import Palette from "$lib/components/Palette.svelte";
	import NewConversation from "$lib/components/NewConversation.svelte";
	import {
		hostsStore,
		worktreesStore,
		buildHosts,
		buildWorktreePickerRows,
	} from "$lib/data/daemon-stores";
	import { buildPaletteEntries, PALETTE_ACTIONS } from "$lib/data/palette";
	import { launchSession } from "$lib/data/daemon";
	import { toast } from "$lib/util/toast";
	import type { Lens, PaletteEntry } from "$lib/data/types";

	const store = getStore();

	let viewportWidth = $state(typeof window !== "undefined" ? window.innerWidth : 1024);

	onMount(() => {
		const onResize = () => {
			viewportWidth = window.innerWidth;
			store.setSurface(viewportWidth < 768 ? "mobile" : "desktop");
		};
		onResize();
		window.addEventListener("resize", onResize);

		// Track online/offline so the UI can surface an obvious banner when
		// the device is offline (PWA over flaky 5G is the motivating case).
		const onOnline = () => store.setOnline(true);
		const onOffline = () => store.setOnline(false);
		window.addEventListener("online", onOnline);
		window.addEventListener("offline", onOffline);

		// Palette consumes hosts + worktrees; pre-fetch so ⌘K is instant.
		hostsStore.fetch();
		worktreesStore.fetch();

		const onKey = (e: KeyboardEvent) => {
			const cmd = e.metaKey || e.ctrlKey;
			const key = e.key.toLowerCase();
			const inField =
				document.activeElement?.tagName === "TEXTAREA" ||
				document.activeElement?.tagName === "INPUT";

			if (cmd && key === "k") {
				e.preventDefault();
				store.paletteOpen ? store.closePalette() : store.openPalette();
			} else if (e.key === "/" && !store.paletteOpen && !inField) {
				e.preventDefault();
				store.openPalette();
			} else if (e.key === "Escape") {
				store.closePalette();
				store.closeNewConv();
			} else if (cmd && key === "n") {
				e.preventDefault();
				store.openNewConv();
			} else if (cmd && key === "b" && store.surface === "desktop") {
				e.preventDefault();
				store.toggleSidebar();
			} else if (cmd && key === "w" && store.surface === "desktop") {
				e.preventDefault();
				if (store.activeTabId) store.closeTab(store.activeTabId);
			} else if (cmd && e.shiftKey && key === "f" && store.surface === "desktop") {
				e.preventDefault();
				store.toggleFullscreen();
			} else if (cmd && key === "\\" && store.surface === "desktop") {
				e.preventDefault();
				store.toggleView();
			} else if (cmd && (e.key === "]" || e.key === "[") && store.surface === "desktop") {
				e.preventDefault();
				store.cycleTab(e.key === "]" ? 1 : -1);
			} else if (cmd && /^[1-9]$/.test(e.key) && store.surface === "desktop") {
				const i = parseInt(e.key, 10) - 1;
				if (store.tabs[i]) {
					e.preventDefault();
					store.jumpToTab(i);
				}
			}
		};
		window.addEventListener("keydown", onKey);

		return () => {
			window.removeEventListener("resize", onResize);
			window.removeEventListener("keydown", onKey);
			window.removeEventListener("online", onOnline);
			window.removeEventListener("offline", onOffline);
		};
	});

	const paletteWorktrees = $derived(buildWorktreePickerRows($worktreesStore.data));
	const paletteHosts = $derived(buildHosts($hostsStore.data));
	const paletteEntries = $derived(
		buildPaletteEntries(paletteWorktrees, paletteHosts, store.chatRooms),
	);

	function onPalettePick(entry: PaletteEntry) {
		store.closePalette();
		if (entry.kind === "action") {
			if (entry.action === "new-conversation") store.openNewConv();
			else if (entry.action?.startsWith("lens:")) store.setLens(entry.action.slice(5) as Lens);
			else if (entry.action === "toggle-theme") store.toggleTheme();
			else if (entry.action === "toggle-view") store.toggleView();
			return;
		}
		if (entry.kind === "channel" && entry.roomId) {
			store.openChannel(entry.roomId);
		} else if (entry.kind === "session" && (entry.paneId || entry.sessionUuid)) {
			store.openSession({ paneId: entry.paneId, sessionUuid: entry.sessionUuid });
		}
		// Worktree-keyed entries don't yet carry a session/pane handle —
		// the next iteration will join Worktree.tmuxPanes into the palette
		// query so the palette can open a live session directly.
	}
</script>

<svelte:head>
	<title>Orchard</title>
</svelte:head>

<div class="shell">
	{#if store.surface === "desktop"}
		<DesktopLayout {store} />
	{:else}
		<MobileLayout {store} />
	{/if}
</div>

<Palette
	open={store.paletteOpen}
	surface={store.surface}
	entries={paletteEntries}
	actions={PALETTE_ACTIONS}
	onClose={() => store.closePalette()}
	onPick={onPalettePick}
/>

<NewConversation
	open={store.newConvOpen}
	surface={store.surface}
	onClose={() => store.closeNewConv()}
	onLaunch={async (spec) => {
		store.closeNewConv();
		// NewConversation emits an existing worktreeId from the picker, so the
		// worktree is already on disk (its path is `spec.cwd`). The daemon's
		// launchSession mutation creates the tmux session + boots Claude there
		// and returns the pane + pre-assigned session UUID, which we open
		// straight into the deep view — the composer targets the pane and the
		// transcript subscribes by uuid, both before the daemon's next poll.
		try {
			const result = await launchSession({
				cwd: spec.cwd,
				model: spec.model,
				prompt: spec.task?.trim() ? spec.task : undefined,
			});
			store.openSession({ paneId: result.paneId, sessionUuid: result.sessionUuid });
			toast.success(`Launched ${result.sessionName}`);
		} catch (err) {
			toast.error(err);
		}
	}}
/>
