<!--
  Ambient cluster of host pips with hover tooltip showing per-host load.
  Reads schema-aligned `HostRow` directly from the `hostsStore` Houdini
  query — `resourceLoad` is null until the daemon's load sampler has fired,
  so the tooltip says "—" rather than fabricating zeros.
-->
<script lang="ts">
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import { relTime } from "$lib/util/format";
	import type { HostRow } from "$lib/data/daemon-stores";

	type Props = { hosts: HostRow[]; now: number };
	let { hosts, now }: Props = $props();
</script>

<div class="peer-cluster" title="Peer health">
	{#each hosts as h (h.id)}
		{@const cpu = h.resourceLoad?.cpuPercent ?? null}
		<div class="peer-pip-wrap" data-down={!h.reachable}>
			<span
				class="pip {h.reachable ? (cpu !== null && cpu > 85 ? 'attn' : 'ok') : 'bad'}"
				aria-label={h.hostname}
			></span>
			<div class="peer-tip glass-strong">
				<div style="display: flex; align-items: center; gap: 6px;">
					<HostGlyph host={h.hostname} size={16} />
					<b class="mono" style="font-size: 12px;">{h.hostname}</b>
					<span class="dimer mono" style="font-size: 11px;">{h.os.split(" ")[0]}</span>
				</div>
				{#if h.reachable}
					{#if h.resourceLoad}
						<div class="peer-tip-grid">
							<span class="dimer">CPU</span>
							<span class="mono tnum">{h.resourceLoad.cpuPercent.toFixed(0)}%</span>
							<span class="dimer">Mem</span>
							<span class="mono tnum">{h.resourceLoad.memPercent.toFixed(0)}%</span>
							<span class="dimer">Disk</span>
							<span class="mono tnum">{h.resourceLoad.diskPercent.toFixed(0)}%</span>
							<span class="dimer">Load</span>
							<span class="mono tnum">
								{h.resourceLoad.loadAvg1m.toFixed(2)} · {h.resourceLoad.loadAvg5m.toFixed(2)} · {h.resourceLoad.loadAvg15m.toFixed(2)}
							</span>
						</div>
					{:else}
						<div class="dimer mono" style="font-size: 11px; margin-top: 4px;">
							no resource sample yet
						</div>
					{/if}
					{#if h.kernel}
						<div class="dimest mono" style="font-size: 10.5px; margin-top: 4px;">{h.kernel}</div>
					{/if}
				{:else}
					<div class="dimer mono" style="font-size: 11px; margin-top: 4px;">
						unreachable · last seen {relTime(h.lastSeenAt, now)}
					</div>
				{/if}
			</div>
		</div>
	{/each}
</div>
