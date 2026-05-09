<!-- Ambient cluster of host pips with hover tooltip showing per-host load. -->
<script lang="ts">
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import { relTime } from "$lib/util/format";
	import type { Host } from "$lib/data/types";

	type Props = { hosts: Host[]; now: number };
	let { hosts, now }: Props = $props();
</script>

<div class="peer-cluster" title="Peer health">
	{#each hosts as h (h.id)}
		<div class="peer-pip-wrap" data-down={!h.reachable}>
			<span
				class="pip {h.reachable ? (h.load.cpu > 85 ? 'attn' : 'ok') : 'bad'}"
				aria-label={h.hostname}
			></span>
			<div class="peer-tip glass-strong">
				<div style="display: flex; align-items: center; gap: 6px;">
					<HostGlyph host={h.hostname} size={16} />
					<b class="mono" style="font-size: 12px;">{h.hostname}</b>
					<span class="dimer mono" style="font-size: 11px;">{h.os.split(" ")[0]}</span>
				</div>
				{#if h.reachable}
					<div class="peer-tip-grid">
						<span class="dimer">CPU</span>
						<span class="mono tnum">{h.load.cpu}%</span>
						<span class="dimer">Mem</span>
						<span class="mono tnum">{h.load.mem}%</span>
						<span class="dimer">Disk</span>
						<span class="mono tnum">{h.load.disk}%</span>
					</div>
				{:else}
					<div class="dimer mono" style="font-size: 11px; margin-top: 4px;">
						unreachable · last seen {relTime(h.lastSeenAt, now)}
					</div>
				{/if}
			</div>
		</div>
	{/each}
</div>
