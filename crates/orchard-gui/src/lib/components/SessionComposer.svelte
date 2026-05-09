<!--
  Composer for a Claude REPL running inside a tmux pane. Sending types
  the message into the pane via `tmux send-keys -t <paneId> -l <text>`
  followed by `Enter` (Tauri command `tmux_send_text` in commands.rs).

  No optimistic UI — the transcript view picks the new turn up on its
  next 4s poll. Keeping it that way avoids divergence between what we
  show and what the JSONL says actually landed.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import { tmuxSendText } from "$lib/tauri";

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

	async function send() {
		const t = text.trim();
		if (!t || sending) return;
		sending = true;
		error = null;
		try {
			await tmuxSendText(paneId, t);
			text = "";
			autosize();
		} catch (err) {
			error = (err as Error)?.message ?? String(err);
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

<div class="composer">
	{#if error}
		<div class="composer-error mono">
			<Icon name="alert" size={11} />
			<span>{error}</span>
			<button class="composer-error-close" onclick={() => (error = null)} aria-label="Dismiss">×</button>
		</div>
	{/if}
	<div class="composer-row">
		<textarea
			bind:this={textarea}
			bind:value={text}
			oninput={autosize}
			onkeydown={onKeydown}
			placeholder={sessionLabel ? `Message ${sessionLabel}…` : "Message session…"}
			rows="1"
			disabled={sending}
		></textarea>
		<button
			class="composer-send"
			onclick={send}
			disabled={sending || !text.trim()}
			title="Send (Enter) · Shift+Enter for newline"
		>
			{#if sending}
				…
			{:else}
				<Icon name="send" size={13} />
			{/if}
		</button>
	</div>
	<div class="composer-hint mono dimer">
		Sent via tmux send-keys → {paneId} · Enter to send · Shift+Enter for newline
	</div>
</div>

<style>
	.composer {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 10px 12px 10px 12px;
		border-top: 0.5px solid var(--line);
		background: var(--surface-1);
	}
	.composer-error {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 5px 8px;
		font-size: 11.5px;
		color: var(--bad-fg, #f06);
		background: color-mix(in oklab, var(--bad-fg, #f06) 10%, transparent);
		border: 0.5px solid color-mix(in oklab, var(--bad-fg, #f06) 30%, var(--line));
		border-radius: 6px;
	}
	.composer-error-close {
		margin-left: auto;
		border: 0;
		background: transparent;
		color: inherit;
		cursor: pointer;
		font-size: 13px;
	}
	.composer-row {
		display: flex;
		align-items: flex-end;
		gap: 8px;
	}
	textarea {
		flex: 1;
		min-height: 36px;
		max-height: 200px;
		resize: none;
		border: 0.5px solid var(--line);
		background: var(--surface-2);
		color: var(--fg);
		border-radius: 8px;
		padding: 8px 10px;
		font: inherit;
		font-size: 13px;
		line-height: 1.45;
	}
	textarea:focus {
		outline: none;
		border-color: color-mix(in oklab, var(--accent, #6cf) 60%, var(--line));
	}
	.composer-send {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 36px;
		height: 36px;
		border: 0;
		background: var(--fg);
		color: var(--bg);
		border-radius: 8px;
		cursor: pointer;
	}
	.composer-send:disabled {
		opacity: 0.4;
		cursor: not-allowed;
	}
	.composer-hint {
		font-size: 10.5px;
	}
</style>
