<!--
  Mobile layout: stack of fleet → conversation. Single-tab; back button
  pops back to the lens list. Uses SessionPane / ChannelPane like the
  desktop, just always one at a time.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import LensSelector from "./LensSelector.svelte";
	import LensSidebar from "./LensSidebar.svelte";
	import PeerCluster from "./PeerCluster.svelte";
	import SessionPane from "./SessionPane.svelte";
	import ChannelPane from "./ChannelPane.svelte";
	import type { AppStore } from "$lib/store.svelte";

	type Props = { store: AppStore };
	let { store }: Props = $props();

	const tab = $derived(store.activeTab);
</script>

<div class="mobile-shell">
	{#if !tab}
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

		<LensSidebar
			now={store.now}
			density="comfortable"
			surface="mobile"
			selectedId={null}
			agents={store.agents}
			onSelect={(target) => {
				if (target.kind === "channel") store.openChannel(target.roomId);
				else store.openSession({ paneId: target.paneId, sessionUuid: target.sessionUuid });
			}}
		/>

		<button class="mobile-fab" onclick={() => store.openNewConv()} aria-label="New conversation">
			<Icon name="plus" size={22} />
		</button>
	{:else}
		<div style:flex="1" style:min-height="0">
			{#if tab.kind === "session"}
				<SessionPane
					paneId={tab.paneId}
					sessionUuid={tab.sessionUuid}
					active={true}
					paneCount={1}
					isLast={true}
					fullscreen={null}
					now={store.now}
					surface="mobile"
					onActivate={() => {}}
					onClose={() => store.mobileBack()}
				/>
			{:else}
				<ChannelPane
					roomId={tab.roomId}
					active={true}
					paneCount={1}
					isLast={true}
					fullscreen={null}
					conversation={store.visibleConversation || { itemId: '', recap: '', isChannel: true, messages: [] }}
					agents={store.agents}
					now={store.now}
					composeText={store.composeText}
					setComposeText={(s) => (store.composeText = s)}
					onSend={() => store.send()}
					sending={store.sending}
					forkPreview={store.forkPreview}
					onStartFork={(i, m) => store.startFork(i, m)}
					onCommitFork={() => store.commitFork()}
					onCancelFork={() => store.cancelFork()}
					onJumpToAgent={() => {}}
					onActivate={() => {}}
					onClose={() => store.mobileBack()}
				/>
			{/if}
		</div>
	{/if}
</div>
