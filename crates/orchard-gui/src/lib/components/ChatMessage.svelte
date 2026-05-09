<!-- One chat message — agent or user, with hover actions and bubble styling. -->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import Avatar from "$lib/icons/Avatar.svelte";
	import SendStatus from "$lib/icons/SendStatus.svelte";
	import { shortTime } from "$lib/util/format";
	import type { Agent, Message } from "$lib/data/types";

	type Props = {
		msg: Message & { typing?: boolean };
		grouped: boolean;
		isChannel: boolean;
		agents: Agent[];
		idx: number;
		statusVariant?: "ticks" | "dots" | "minimal" | "text";
		onForkFrom: (idx: number, m: Message) => void;
		onReset: (idx: number, m: Message) => void;
	};
	let {
		msg,
		grouped,
		isChannel,
		agents,
		idx,
		statusVariant = "ticks",
		onForkFrom,
		onReset,
	}: Props = $props();

	let copied = $state(false);

	const isUser = $derived(msg.role === "user");
	const agent = $derived(
		isChannel && msg.agentId ? agents.find((a) => a.id === msg.agentId) || null : null,
	);
	/**
	 * Display name shown above each message:
	 *   - user → "Drew"
	 *   - mock-agent (matched in `agents`) → that agent's `name`
	 *   - real chat-core message → the raw `agentId`, which is the
	 *     sender handle (e.g. `@parent-tester`). chatCoreToGuiMessage
	 *     stuffs the handle into `agentId`; without this fallback the
	 *     UI defaulted to a generic "Agent" label, hiding the speaker.
	 */
	const displayName = $derived(
		isUser ? "Drew" : agent ? agent.name : msg.agentId || "Agent",
	);

	const showActions = $derived(!msg.typing);

	async function doCopy() {
		if (msg.text) {
			try {
				await navigator.clipboard.writeText(msg.text);
			} catch {}
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
				(m) =>
					`<span class="mono" style="color:var(--accent);font-weight:500;">${m}</span>`,
			)
			.replace(
				/(__contract__:)/g,
				'<span class="mono" style="color:var(--attn-fg);font-weight:600;">contract:</span>',
			)
			.replace(/\n/g, "<br/>");
	}

	const isContractMsg = $derived(msg.text && /__contract__:/.test(msg.text));
</script>

{#if !isContractMsg}
	<div
		class="chat-msg fadeIn"
		class:grouped
		class:is-user={isUser}
		class:is-agent={!isUser}
		class:is-question={msg.isQuestion}
		class:is-paused={msg.isPaused}
	>
		<div class="chat-msg-gutter">
			{#if !grouped}
				{#if agent}
					<span
						class="agent-avatar"
						style="background: oklch(0.62 0.13 {agent.hue}); width: 22px; height: 22px; font-size: 11px; border-radius: 5px; display: inline-flex; align-items: center; justify-content: center; color: white; font-weight: 600;"
						title="{agent.name} · {agent.role}"
					>
						{agent.avatar}
					</span>
				{:else}
					<Avatar kind={msg.role} size={22} />
				{/if}
			{/if}
		</div>
		<div class="chat-msg-body">
			{#if !grouped}
				<div class="chat-msg-meta">
					<span
						class="chat-msg-name"
						style:color={agent ? `oklch(0.78 0.13 ${agent.hue})` : undefined}
					>
						{displayName}
					</span>
					{#if agent}
						<span class="dimest mono" style:font-size="10.5px">{agent.model}</span>
					{/if}
					<span class="dimest mono" style:font-size="10.5px">{shortTime(msg.ts)}</span>
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
			<div class="chat-msg-bubble">
				{#if msg.typing}
					<span class="typing-dots"><i></i><i></i><i></i></span>
				{:else}
					{@html linkify(msg.text)}
				{/if}
				{#if msg.tools && msg.tools.length > 0}
					<div class="chat-msg-tools">
						{#each msg.tools as t}
							<span class="chip ghost" style="height: 18px; font-size: 10.5px; padding: 0 6px;">
								<Icon name="bolt" size={9} /><span class="mono">{t}</span>
							</span>
						{/each}
					</div>
				{/if}
				{#if msg.diff}
					<div class="chat-msg-diff mono">
						<span style="color: var(--ok-fg);">+{msg.diff.plus}</span>
						<span style="color: var(--bad-fg);">−{msg.diff.minus}</span>
						<span class="dimer">across {msg.diff.files} files</span>
						<button
							class="btn-ghost"
							style="height: 18px; padding: 0 6px; font-size: 11px; margin-left: auto;"
						>
							view
						</button>
					</div>
				{/if}
			</div>
			{#if isUser}
				<div class="chat-msg-status">
					<SendStatus status={msg.status} variant={statusVariant} />
				</div>
			{/if}
			{#if showActions}
				<div class="chat-msg-actions" role="group" aria-label="Message actions">
					<button class="chat-msg-action" onclick={doCopy} title={copied ? "Copied" : "Copy"}>
						<Icon name={copied ? "check" : "copy"} size={11} />
					</button>
					<button
						class="chat-msg-action"
						onclick={() => onForkFrom(idx, msg)}
						title="Fork from here"
					>
						<Icon name="git-fork" size={11} />
					</button>
					<button
						class="chat-msg-action chat-msg-action-danger"
						onclick={() => onReset(idx, msg)}
						title="Reset from here"
					>
						<Icon name="refresh" size={11} />
					</button>
				</div>
			{/if}
		</div>
	</div>
{/if}

<style>
	.typing-dots {
		display: inline-flex;
		gap: 3px;
		align-items: center;
		height: 18px;
	}
	.typing-dots i {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--fg-3);
		animation: typing 1.2s ease-in-out infinite;
	}
	.typing-dots i:nth-child(2) {
		animation-delay: 0.15s;
	}
	.typing-dots i:nth-child(3) {
		animation-delay: 0.3s;
	}
	@keyframes typing {
		0%,
		80%,
		100% {
			opacity: 0.3;
			transform: translateY(0);
		}
		40% {
			opacity: 1;
			transform: translateY(-2px);
		}
	}
</style>
