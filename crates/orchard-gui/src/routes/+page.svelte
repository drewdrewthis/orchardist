<!--
  App root. Daemon-only — every visible row of data is hydrated from the
  local daemon (HTTP + WS) or the Tauri chat bridge. Mock data has been
  retired; surfaces without a daemon source render empty state.
-->
<script lang="ts">
	import { onMount } from "svelte";
	import { getStore } from "$lib/store.svelte";
	import DesktopLayout from "$lib/components/DesktopLayout.svelte";
	import MobileLayout from "$lib/components/MobileLayout.svelte";
	import Palette from "$lib/components/Palette.svelte";
	import NewConversation from "$lib/components/NewConversation.svelte";
	import ContractModal from "$lib/components/ContractModal.svelte";
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
				store.openContract(null);
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
		};
	});

	function onPalettePick(entry: PaletteEntry) {
		store.closePalette();
		if (entry.kind === "action") {
			if (entry.action === "new-conversation") store.openNewConv();
			else if (entry.action?.startsWith("lens:")) store.setLens(entry.action.slice(5) as Lens);
			else if (entry.action === "toggle-theme") store.toggleTheme();
			else if (entry.action === "toggle-view") store.toggleView();
			return;
		}
		if (entry.itemId) {
			// Palette entries today carry legacy item ids — for channels
			// the id is the room id; for worktrees we don't have a
			// session keyed at the lens level here, so we surface a
			// session-tab keyed by sessionUuid when present.
			const channel = store.chatRooms.find((r) => r.id === entry.itemId);
			if (channel) {
				store.openChannel(channel.id);
			}
			// Worktree-keyed palette entries don't currently map to a
			// session/pane identity — skip until palette emits row
			// identity directly.
		}
	}

	const contractItem = $derived(
		store.contractItemId ? store.items.find((i) => i.id === store.contractItemId) || null : null,
	);
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
	entries={store.paletteEntries}
	actions={store.paletteActions}
	onClose={() => store.closePalette()}
	onPick={onPalettePick}
/>

<NewConversation
	open={store.newConvOpen}
	surface={store.surface}
	items={store.items}
	hosts={store.hosts}
	onClose={() => store.closeNewConv()}
	onLaunch={async (spec) => {
		store.closeNewConv();
		const wt = store.items.find((i) => i.id === spec.worktreeId);
		if (wt && wt.kind === "worktree" && spec.host === wt.host) {
			try {
				const { createWorktree } = await import("$lib/tauri");
				const repoRoot = wt.path.split("/wt/")[0] || wt.path;
				await createWorktree(repoRoot, wt.path, wt.branch);
				store.hydrateFromDaemon();
			} catch (err) {
				console.warn("[orchard-gui] create_worktree failed:", err);
			}
		}
		// Newly-created worktree: no session identity yet — the user can
		// click its row in the sidebar once the daemon picks it up.
	}}
/>

<ContractModal
	item={contractItem}
	messages={[]}
	onClose={() => store.openContract(null)}
/>
