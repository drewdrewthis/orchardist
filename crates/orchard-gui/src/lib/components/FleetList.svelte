<!--
  Grouped fleet list. Renders nested tmux topology; falls back to flat groups
  for the other lenses. Selection forwards to the store via onSelect.
-->
<script lang="ts">
	import FleetItem from "./FleetItem.svelte";
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import { groupItems } from "$lib/util/groupItems";
	import type { Agent, Host, Item, Lens } from "$lib/data/types";

	type Props = {
		items: Item[];
		hosts: Host[];
		lens: Lens;
		now: number;
		density: "comfortable" | "compact";
		surface: "desktop" | "mobile";
		selectedId: string | null;
		agents: Agent[];
		onSelect: (id: string, ev?: MouseEvent) => void;
	};
	let { items, hosts, lens, now, density, surface, selectedId, agents, onSelect }: Props = $props();

	const groups = $derived(groupItems(items, lens, now));
	const downHosts = $derived(new Set(hosts.filter((h) => !h.reachable).map((h) => h.hostname)));

	function isPeerDown(it: Item): boolean {
		return it.kind === "worktree" ? downHosts.has(it.host) : false;
	}
</script>

<div class="fleet-list">
	{#each groups as g (g.key)}
		<div class="fleet-group" data-kind={g.kind || ""}>
			<div class="group-header" class:attn={g.key === "attn"}>
				<span style="display: inline-flex; align-items: center; gap: 6px;">
					{#if g.key === "attn"}
						<Icon name="alert" size={11} />
					{:else if g.kind === "channels"}
						<Icon name="message" size={11} />
					{:else if g.key.startsWith("host:")}
						<HostGlyph host={g.label} size={11} />
					{:else if g.kind === "tmux-session" && g.host}
						<HostGlyph host={g.host} size={11} />
						<Icon name="terminal" size={11} />
					{:else if g.kind === "detached" || g.kind === "none"}
						<Icon name="terminal" size={11} />
					{:else if g.key.startsWith("repo:")}
						<Icon name="git-branch" size={11} />
					{:else if g.key.startsWith("a:")}
						<Icon name="clock" size={11} />
					{:else if g.key.startsWith("issue:")}
						<Icon name="issue" size={11} />
					{/if}
					<span>{g.label}</span>
				</span>
				<span class="count">{g.items.length}</span>
			</div>

			{#if g.subgroups && g.subgroups.length > 0}
				{#each g.subgroups as sg (sg.key)}
					<div class="fleet-subgroup">
						<div class="subgroup-header">
							<span class="subgroup-rule"></span>
							<span class="mono dimer" style:font-size="10.5px" style:letter-spacing="0.2px">
								{sg.label}
							</span>
							<span class="dimest mono" style:font-size="10.5px">{sg.items.length}</span>
						</div>
						{#each sg.items as it (it.id)}
							<div class="fleet-nested">
								<FleetItem
									item={it}
									selected={it.id === selectedId}
									{now}
									{density}
									{surface}
									peerDown={isPeerDown(it)}
									{agents}
									{onSelect}
								/>
							</div>
						{/each}
					</div>
				{/each}
			{:else}
				{#each g.items as it (it.id)}
					<FleetItem
						item={it}
						selected={it.id === selectedId}
						{now}
						{density}
						{surface}
						peerDown={isPeerDown(it)}
						{agents}
						{onSelect}
					/>
				{/each}
			{/if}
		</div>
	{/each}

	{#if groups.length === 0}
		<div style="padding: 36px 16px; text-align: center; font-size: 13px;">
			<span class="dimer">No items match these filters.</span>
		</div>
	{/if}
</div>
