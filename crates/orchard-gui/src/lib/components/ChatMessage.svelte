<!-- One chat message — agent or user, with hover actions and bubble styling. -->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import Avatar from "$lib/icons/Avatar.svelte";
	import SendStatus from "$lib/icons/SendStatus.svelte";
	import { shortTime } from "$lib/util/format";
	import type { Message } from "$lib/data/types";

	type Props = {
		msg: Message & { typing?: boolean };
		grouped: boolean;
		isChannel: boolean;
		idx: number;
		statusVariant?: "ticks" | "dots" | "minimal" | "text";
		onForkFrom: (idx: number, m: Message) => void;
		onReset: (idx: number, m: Message) => void;
	};
	let {
		msg,
		grouped,
		isChannel,
		idx,
		statusVariant = "ticks",
		onForkFrom,
		onReset,
	}: Props = $props();

	let copied = $state(false);

	const isUser = $derived(msg.role === "user");
	/**
	 * Display name shown above each message:
	 *   - user → "Drew"
	 *   - agent message → the raw `agentId` (sender handle e.g. `@parent-tester`).
	 *
	 * We no longer carry a hand-rolled Agent registry; the chat-core
	 * message itself is the single source of truth.
	 */
	const displayName = $derived(isUser ? "Drew" : msg.agentId || "Agent");

	const showActions = $derived(!msg.typing);

	async function doCopy() {
		if (msg.text) {
			try {
				await navigator.clipboard.writeText(msg.text);
			} catch {
				// intentional swallow: clipboard write denied (no user gesture or permission blocked); copy action silently no-ops
			}
		}
		copied = true;
		setTimeout(() => (copied = false), 1100);
	}

	function linkify(s: string): string {
		const esc = s.replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" })[c] || c);
		return esc
			.replace(/(`[^`]+`)/g, (m) => `<code class="mono inline-code">${m.slice(1, -1)}</code>`)
			.replace(
				/(#\d+)/g,
				// Dynamic-accent pattern (ADR-020): inline style consumes var(--accent), which is bridged
				// from @theme inline {} and tracks --accent-hue runtime mutation.
				(m) => `<span class="mono" style="color:var(--accent);font-weight:500;">${m}</span>`,
			)
			.replace(/\n/g, "<br/>");
	}
</script>

<div
	class="fadeIn grid grid-cols-[30px_1fr] gap-2.5 px-3 relative group"
	class:py-1={!grouped}
	class:my-1.5={!grouped}
	class:py-0={grouped}
	class:m-0={grouped}
>
	<div>
		{#if !grouped}
			<Avatar kind={msg.role} size={22} />
		{/if}
	</div>
	<div>
		{#if !grouped}
			<div class="flex items-baseline gap-2 mb-1">
				<span class="font-semibold text-[13px] tracking-tight">{displayName}</span>
				<span class="dimest mono text-[10.5px]">{shortTime(msg.ts)}</span>
				{#if msg.isQuestion}
					<span class="chip attn" style="height: 16px; font-size: 10px; padding: 0 6px;">
						<Icon name="question" size={9} /> open question
					</span>
				{/if}
				{#if msg.isPaused}
					<span class="chip" style="height: 16px; font-size: 10px; padding: 0 6px;">
						<Icon name="clock" size={9} /> paused
					</span>
				{/if}
			</div>
		{/if}
		<div
			class="text-[13.5px] leading-[1.5] tracking-[-0.005em]"
			class:text-fg={isUser && !msg.isQuestion && !msg.isPaused}
			class:text-fg-2={!isUser && !msg.isQuestion && !msg.isPaused}
			class:bg-attn-soft={msg.isQuestion}
			class:border-l-2={msg.isQuestion}
			class:border-attn={msg.isQuestion}
			class:px-3.5={msg.isQuestion}
			class:py-2.5={msg.isQuestion}
			class:rounded-r-lg={msg.isQuestion}
			class:text-attn-fg={msg.isQuestion}
			class:italic={msg.isPaused}
			class:text-fg-3={msg.isPaused}
		>
			{#if msg.typing}
				<span class="inline-flex gap-[3px] items-center h-[18px]">
					<i class="w-[5px] h-[5px] rounded-full bg-fg-3 animate-typing"></i>
					<i class="w-[5px] h-[5px] rounded-full bg-fg-3 animate-typing [animation-delay:0.15s]"></i>
					<i class="w-[5px] h-[5px] rounded-full bg-fg-3 animate-typing [animation-delay:0.3s]"></i>
				</span>
			{:else}
				{@html linkify(msg.text)}
			{/if}
			{#if msg.tools && msg.tools.length > 0}
				<div class="inline-flex gap-1 flex-wrap mt-1.5">
					{#each msg.tools as t}
						<span class="chip ghost" style="height: 18px; font-size: 10.5px; padding: 0 6px;">
							<Icon name="bolt" size={9} /><span class="mono">{t}</span>
						</span>
					{/each}
				</div>
			{/if}
			{#if msg.diff}
				<div class="mono inline-flex items-center gap-2 mt-2 px-2.5 py-1.5 bg-surface-2 border-[0.5px] border-line rounded-[7px] text-[11.5px]">
					<span class="text-ok-fg">+{msg.diff.plus}</span>
					<span class="text-bad-fg">−{msg.diff.minus}</span>
					<span class="dimer">across {msg.diff.files} files</span>
				</div>
			{/if}
		</div>
		{#if isUser}
			<div class="inline-flex items-center mt-1">
				<SendStatus status={msg.status} variant={statusVariant} />
			</div>
		{/if}
		{#if showActions}
			<div
				class="absolute top-1 right-2 inline-flex gap-0.5 p-0.5 bg-surface border-[0.5px] border-line rounded-[7px] shadow-[0_2px_8px_rgba(0,0,0,0.06)] opacity-0 -translate-y-0.5 transition duration-100 pointer-events-none group-hover:opacity-100 group-hover:translate-y-0 group-hover:pointer-events-auto group-focus-within:opacity-100 group-focus-within:translate-y-0 group-focus-within:pointer-events-auto"
				role="group"
				aria-label="Message actions"
			>
				<button
					class="w-[22px] h-[22px] rounded-[5px] inline-flex items-center justify-center bg-transparent border-0 text-fg-3 transition duration-100 hover:bg-fg/[0.07] hover:text-fg"
					onclick={doCopy}
					title={copied ? "Copied" : "Copy"}
				>
					<Icon name={copied ? "check" : "copy"} size={11} />
				</button>
				<button
					class="w-[22px] h-[22px] rounded-[5px] inline-flex items-center justify-center bg-transparent border-0 text-fg-3 transition duration-100 hover:bg-fg/[0.07] hover:text-fg"
					onclick={() => onForkFrom(idx, msg)}
					title="Fork from here"
				>
					<Icon name="git-fork" size={11} />
				</button>
				<button
					class="w-[22px] h-[22px] rounded-[5px] inline-flex items-center justify-center bg-transparent border-0 text-fg-3 transition hover:bg-fg/[0.07] hover:text-bad-fg"
					onclick={() => onReset(idx, msg)}
					title="Reset from here"
				>
					<Icon name="refresh" size={11} />
				</button>
			</div>
		{/if}
	</div>
</div>
