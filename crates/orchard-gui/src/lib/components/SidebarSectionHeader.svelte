<!--
  Section header — a clear visual chapter break between rows. Quiet
  typography (mono, small, dim), but with a top hairline divider so the
  eye reads the sidebar as discrete sections.
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
		<Icon name={icon} size={10} />
		<span class="section-header__label">{label.toLowerCase()}</span>
	</span>
	<span class="section-header__rhs">
		<span class="section-header__count">{count}</span>
		<Icon name={collapsed ? "chevron-right" : "chevron-down"} size={9} />
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
		border-top: 1px solid var(--color-border, rgba(255, 255, 255, 0.04));
		padding: 10px 14px 6px 14px;
		margin: 0;
		font: inherit;
		color: var(--color-text-dim, #797d86);
		cursor: pointer;
		text-align: left;
		font-family: "Geist Mono", ui-monospace, monospace;
		font-size: 9.5px;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		font-weight: 500;
	}
	/* First section in a list shouldn't have the top rule. */
	:global(.sidebar-list > section:first-of-type) > .section-header,
	:global(.sidebar-list > section.sidebar-group:first-child) > .section-header {
		border-top: none;
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
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.section-header__rhs {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		color: var(--color-text-dimmer, #5f6370);
	}
	.section-header__count {
		font-size: 9.5px;
		min-width: 1.25em;
		text-align: right;
	}
	/* Attn (blocked) tier — subtle amber tint, no shout. */
	.section-header.attn {
		color: #d99a55;
	}
	.section-header.attn:hover {
		color: #f1b06b;
	}
</style>
