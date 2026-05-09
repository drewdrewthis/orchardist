<!--
  Mobile layout: stack of fleet → conversation. Fills the whole viewport on a
  real phone; on desktop we render a maxed phone-width column for design dev.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import LensSelector from "./LensSelector.svelte";
	import FleetList from "./FleetList.svelte";
	import PeerCluster from "./PeerCluster.svelte";
	import ConvHeader from "./ConvHeader.svelte";
	import ChannelHeader from "./ChannelHeader.svelte";
	import ChatView from "./ChatView.svelte";
	import TerminalAttach from "./TerminalAttach.svelte";
	import type { AppStore } from "$lib/store.svelte";
	import type { ChannelItem, WorktreeItem } from "$lib/data/types";

	type Props = { store: AppStore };
	let { store }: Props = $props();

	const selected = $derived(store.activeItem);
</script>

<div class="mobile-shell">
	{#if !selected}
		<div class="mobile-top">
			<div class="mobile-top-row">
				<div style="display: flex; align-items: center; gap: 8px;">
					<span class="fleet-brand-mark"><Icon name="orchard" size={14} /></span>
					<span style="font-size: 17px; font-weight: 600; letter-spacing: -0.02em;">Orchard</span>
				</div>
				<div class="mobile-top-actions">
					<PeerCluster hosts={store.hosts} now={store.now} />
					<button class="iconbtn" onclick={() => store.toggleTheme()} aria-label="Theme">
						<Icon name={store.theme === "dark" ? "sun" : "moon"} size={15} />
					</button>
					<button class="iconbtn-primary" onclick={() => store.openNewConv()} aria-label="New">
						<Icon name="plus" size={15} />
					</button>
				</div>
			</div>
			<div style="display: flex; align-items: center; justify-content: flex-end;">
				<button
					class="iconbtn"
					onclick={() => store.openPalette()}
					aria-label="Search"
					style="height: 34px; padding: 0 10px; border: 0.5px solid var(--line); background: var(--surface-2); border-radius: 9px; flex: 1;"
				>
					<Icon name="search" size={16} />
					<span class="dimer" style="margin-left: 8px; font-size: 13px;">Search</span>
				</button>
			</div>
			<div style="display: flex;">
				<LensSelector value={store.lens} onChange={(l) => store.setLens(l)} />
			</div>
		</div>

		<FleetList
			items={store.visibleItems}
			hosts={store.hosts}
			lens={store.lens}
			now={store.now}
			density="comfortable"
			surface="mobile"
			selectedId={null}
			agents={store.agents}
			onSelect={(id) => store.mobileOpen(id)}
		/>

		<button class="mobile-fab" onclick={() => store.openNewConv()} aria-label="New conversation">
			<Icon name="plus" size={22} />
		</button>
	{:else}
		<div class="conv" style:flex="1" style:min-height="0">
			{#if selected.kind === "channel"}
				<ChannelHeader
					item={selected as ChannelItem}
					view={store.view}
					surface="mobile"
					agents={store.agents}
					onView={(v) => store.setView(v)}
					onFork={() => {
						const conv = store.visibleConversation;
						if (conv && conv.messages.length > 0) store.startFork(0, conv.messages[0]);
					}}
					onClose={() => store.mobileBack()}
					onJumpToAgent={() => {}}
				/>
			{:else}
				<ConvHeader
					item={selected as WorktreeItem}
					view={store.view}
					surface="mobile"
					sessionLive={!!(selected as WorktreeItem).session?.live}
					onView={(v) => store.setView(v)}
					onFork={() => {
						const conv = store.visibleConversation;
						if (conv && conv.messages.length > 0) store.startFork(0, conv.messages[0]);
					}}
					onClose={() => store.mobileBack()}
					onOpenContract={(id) => store.openContract(id)}
				/>
			{/if}
			{#if selected.kind === "channel"}
				<ChatView
					item={selected}
					conversation={store.visibleConversation || { itemId: '', recap: '', isChannel: true, messages: [] }}
					agents={store.agents}
					surface="mobile"
					now={store.now}
					composeText={store.composeText}
					setComposeText={(s) => (store.composeText = s)}
					onSend={() => store.send()}
					sending={store.sending}
					forkPreview={store.forkPreview}
					onStartFork={(i, m) => store.startFork(i, m)}
					onCommitFork={() => store.commitFork()}
					onCancelFork={() => store.cancelFork()}
				/>
			{:else}
				{@const pane = store.primaryPaneFor(selected)}
				{#if pane}
					<TerminalAttach
						argv={[
							"sh",
							"-c",
							`tmux select-pane -t ${pane.paneId} 2>/dev/null; exec tmux attach-session -t ${pane.session.name}`,
						]}
						label={`${pane.session.name} → ${pane.window.name} · ${pane.paneId}`}
					/>
				{:else}
					<div class="conv-empty">
						<div style="font-size: 13px; color: var(--fg-2);">No tmux pane in this worktree.</div>
					</div>
				{/if}
			{/if}
		</div>
	{/if}
</div>
