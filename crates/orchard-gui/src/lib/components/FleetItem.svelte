<!--
  One row in the fleet sidebar. Worktree or channel; renders host + branch +
  signals + sparkline. Click selects (which the parent forwards to the store).
-->
<script lang="ts">
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import Spark from "$lib/icons/Spark.svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import SignalRow from "./SignalRow.svelte";
	import AgentStack from "./AgentStack.svelte";
	import { relTime } from "$lib/util/format";
	import type { Agent, Item } from "$lib/data/types";

	type Props = {
		item: Item;
		selected: boolean;
		now: number;
		density: "comfortable" | "compact";
		surface: "desktop" | "mobile";
		peerDown?: boolean;
		agents?: Agent[];
		onSelect: (id: string, ev?: MouseEvent) => void;
	};
	let {
		item,
		selected,
		now,
		density,
		surface,
		peerDown = false,
		agents = [],
		onSelect,
	}: Props = $props();

	const isStale = $derived(item.status === "stale" || peerDown);
	const liveDot = $derived(item.kind === "worktree" && item.session?.live);
	const isChannel = $derived(item.kind === "channel");
	const channelAgents = $derived(
		isChannel && item.kind === "channel"
			? agents.filter((a) => item.participants.includes(a.id))
			: [],
	);
</script>

<div
	class="fleet-item"
	class:is-channel={isChannel}
	data-selected={selected}
	data-density={density}
	data-stale={isStale}
	onclick={(e) => onSelect(item.id, e)}
	onkeydown={(e) => {
		if (e.key === "Enter" || e.key === " ") {
			e.preventDefault();
			onSelect(item.id);
		}
	}}
	role="button"
	tabindex="0"
>
	<div class="fleet-item-main">
		{#if isChannel}
			<span class="channel-hash" title="Channel">#</span>
		{:else}
			<span class="pip {item.status}" title={item.status}></span>
		{/if}
		<div class="fleet-item-body">
			<div class="fleet-item-title-row">
				<span class="fleet-item-title" style:opacity={isStale ? 0.5 : 1}>{item.title}</span>
				<SignalRow {item} />
			</div>
			<div class="fleet-item-sub">
				{#if isChannel && item.kind === "channel"}
					<AgentStack agents={channelAgents} size={14} />
					<span class="dimer" style:font-size="11.5px">
						{channelAgents.length} agent{channelAgents.length === 1 ? "" : "s"}
					</span>
					{#if item.topic && surface !== "mobile"}
						<span class="dimest">·</span>
						<span class="dimer" style:font-size="11.5px">{item.topic}</span>
					{/if}
				{:else if item.kind === "worktree"}
					<HostGlyph host={item.host} size={12} dim={isStale} />
					{#if surface !== "mobile"}
						<span class="mono dimer">{item.host}</span>
						<span class="dimest">·</span>
					{/if}
					<span class="mono dimer branch">{item.branch}</span>
					{#if item.attentionReason && density === "comfortable" && surface !== "mobile"}
						<span class="dimest">·</span>
						<span class="reason-inline" title={item.attentionReason}>
							<Icon name="alert" size={10} />
							<span>{item.attentionReason}</span>
						</span>
					{/if}
				{/if}
			</div>
		</div>
		<div class="fleet-item-meta">
			{#if liveDot}
				<span class="pip live" title="Live"></span>
			{/if}
			<span class="mono dimer tnum" style:font-size="11px">{relTime(item.lastActivity, now)}</span>
			{#if density !== "compact" && surface !== "mobile" && item.sparkline.length > 0}
				<Spark
					values={item.sparkline}
					w={48}
					h={10}
					color={item.status === "attn" ? "var(--attn)" : "var(--fg-3)"}
				/>
			{/if}
		</div>
	</div>
</div>

<style>
	.branch {
		max-width: 220px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.reason-inline {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		color: var(--attn);
		font-size: 11px;
		max-width: 240px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
</style>
