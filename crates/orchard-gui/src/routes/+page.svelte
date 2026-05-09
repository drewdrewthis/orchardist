<!--
  App root. Picks desktop vs mobile based on window width (mobile-first), wires
  the keyboard shortcut handler, mounts the palette / new-conversation /
  contract overlays, and binds them to the store.
-->
<script lang="ts">
	import { onMount } from "svelte";
	import { getStore } from "$lib/store.svelte";
	import { conversation as mockConversation } from "$lib/data/mock";
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
			else if (entry.action === "fork") {
				const idx = store.conversation.messages.length - 1;
				if (idx >= 0) store.startFork(idx, store.conversation.messages[idx]);
			}
			return;
		}
		if (entry.itemId) store.openItem(entry.itemId);
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
				console.warn("[orchard-gui] create_worktree failed (acceptable in dev):", err);
			}
		}
		if (spec.worktreeId) store.openItem(spec.worktreeId, { newPane: true });
	}}
/>

<ContractModal
	item={contractItem}
	messages={mockConversation.messages}
	onClose={() => store.openContract(null)}
/>
