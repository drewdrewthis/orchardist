<!-- App-wide topbar: brand + search trigger + peer cluster + quota + theme + new-conv. -->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import PeerCluster from "./PeerCluster.svelte";
	import type { Account, Host, Theme, Surface } from "$lib/data/types";

	type Props = {
		hosts: Host[];
		account: Account | null;
		theme: Theme;
		surface: Surface;
		now: number;
		onOpenPalette: () => void;
		onNewConv: () => void;
		onToggleTheme: () => void;
		onToggleSidebar?: () => void;
	};
	let {
		hosts,
		account,
		theme,
		surface,
		now,
		onOpenPalette,
		onNewConv,
		onToggleTheme,
		onToggleSidebar,
	}: Props = $props();

	const quotaPct = $derived(
		account && account.quotaCap > 0 ? account.quotaUsed / account.quotaCap : 0,
	);
	const overQuota = $derived(quotaPct > 0.8);
</script>

<div class="fleet-topbar">
	<div class="fleet-topbar-inner">
		{#if surface === "desktop" && onToggleSidebar}
			<button class="iconbtn" onclick={onToggleSidebar} aria-label="Toggle sidebar">
				<Icon name="sidebar" size={16} />
			</button>
		{/if}

		<div class="fleet-brand no-select">
			<span class="fleet-brand-mark"><Icon name="orchard" size={14} /></span>
			<span class="fleet-brand-name">Orchard</span>
		</div>

		<div class="fleet-topbar-spacer"></div>

		<button class="fleet-search-btn" onclick={onOpenPalette} aria-label="Search">
			<Icon name="search" size={14} />
			<span class="fleet-search-placeholder">Search or jump to…</span>
			{#if surface === "desktop"}
				<span style="display: inline-flex; gap: 3px; margin-left: auto;">
					<span class="kbd">⌘</span><span class="kbd">K</span>
				</span>
			{/if}
		</button>

		<PeerCluster {hosts} {now} />

		{#if account && account.quotaCap > 0}
			<div class="fleet-quota">
				<span class="mono dimer" style:font-size="11px">{account.quotaUsed}/{account.quotaCap}</span>
				<span
					style="display: inline-block; width: 28px; height: 4px; border-radius: 2px; background: var(--line-2); position: relative; overflow: hidden;"
				>
					<i
						style:position="absolute"
						style:left="0"
						style:top="0"
						style:bottom="0"
						style:width="{Math.min(100, quotaPct * 100)}%"
						style:background={overQuota ? "var(--attn)" : "var(--fg-3)"}
						style:border-radius="2px"
					></i>
				</span>
			</div>
		{/if}

		<button class="iconbtn" onclick={onToggleTheme} aria-label="Toggle theme">
			<Icon name={theme === "dark" ? "sun" : "moon"} size={15} />
		</button>

		<button class="iconbtn-primary" onclick={onNewConv} title="New conversation · ⌘N" aria-label="New">
			<Icon name="plus" size={15} />
		</button>
	</div>
</div>
