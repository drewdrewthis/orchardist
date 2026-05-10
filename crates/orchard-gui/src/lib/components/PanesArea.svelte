<!--
  Side-by-side panes with drag-to-resize between them. Up to 3 active panes.
  Empty state shows palette/new-conv launcher.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import SessionPane from "./SessionPane.svelte";
	import ChannelPane from "./ChannelPane.svelte";
	import type { Tab } from "$lib/store.svelte";
	import type {
		Conversation,
		ConvView,
		ForkPreview,
		Message,
	} from "$lib/data/types";

	type Props = {
		tabs: Tab[];
		activeTabId: string | null;
		paneSizes: number[];
		fullscreen: boolean;
		view: ConvView;
		conversation: Conversation;
		now: number;
		statusVariant?: "ticks" | "dots" | "minimal" | "text";
		composeText: string;
		setComposeText: (s: string) => void;
		sending: { tempId: string; status: string } | null;
		forkPreview: ForkPreview | null;
		onActivateTab: (id: string) => void;
		onCloseTab: (id: string) => void;
		onResizePanes: (sizes: number[]) => void;
		onView: (v: ConvView) => void;
		onSetTabView: (tabId: string, v: ConvView) => void;
		onSend: () => void;
		onFork: () => void;
		onStartFork: (idx: number, m: Message) => void;
		onCommitFork: () => void;
		onCancelFork: () => void;
		onToggleFullscreen: () => void;
		onOpenPalette: () => void;
		onLaunch: () => void;
	};
	let {
		tabs,
		activeTabId,
		paneSizes,
		fullscreen,
		view,
		conversation,
		now,
		statusVariant = "ticks",
		composeText,
		setComposeText,
		sending,
		forkPreview,
		onActivateTab,
		onCloseTab,
		onResizePanes,
		onView,
		onSetTabView,
		onSend,
		onFork,
		onStartFork,
		onCommitFork,
		onCancelFork,
		onToggleFullscreen,
		onOpenPalette,
		onLaunch,
	}: Props = $props();

	let rowEl: HTMLDivElement | undefined = $state();

	const sizes = $derived(
		paneSizes.length === tabs.length ? paneSizes : tabs.map(() => 1 / Math.max(1, tabs.length)),
	);

	function startResize(e: MouseEvent, splitIdx: number) {
		e.preventDefault();
		const row = rowEl;
		if (!row) return;
		const totalW = row.getBoundingClientRect().width || 1;
		const startX = e.clientX;
		const startSizes = [...sizes];
		const min = 0.12;
		const onMove = (ev: MouseEvent) => {
			const dx = (ev.clientX - startX) / totalW;
			const next = [...startSizes];
			let a = next[splitIdx] + dx;
			let b = next[splitIdx + 1] - dx;
			if (a < min) {
				b += a - min;
				a = min;
			}
			if (b < min) {
				a += b - min;
				b = min;
			}
			next[splitIdx] = a;
			next[splitIdx + 1] = b;
			onResizePanes(next);
		};
		const onUp = () => {
			window.removeEventListener("mousemove", onMove);
			window.removeEventListener("mouseup", onUp);
			document.body.style.cursor = "";
			document.body.style.userSelect = "";
		};
		document.body.style.cursor = "col-resize";
		document.body.style.userSelect = "none";
		window.addEventListener("mousemove", onMove);
		window.addEventListener("mouseup", onUp);
	}
</script>

{#if tabs.length === 0}
	<div class="panes-empty">
		<div class="conv-empty">
			<Icon name="orchard" size={28} />
			<div style="font-size: 14px; font-weight: 500; color: var(--fg-2);">
				No conversations open
			</div>
			<!-- #540 E1: button row spacing — fixed-width buttons with stable
			     internal gaps so labels + keyboard glyphs don't collide
			     into each other regardless of viewport width. -->
			<div class="empty-actions mono">
				<button class="btn-tonal empty-action" onclick={onOpenPalette}>
					<Icon name="command" size={13} />
					<span class="action-label">Search</span>
					<span class="action-kbd"><span class="kbd">⌘</span><span class="kbd">K</span></span>
				</button>
				<button class="btn-tonal empty-action" onclick={onLaunch}>
					<Icon name="plus" size={13} />
					<span class="action-label">New</span>
					<span class="action-kbd"><span class="kbd">⌘</span><span class="kbd">N</span></span>
				</button>
			</div>
		</div>
	</div>
{:else}
	<div bind:this={rowEl} class="panes-row" data-count={tabs.length}>
		{#each tabs as tab, idx (tab.id)}
			{#if idx > 0}
				<div
					class="pane-resizer"
					onmousedown={(e) => startResize(e, idx - 1)}
					role="separator"
					aria-orientation="vertical"
					title="Drag to resize"
				></div>
			{/if}
			<div class="pane-flex" style:flex="{sizes[idx] || 1 / tabs.length} 1 0" style:min-width="0">
				{#if tab.kind === "session"}
					<SessionPane
						paneId={tab.paneId}
						sessionUuid={tab.sessionUuid}
						active={tab.id === activeTabId}
						paneCount={tabs.length}
						isLast={idx === tabs.length - 1}
						fullscreen={idx === tabs.length - 1 ? fullscreen : null}
						{now}
						surface="desktop"
						view={tab.view}
						onSetView={(v) => onSetTabView(tab.id, v)}
						onActivate={() => onActivateTab(tab.id)}
						onClose={() => onCloseTab(tab.id)}
						onToggleFullscreen={idx === tabs.length - 1 ? onToggleFullscreen : undefined}
					/>
				{:else}
					<ChannelPane
						roomId={tab.roomId}
						active={tab.id === activeTabId}
						paneCount={tabs.length}
						isLast={idx === tabs.length - 1}
						fullscreen={idx === tabs.length - 1 ? fullscreen : null}
						{conversation}
						{now}
						{statusVariant}
						{composeText}
						{setComposeText}
						{onSend}
						sending={tab.id === activeTabId ? sending : null}
						forkPreview={tab.id === activeTabId ? forkPreview : null}
						{onStartFork}
						{onCommitFork}
						{onCancelFork}
						onActivate={() => onActivateTab(tab.id)}
						onClose={() => onCloseTab(tab.id)}
						onToggleFullscreen={idx === tabs.length - 1 ? onToggleFullscreen : undefined}
					/>
				{/if}
			</div>
		{/each}
	</div>
{/if}

<style>
	.pane-flex { display: flex; flex-direction: column; height: 100%; }

	/* #540 E1 — empty-state button row.
	   Buttons get stable internal layout (icon · label · keyboard glyph)
	   so the row reads cleanly at every viewport width. The container
	   gaps spread with `gap: 12px` so the buttons never touch. */
	.empty-actions {
		display: flex;
		gap: 12px;
		align-items: center;
		justify-content: center;
		flex-wrap: wrap;
	}
	.empty-action {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		min-width: 120px;
		justify-content: center;
		padding: 6px 12px;
	}
	.empty-action .action-label {
		flex: 0 0 auto;
	}
	.empty-action .action-kbd {
		display: inline-flex;
		gap: 3px;
		margin-left: 6px;
		opacity: 0.7;
	}
</style>
