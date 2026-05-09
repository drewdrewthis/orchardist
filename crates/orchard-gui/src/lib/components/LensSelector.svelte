<!-- Icon-only segmented control for the fleet's lens (attention/host/tmux/etc). -->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import type { Lens } from "$lib/data/types";

	type LensDef = { id: Lens; label: string; hint: string; icon: string };
	const LENSES: LensDef[] = [
		{ id: "attention", label: "Attention", hint: "Blocked / waiting / active", icon: "bell" },
		{ id: "recent", label: "Recent", hint: "All Claude sessions, freshest first", icon: "clock" },
		{ id: "tmux", label: "Tmux", hint: "Sessions · windows · panes", icon: "terminal" },
		{ id: "issue", label: "Issue", hint: "Open work — PRs linked to issues", icon: "issue" },
	];

	type Props = { value: Lens; onChange: (l: Lens) => void };
	let { value, onChange }: Props = $props();

	const idx = $derived(Math.max(0, LENSES.findIndex((l) => l.id === value)));
</script>

<div class="lens-pills" role="tablist">
	<div
		class="lens-thumb"
		style:left="calc(2px + {idx} * (100% - 4px) / {LENSES.length})"
		style:width="calc((100% - 4px) / {LENSES.length})"
	></div>
	{#each LENSES as l (l.id)}
		<button
			role="tab"
			aria-selected={l.id === value}
			data-on={l.id === value}
			title="{l.label} · {l.hint}"
			onclick={() => onChange(l.id)}
		>
			<Icon name={l.icon} size={14} />
		</button>
	{/each}
</div>
