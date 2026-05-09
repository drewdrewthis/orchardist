<!--
  Header for a channel/room conversation. Replaces ConvHeader when the active
  item is a channel. Shows participant rail with per-agent tmux attach + jump
  to that agent's running session.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import ViewSwitcher from "./ViewSwitcher.svelte";
	import type { Agent, ChannelItem, ConvView, Surface } from "$lib/data/types";

	type Props = {
		item: ChannelItem;
		view: ConvView;
		switcherVariant?: "segmented" | "icon-toggle";
		surface: Surface;
		agents: Agent[];
		onView: (v: ConvView) => void;
		onFork: () => void;
		onClose?: () => void;
		onJumpToAgent: (agentId: string) => void;
	};
	let {
		item,
		view,
		switcherVariant = "segmented",
		surface,
		agents,
		onView,
		onFork,
		onClose,
		onJumpToAgent,
	}: Props = $props();

	let copied = $state<string | null>(null);
	let inviteOpen = $state(false);

	const participants = $derived(agents.filter((a) => item.participants.includes(a.id)));
	const others = $derived(agents.filter((a) => !item.participants.includes(a.id)));

	function tmuxFor(a: Agent) {
		return `agent-${a.id.replace(/^a\./, "")}`;
	}
	function attachCmd(a: Agent) {
		return `tmux -L orchard attach -t ${tmuxFor(a)}`;
	}

	async function copy(key: string, text: string) {
		try {
			await navigator.clipboard.writeText(text);
		} catch {}
		copied = key;
		setTimeout(() => {
			if (copied === key) copied = null;
		}, 1200);
	}
</script>

<div class="conv-header is-channel">
	<div class="conv-header-row">
		{#if surface === "mobile" && onClose}
			<button class="iconbtn" onclick={onClose} aria-label="Back" style="margin-left: -6px;">
				<Icon name="arrow-left" size={16} />
			</button>
		{/if}
		<div class="conv-title-block">
			<div class="conv-title-row">
				<span
					class="channel-hash"
					style="width: 18px; height: 18px; font-size: 12px; line-height: 16px;">#</span
				>
				<span class="conv-title">{item.title.replace(/^#?\s*/, "").replace(/\s·\s.*/, "")}</span>
				{#if item.topic}
					<span
						class="dimer"
						style="font-size: 12px; margin-left: 6px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"
					>
						· {item.topic}
					</span>
				{/if}
			</div>
		</div>
		<div class="conv-header-actions">
			<button class="iconbtn" onclick={onFork} title="Fork conversation">
				<Icon name="git-fork" size={15} />
			</button>
			<button class="iconbtn" title="More"><Icon name="more" size={15} /></button>
			{#if surface === "desktop"}
				<span class="conv-header-divider" aria-hidden="true"></span>
				<ViewSwitcher value={view} onChange={onView} variant={switcherVariant} />
			{/if}
		</div>
	</div>

	<div class="channel-roster">
		{#each participants as a (a.id)}
			<div class="channel-pill" title="{a.name} · {a.role} · {a.host}">
				<span
					class="agent-avatar"
					style="background: oklch(0.62 0.13 {a.hue}); width: 16px; height: 16px; font-size: 9.5px; border-radius: 4px; display: inline-flex; align-items: center; justify-content: center; color: white; font-weight: 600;"
				>
					{a.avatar}
				</span>
				<span class="channel-pill-name">{a.name}</span>
				<span class="dimest mono" style="font-size: 10px;">{a.model}</span>
				<button
					class="channel-pill-jump"
					title="Open {a.name}'s session in a new pane"
					onclick={() => onJumpToAgent(a.id)}
				>
					<Icon name="chat" size={10} />
					<span>session</span>
				</button>
				<button
					class="channel-pill-tmux"
					title="Copy: {attachCmd(a)}"
					onclick={() => copy(a.id, attachCmd(a))}
				>
					<Icon name="terminal" size={10} />
					<span class="mono">{tmuxFor(a)}</span>
					<Icon name={copied === a.id ? "check" : "copy"} size={10} />
				</button>
			</div>
		{/each}

		<div style="position: relative;">
			<button class="channel-invite" onclick={() => (inviteOpen = !inviteOpen)} title="Invite an agent">
				<Icon name="plus" size={11} />
				<span>Invite</span>
			</button>
			{#if inviteOpen}
				<div
					class="channel-invite-menu glass-strong"
					role="menu"
					tabindex="-1"
					onmouseleave={() => (inviteOpen = false)}
				>
					<div
						class="dimer mono"
						style="font-size: 10.5px; padding: 6px 10px 4px; letter-spacing: 0.06em;"
					>
						AVAILABLE AGENTS
					</div>
					{#if others.length === 0}
						<div class="dimer" style="padding: 8px 10px; font-size: 12px;">
							All agents are already here.
						</div>
					{:else}
						{#each others as a (a.id)}
							<button class="channel-invite-item" onclick={() => (inviteOpen = false)}>
								<span
									class="agent-avatar"
									style="background: oklch(0.62 0.13 {a.hue}); width: 18px; height: 18px; font-size: 10px; border-radius: 4px; display: inline-flex; align-items: center; justify-content: center; color: white; font-weight: 600;"
								>
									{a.avatar}
								</span>
								<span style:flex="1">{a.name}</span>
								<span class="dimer" style:font-size="11px">{a.role}</span>
								<span class="dimest mono" style:font-size="10.5px">{a.host}</span>
							</button>
						{/each}
					{/if}
				</div>
			{/if}
		</div>
	</div>
</div>

<style>
	.channel-invite-menu {
		position: absolute;
		top: calc(100% + 4px);
		left: 0;
		z-index: 30;
		min-width: 280px;
		padding: 4px 0;
		border: 0.5px solid var(--line);
		border-radius: 8px;
		box-shadow: 0 12px 32px rgba(0, 0, 0, 0.35);
	}
	.channel-invite-item {
		display: flex;
		align-items: center;
		gap: 8px;
		width: 100%;
		padding: 7px 10px;
		background: transparent;
		border: 0;
		font-size: 12px;
		color: var(--fg);
		text-align: left;
		cursor: default;
	}
	.channel-invite-item:hover {
		background: var(--surface-2);
	}
</style>
