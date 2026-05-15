<!--
  Refined section header used across every lens. Lowercase label, normal
  weight, dim leader to the count. Color is reserved for state semantics
  (blocked tier uses `attn`); ordinary headers stay neutral.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";

	type Props = {
		icon: string;
		label: string;
		count: number;
		collapsed: boolean;
		attn?: boolean;
		onToggle: () => void;
	};
	let { icon, label, count, collapsed, attn = false, onToggle }: Props = $props();
</script>

<button
	class="section-header"
	class:attn
	aria-expanded={!collapsed}
	onclick={onToggle}
>
	<span class="section-header__lhs">
		<Icon name={icon} size={11} />
		<span class="section-header__label">{label.toLowerCase()}</span>
	</span>
	<span class="section-header__rhs">
		<span class="section-header__count">{count}</span>
		<Icon name={collapsed ? "chevron-right" : "chevron-down"} size={10} />
	</span>
</button>

<style>
	.section-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		width: 100%;
		background: none;
		border: none;
		padding: 6px 12px 4px;
		margin: 0;
		font: inherit;
		color: var(--color-text-muted, #9ca0a8);
		cursor: pointer;
		text-align: left;
		font-size: 10.5px;
		letter-spacing: 0.04em;
		text-transform: none;
		font-weight: 500;
	}
	.section-header:hover {
		color: var(--color-text, #d4d7dc);
	}
	.section-header:focus-visible {
		outline: 1px solid var(--color-accent, #6366f1);
		outline-offset: -1px;
		border-radius: 2px;
	}
	.section-header__lhs {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		min-width: 0;
	}
	.section-header__label {
		font-variant: tabular-nums;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.section-header__rhs {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		color: var(--color-text-dim, #6c707a);
	}
	.section-header__count {
		font-family: var(--font-mono, ui-monospace, monospace);
		font-size: 10px;
		min-width: 1.25em;
		text-align: right;
	}
	/* attn tier (blocked) — restrained amber accent, label only */
	.section-header.attn {
		color: #d99a55;
	}
	.section-header.attn:hover {
		color: #f1b06b;
	}
</style>
