<!--
  Desktop split: sidebar (resizable, auto-collapse to rail under 200px) and a
  panes area on the right. Topbar sits above. Card aesthetic.

  Daemon-data flow: FleetTopBar / LensSidebar / SessionPane each own their
  own Houdini queries; this component is just layout glue around UI state
  (tabs, fullscreen, sidebar width).
-->
<script lang="ts">
	import { onMount } from "svelte";
	import FleetTopBar from "./FleetTopBar.svelte";
	import LensSelector from "./LensSelector.svelte";
	import LensSidebar from "./LensSidebar.svelte";
	import PanesArea from "./PanesArea.svelte";
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
			theme={store.theme}
			surface="desktop"
			now={store.now}
			onOpenPalette={() => store.openPalette()}
			onNewConv={() => store.openNewConv()}
			onToggleTheme={() => store.toggleTheme()}
			onToggleSidebar={() => store.toggleSidebar()}
		/>
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
						onSelect={(target, ev) => {
							const split = !!(ev && (ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.button === 1));
							store.openSession({ paneId: target.paneId, sessionUuid: target.sessionUuid, titleHint: target.titleHint }, { split });
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
					<div class="flex flex-col h-full py-2">
						<div class="flex-1 min-h-0 overflow-y-auto flex flex-col gap-0.5 px-1.5">
							{#each store.tabs as tab (tab.id)}
								{@const isOn = tab.id === store.activeTabId}
								<button
									class="group/rail relative flex items-center justify-center w-9 h-9 mx-auto border-0 rounded-[9px] cursor-default transition-colors duration-100 bg-transparent hover:bg-fg/[0.06] data-[on=true]:bg-surface-2"
									data-on={isOn}
									onclick={() => (store.activeTabId = tab.id)}
									title={tab.kind === "channel" ? "#" + tab.roomId : (tab.paneId || tab.sessionUuid?.slice(0, 8) || "session")}
								>
									<span
										class="absolute -left-1.5 top-2 bottom-2 w-0.5 rounded-[2px] bg-transparent transition-colors duration-100 group-data-[on=true]/rail:bg-fg"
									></span>
									{#if tab.kind === "channel"}
										<span class="channel-hash">#</span>
									{:else}
										<span class="pip ok absolute right-1 top-1 w-1.5 h-1.5"></span>
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
				onToggleFullscreen={() => store.toggleFullscreen()}
				onOpenPalette={() => store.openPalette()}
				onLaunch={() => store.openNewConv()}
			/>
		</div>
	</div>
</div>
