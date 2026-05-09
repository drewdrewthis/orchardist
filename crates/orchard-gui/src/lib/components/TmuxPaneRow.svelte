<!--
  Sidebar row for a tmux pane in the tmux lens. The pane is the unit;
  parent window/session are already shown by the surrounding subgroup
  header. This row shows pane-specific signal: pane-id, command, claude
  state if any, "here" flag if a client is currently watching it.
-->
<script lang="ts">
	import { relTime } from "$lib/util/format";
	import { getStore } from "$lib/store.svelte";
	import type { PaneCardT } from "$lib/data/lenses";

	type Props = {
		pane: PaneCardT;
		here: boolean;
		now: number;
		density: "comfortable" | "compact";
		surface: "desktop" | "mobile";
		selected: boolean;
		onSelect: (id: string, ev?: MouseEvent) => void;
	};
	let { pane, here, now, density, surface, selected, onSelect }: Props = $props();

	const store = getStore();
	const claudeState = $derived(pane.claudeInstance?.state);
	// Prefer the JSONL-derived timestamp (conversations.lastSeenAt) over
	// claudeInstance.lastActivityAt, which the daemon currently leaves null.
	const lastMs = $derived(() => {
		const uuid = pane.claudeInstance?.sessionUuid;
		if (uuid) {
			const v = store.lensSnapshots.tmux.lastSeenByUuid[uuid];
			if (v) return v;
		}
		return pane.claudeInstance?.lastActivityAt
			? Date.parse(pane.claudeInstance.lastActivityAt) || 0
			: 0;
	});
	const cwdSuffix = $derived(() => {
		const cwd = pane.process?.cwd;
		if (!cwd) return "";
		// Trim home prefix and show last two segments — stable identity in
		// the row width budget.
		const trimmed = cwd.replace(/^\/Users\/[^/]+\//, "~/");
		return trimmed;
	});
</script>

<div
	class="fleet-item"
	data-selected={selected}
	data-density={density}
	data-here={here}
	onclick={(e) => onSelect(pane.paneId, e)}
	onkeydown={(e) => {
		if (e.key === "Enter" || e.key === " ") {
			e.preventDefault();
			onSelect(pane.paneId);
		}
	}}
	role="button"
	tabindex="0"
>
	<div class="fleet-item-main">
		<span
			class="pip {claudeState === 'working' ? 'ok' : claudeState === 'no_claude' ? 'idle' : 'ok'}"
			title={claudeState ?? "no claude"}
		></span>
		<div class="fleet-item-body">
			<div class="fleet-item-title-row">
				<span class="fleet-item-title mono">{pane.paneId}</span>
				{#if here}
					<span class="here-badge mono" title="A client is currently watching this pane">here</span>
				{/if}
			</div>
			<div class="fleet-item-sub">
				<span class="mono dimer" style:font-size="11px">{pane.currentCommand}</span>
				{#if pane.claudeInstance}
					<span class="dimest">·</span>
					<span class="mono dimer" style:font-size="11px">
						{pane.claudeInstance.state}
					</span>
					{#if lastMs() > 0}
						<span class="dimest">·</span>
						<span class="mono dimer" style:font-size="11px">{relTime(lastMs(), now)}</span>
					{/if}
				{/if}
				{#if pane.process?.cwd && surface !== "mobile"}
					<span class="dimest">·</span>
					<span class="dimer mono" style:font-size="10.5px">{cwdSuffix()}</span>
				{/if}
			</div>
		</div>
	</div>
</div>
