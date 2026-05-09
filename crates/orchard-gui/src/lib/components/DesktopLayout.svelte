<!--
  Desktop split: sidebar (resizable, auto-collapse to rail under 200px) and a
  panes area on the right. Topbar + offline ribbon sit above. Card aesthetic.
-->
<script lang="ts">
	import { onMount } from "svelte";
	import FleetTopBar from "./FleetTopBar.svelte";
	import LensSelector from "./LensSelector.svelte";
	import LensSidebar from "./LensSidebar.svelte";
	import PanesArea from "./PanesArea.svelte";
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import type { AppStore } from "$lib/store.svelte";

	type Props = { store: AppStore };
	let { store }: Props = $props();

	const COLLAPSE_AT = 200;
	const MIN_W = 240;
	const MAX_W = 460;

	onMount(() => {
		const v = parseInt(localStorage.getItem("orchard:sidebarW") || "320", 10);
		if (!isNaN(v)) store.setSidebarWidth(v);
	});

	$effect(() => {
		localStorage.setItem("orchard:sidebarW", String(store.sidebarWidth));
	});

	function onResizeStart(e: MouseEvent) {
		e.preventDefault();
		const startX = e.clientX;
		const startW = store.sidebarWidth;
		const move = (ev: MouseEvent) => {
			const next = startW + (ev.clientX - startX);
			if (next < COLLAPSE_AT) {
				if (!store.sidebarCollapsed) store.toggleSidebar();
				return;
			}
			if (store.sidebarCollapsed) store.toggleSidebar();
			store.setSidebarWidth(Math.max(MIN_W, Math.min(MAX_W, next)));
		};
		const up = () => {
			window.removeEventListener("mousemove", move);
			window.removeEventListener("mouseup", up);
			document.body.style.cursor = "";
		};
		window.addEventListener("mousemove", move);
		window.addEventListener("mouseup", up);
		document.body.style.cursor = "col-resize";
	}
</script>

<div class="desktop-frame" style:--sidebar-w="{store.sidebarWidth}px">
	{#if !store.fullscreen}
		<FleetTopBar
			hosts={store.hosts}
			account={store.account}
			theme={store.theme}
			surface="desktop"
			now={store.now}
			onOpenPalette={() => store.openPalette()}
			onNewConv={() => store.openNewConv()}
			onToggleTheme={() => store.toggleTheme()}
			onToggleSidebar={() => store.toggleSidebar()}
		/>
	{/if}

	{#if store.offline && !store.fullscreen}
		<div class="offline-banner">
			<Icon name="wifi-off" size={13} />
			<span>Daemon unreachable.</span>
			<span class="mono dimer">last sync · 38s</span>
			<button class="btn-ghost" style:height="22px" onclick={() => (store.offline = false)}>
				Dismiss
			</button>
		</div>
	{/if}

	<div
		class="desktop-grid"
		data-sidebar-collapsed={store.sidebarCollapsed || store.fullscreen}
	>
		{#if !store.fullscreen}
			<div
				class="desktop-sidebar"
				style:width={store.sidebarCollapsed ? "56px" : "{store.sidebarWidth}px"}
			>
				{#if !store.sidebarCollapsed}
					<div class="sidebar-controls">
						<LensSelector value={store.lens} onChange={(l) => store.setLens(l)} />
					</div>
					<LensSidebar
						now={store.now}
						density={store.density}
						surface="desktop"
						selectedId={store.selectedId}
						agents={store.agents}
						onSelect={(target, ev) => {
							const split = !!(ev && (ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.button === 1));
							if (target.kind === "channel") {
								store.openChannel(target.roomId, { split });
							} else {
								store.openSession({ paneId: target.paneId, sessionUuid: target.sessionUuid }, { split });
							}
						}}
					/>
					<div
						class="sidebar-resize"
						onmousedown={onResizeStart}
						role="separator"
						aria-orientation="vertical"
						title="Drag to resize · collapses below 200px"
					></div>
				{:else}
					<div class="rail-collapsed">
						<div class="rail-list">
							{#each store.tabs as tab (tab.id)}
								<button
									class="rail-conv"
									class:on={tab.id === store.activeTabId}
									onclick={() => (store.activeTabId = tab.id)}
									title={tab.kind === "channel" ? "#" + tab.roomId : (tab.paneId || tab.sessionUuid?.slice(0, 8) || "session")}
								>
									<span class="rail-active-bar"></span>
									{#if tab.kind === "channel"}
										<span class="channel-hash">#</span>
									{:else}
										<span class="pip ok rail-pip"></span>
									{/if}
								</button>
							{/each}
						</div>
					</div>
				{/if}
			</div>
		{/if}

		<div class="desktop-pane">
			<PanesArea
				tabs={store.tabs}
				activeTabId={store.activeTabId}
				paneSizes={store.paneSizes}
				fullscreen={store.fullscreen}
				view={store.view}
				conversation={store.visibleConversation || { itemId: '', recap: '', isChannel: false, messages: [] }}
				terminalLines={store.terminalLines}
				agents={store.agents}
				now={store.now}
				composeText={store.composeText}
				setComposeText={(s) => (store.composeText = s)}
				sending={store.sending}
				forkPreview={store.forkPreview}
				onActivateTab={(id) => (store.activeTabId = id)}
				onCloseTab={(id) => store.closeTab(id)}
				onResizePanes={(sizes) => store.setPaneSizes(sizes)}
				onView={(v) => store.setView(v)}
				onSetTabView={(id, v) => store.setTabView(id, v)}
				onSend={() => store.send()}
				onFork={() => {
					const conv = store.visibleConversation;
					if (conv && conv.messages.length > 0) {
						store.startFork(conv.messages.length - 1, conv.messages[conv.messages.length - 1]);
					}
				}}
				onStartFork={(i, m) => store.startFork(i, m)}
				onCommitFork={() => store.commitFork()}
				onCancelFork={() => store.cancelFork()}
				onJumpToAgent={() => {
					/* Agent jump removed pending lens-aware impl. */
				}}
				onOpenContract={(id) => store.openContract(id)}
				onToggleFullscreen={() => store.toggleFullscreen()}
				onOpenPalette={() => store.openPalette()}
				onLaunch={() => store.openNewConv()}
			/>
		</div>
	</div>
</div>

<style>
	.rail-collapsed {
		display: flex;
		flex-direction: column;
		height: 100%;
		padding: 8px 0;
	}
	.rail-list {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 2px;
		padding: 0 6px;
	}
	.rail-conv {
		position: relative;
		display: flex;
		align-items: center;
		justify-content: center;
		width: 36px;
		height: 36px;
		margin: 0 auto;
		border: 0;
		background: transparent;
		border-radius: 9px;
		cursor: default;
		transition: background 0.12s;
	}
	.rail-conv:hover {
		background: color-mix(in oklab, var(--fg) 6%, transparent);
	}
	.rail-conv.on {
		background: var(--surface-2);
	}
	.rail-active-bar {
		position: absolute;
		left: -6px;
		top: 8px;
		bottom: 8px;
		width: 2px;
		border-radius: 2px;
		background: transparent;
		transition: background 0.12s;
	}
	.rail-conv.on .rail-active-bar {
		background: var(--fg);
	}
	.rail-pip {
		position: absolute;
		right: 4px;
		top: 4px;
		width: 6px;
		height: 6px;
	}
</style>
