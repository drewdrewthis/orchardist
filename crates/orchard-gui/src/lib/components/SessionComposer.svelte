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
			autocorrect="on"
			spellcheck="true"
			enterkeyhint="send"
			class="flex-1 min-h-[36px] max-h-[200px] resize-none border-[0.5px] border-line bg-surface-2 text-fg rounded-lg px-2.5 py-2 text-[16px] leading-[1.4] outline-none focus:border-[color-mix(in_oklab,var(--color-accent)_60%,var(--color-line))]"
		></textarea>
		<button
			class="inline-flex items-center justify-center w-9 h-9 border-0 bg-fg text-bg rounded-lg cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
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
	<div class="mono dimer text-[10.5px]">
		Sent via tmux send-keys → {paneId} · Enter to send · Shift+Enter for newline
	</div>
</div>
