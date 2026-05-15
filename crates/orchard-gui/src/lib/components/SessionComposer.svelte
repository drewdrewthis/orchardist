<!--
  Composer for a Claude REPL running inside a tmux pane. Sending types
  the message into the pane via `tmux send-keys -t <paneId> -l <text>`
  followed by `Enter` (Tauri command `tmux_send_text` in commands.rs).

  No optimistic UI — the transcript view subscribes to
  `Subscription.conversationChanged(sessionUuid:)` and re-loads the
  JSONL when the daemon's fsnotify watcher fires. The new turn shows
  up the moment Claude writes it, no client-side polling.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import { tmuxSendText } from "$lib/tauri";
	import { toast } from "$lib/util/toast";

	type Props = {
		paneId: string;
		/** Display label used in error messages (e.g. tmux session name). */
		sessionLabel?: string;
	};
	let { paneId, sessionLabel }: Props = $props();

	let text = $state("");
	let sending = $state(false);
	let error = $state<string | null>(null);
	let textarea: HTMLTextAreaElement | undefined = $state();
	// Lightweight clock for the "sent" badge fade. 500ms tick is plenty —
	// the badge only flips once after 2.5s.
	let now = $state(Date.now());
	/** "sent" badge state — visible for 2.5s after a successful send. */
	let lastSentAt = $state<number | null>(null);
	const sentBadgeVisible = $derived(
		lastSentAt != null && now - lastSentAt < 2500,
	);
	$effect(() => {
		if (!sentBadgeVisible) return;
		const t = setInterval(() => (now = Date.now()), 500);
		return () => clearInterval(t);
	});

	async function send() {
		const t = text.trim();
		if (!t || sending) return;
		sending = true;
		error = null;
		try {
			await tmuxSendText(paneId, t);
			text = "";
			autosize();
			lastSentAt = Date.now();
			now = Date.now();
		} catch (err) {
			error = (err as Error)?.message ?? String(err);
			toast.error(err);
		} finally {
			sending = false;
			queueMicrotask(() => textarea?.focus());
		}
	}

	function onKeydown(e: KeyboardEvent) {
		// Enter (without shift/cmd) sends. Shift+Enter inserts a newline.
		// Cmd/Ctrl+Enter also sends, matching ChatView's existing shortcut.
		if (e.key === "Enter" && !e.shiftKey) {
			e.preventDefault();
			send();
		}
	}

	function autosize() {
		if (!textarea) return;
		textarea.style.height = "auto";
		const max = 200;
		textarea.style.height = Math.min(textarea.scrollHeight, max) + "px";
	}
</script>

<div class="flex flex-col gap-1 px-3 py-2.5 border-t-[0.5px] border-line bg-surface">
	{#if error}
		<div class="mono flex items-center gap-1.5 px-2 py-1 text-[11.5px] text-bad-fg rounded-md bg-[color-mix(in_oklab,var(--color-bad-fg)_10%,transparent)] border-[0.5px] border-[color-mix(in_oklab,var(--color-bad-fg)_30%,var(--color-line))]">
			<Icon name="alert" size={11} />
			<span>{error}</span>
			<button
				class="ml-auto border-0 bg-transparent text-inherit cursor-pointer text-[13px]"
				onclick={() => (error = null)}
				aria-label="Dismiss"
			>×</button>
		</div>
	{/if}
	<div class="flex items-end gap-2">
		<textarea
			bind:this={textarea}
			bind:value={text}
			oninput={autosize}
			onkeydown={onKeydown}
			placeholder={sessionLabel ? `Message ${sessionLabel}…` : "Message session…"}
			rows="1"
			disabled={sending}
			autocapitalize="sentences"
			autocomplete="off"
			spellcheck="true"
			enterkeyhint="send"
			{...{ autocorrect: "on" }}
			class="flex-1 min-h-[36px] max-h-[200px] resize-none border-[0.5px] border-line bg-surface-2 text-fg rounded-lg px-2.5 py-2 text-[16px] leading-[1.4] outline-none focus:border-[color-mix(in_oklab,var(--color-accent)_60%,var(--color-line))]"
		></textarea>
		<button
			class="send-btn"
			class:sending
			onclick={send}
			disabled={sending || !text.trim()}
			title="Send (Enter) · Shift+Enter for newline"
			aria-label="Send"
		>
			{#if sending}
				<span class="send-spinner" aria-hidden="true"></span>
				<span class="sr-only">Sending…</span>
			{:else}
				<Icon name="send" size={14} />
			{/if}
		</button>
	</div>
	<div class="composer-status mono">
		{#if sending}
			<span class="status-pill status-pill--sending">
				<span class="send-spinner" aria-hidden="true"></span>
				sending to {paneId}…
			</span>
		{:else if sentBadgeVisible}
			<span class="status-pill status-pill--sent">
				<Icon name="check" size={9} /> delivered to {paneId}
			</span>
		{:else}
			<span class="dimer">↵ send · ⇧↵ newline · {paneId}</span>
		{/if}
	</div>
</div>

<style>
	.send-btn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 38px;
		height: 38px;
		border: none;
		border-radius: 10px;
		background: var(--color-accent, #6366f1);
		color: white;
		cursor: pointer;
		transition: background 100ms ease, transform 80ms ease;
		flex: none;
	}
	.send-btn:hover:not(:disabled) {
		background: color-mix(in oklab, var(--color-accent, #6366f1) 90%, white);
	}
	.send-btn:active:not(:disabled) {
		transform: scale(0.95);
	}
	.send-btn:disabled {
		background: color-mix(in oklab, var(--color-fg, #888) 15%, transparent);
		color: color-mix(in oklab, var(--color-fg, #888) 40%, transparent);
		cursor: not-allowed;
	}
	.send-btn.sending {
		background: color-mix(in oklab, var(--color-accent, #6366f1) 60%, transparent);
	}
	.send-spinner {
		display: inline-block;
		width: 12px;
		height: 12px;
		border: 1.5px solid currentColor;
		border-right-color: transparent;
		border-radius: 50%;
		animation: send-spin 700ms linear infinite;
	}
	@keyframes send-spin {
		to { transform: rotate(360deg); }
	}
	.sr-only {
		position: absolute;
		width: 1px;
		height: 1px;
		padding: 0;
		margin: -1px;
		overflow: hidden;
		clip: rect(0, 0, 0, 0);
		white-space: nowrap;
		border: 0;
	}
	.composer-status {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 10.5px;
		min-height: 14px;
		padding: 0 2px;
	}
	.status-pill {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 1px 6px;
		border-radius: 8px;
		font-size: 10px;
		animation: status-fade 200ms ease-out;
	}
	.status-pill--sending {
		color: var(--color-accent, #6366f1);
		background: color-mix(in oklab, var(--color-accent, #6366f1) 12%, transparent);
	}
	.status-pill--sent {
		color: #6fd391;
		background: color-mix(in oklab, #6fd391 12%, transparent);
		animation: status-fade-out 2500ms ease-out;
	}
	@keyframes status-fade {
		from { opacity: 0; transform: translateY(2px); }
		to { opacity: 1; transform: translateY(0); }
	}
	@keyframes status-fade-out {
		0%, 70% { opacity: 1; }
		100% { opacity: 0.3; }
	}
</style>
