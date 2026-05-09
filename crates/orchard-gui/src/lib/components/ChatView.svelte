<!--
  Chat scrollback + recap header + composer + fork preview. Auto-scrolls to
  bottom on message change.
-->
<script lang="ts">
	import { tick } from "svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import ChatMessage from "./ChatMessage.svelte";
	import Composer from "./Composer.svelte";
	import type {
		Agent,
		ChannelItem,
		Conversation,
		ForkPreview,
		Message,
		WorktreeItem,
	} from "$lib/data/types";

	type ForkLocal = { fromIdx: number; msg: Message };

	type Props = {
		item: WorktreeItem | ChannelItem;
		conversation: Conversation;
		agents: Agent[];
		surface: "desktop" | "mobile";
		now: number;
		statusVariant?: "ticks" | "dots" | "minimal" | "text";
		composeText: string;
		setComposeText: (s: string) => void;
		onSend: () => void;
		sending: { tempId: string; status: string } | null;
		forkPreview: ForkLocal | null;
		onStartFork: (idx: number, m: Message) => void;
		onCommitFork: () => void;
		onCancelFork: () => void;
	};
	let {
		item,
		conversation,
		agents,
		surface,
		now,
		statusVariant = "ticks",
		composeText,
		setComposeText,
		onSend,
		sending,
		forkPreview,
		onStartFork,
		onCommitFork,
		onCancelFork,
	}: Props = $props();

	let recapOpen = $state(false);
	let scrollEl: HTMLDivElement | undefined = $state();

	$effect(() => {
		const _ = conversation.messages.length + (sending ? 1 : 0);
		void _;
		void item.id;
		tick().then(() => {
			if (scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
		});
	});
</script>

<div class="chat">
	<div class="chat-recap" class:collapsed={!recapOpen} class:open={recapOpen}>
		<button
			class="chat-recap-toggle"
			onclick={() => (recapOpen = !recapOpen)}
			aria-expanded={recapOpen}
			title={recapOpen ? "Hide recap" : "Show recap"}
		>
			<Icon name="docs" size={11} />
			<span
				class="dimest mono"
				style="font-size: 10.5px; font-weight: 600; letter-spacing: 0.06em;"
			>
				RECAP
			</span>
			{#if !recapOpen}
				<span class="chat-recap-peek dimer">{conversation.recap}</span>
			{/if}
			<Icon name="chevron-down" size={11} />
		</button>
		{#if recapOpen}
			<p>{conversation.recap}</p>
		{/if}
	</div>

	<div class="chat-scroll" bind:this={scrollEl}>
		{#each conversation.messages as msg, i (msg.id)}
			{@const prev = conversation.messages[i - 1]}
			{@const grouped = !!(prev && prev.role === msg.role && msg.ts - prev.ts < 60_000 * 5)}
			<ChatMessage
				{msg}
				{grouped}
				isChannel={item.kind === "channel"}
				{agents}
				idx={i}
				{statusVariant}
				onForkFrom={(_i, m) => onStartFork(_i, m)}
				onReset={() => {}}
			/>
		{/each}

		{#if sending}
			<ChatMessage
				msg={{ id: "sending-typing", role: "agent", text: "", status: "pending", ts: now, typing: true } as Message & { typing: boolean }}
				grouped={false}
				isChannel={false}
				{agents}
				idx={conversation.messages.length}
				{statusVariant}
				onForkFrom={() => {}}
				onReset={() => {}}
			/>
		{/if}

		{#if forkPreview}
			<div class="chat-fork-preview fadeIn">
				<div class="chat-fork-header">
					<Icon name="git-fork" size={13} />
					<b>Forking from message #{forkPreview.fromIdx + 1}</b>
					<span class="dimer" style="font-size: 11px;">creates new session anchor</span>
				</div>
				<div class="chat-fork-body">
					<div style="display: flex; align-items: center; gap: 8px;">
						<span class="dimer" style="font-size: 11px; width: 56px;">parent</span>
						<span class="mono" style="font-size: 11.5px;">
							{item.kind === "worktree" ? item.session?.uuid || "-" : item.id}
						</span>
					</div>
					<div style="display: flex; align-items: center; gap: 8px;">
						<span class="dimer" style="font-size: 11px; width: 56px;">new</span>
						<span class="mono" style="font-size: 11.5px; color: var(--accent);">
							fork-{(item.kind === "worktree" && item.session?.uuid
								? item.session.uuid.slice(0, 4)
								: item.id.slice(0, 4))}-…
						</span>
					</div>
					<textarea
						class="input"
						style="height: 60px; padding: 8px; resize: none; margin-top: 4px;"
						placeholder="Take this in a new direction…"
					></textarea>
				</div>
				<div class="chat-fork-actions">
					<button class="btn-ghost" onclick={onCancelFork}>Cancel</button>
					<button class="btn-primary" onclick={onCommitFork}>Fork conversation</button>
				</div>
			</div>
		{/if}
	</div>

	<Composer value={composeText} onChange={setComposeText} {onSend} {item} {sending} {surface} />
</div>
