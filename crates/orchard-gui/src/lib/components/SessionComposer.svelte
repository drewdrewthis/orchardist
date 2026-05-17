<!--
  Composer for a Claude REPL running inside a tmux pane. Sending types
  the message into the pane via `tmux send-keys -t <paneId> -l <text>`
  followed by `Enter` (Tauri command `tmux_send_text` in commands.rs).

  Optimistic UI: the input clears INSTANTLY on Enter and a pending bubble
  appears in the transcript view. State machine tracks Sent → Received →
  Seen (iMessage style). The 2.5s "delivered" badge has been removed —
  replaced by per-bubble indicators that are actually honest.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import { tmuxSendText } from "$lib/tauri";
	import { toast } from "$lib/util/toast";
	import { getStore, type PendingTurn } from "$lib/store.svelte";

	type Props = {
		paneId: string;
		/** Display label used in error messages (e.g. tmux session name). */
		sessionLabel?: string;
		/**
		 * Key used to bucket pending turns in the store. Use sessionUuid when
		 * available; fall back to paneId so the composer can always write.
		 */
		sessionKey: string;
		/** Current transcript turns.length — captured at send time. */
		turnsLength: number;
	};
	let { paneId, sessionLabel, sessionKey, turnsLength }: Props = $props();

	const store = getStore();

	let text = $state("");
	let error = $state<string | null>(null);
	let textarea: HTMLTextAreaElement | undefined = $state();

	async function send() {
		const t = text.trim();
		if (!t) return;

		// --- INSTANT clear: input empties before any async work ---
		text = "";
		autosize();
		queueMicrotask(() => textarea?.focus());

		// Optimistic insert
		const id = "pending-" + Date.now() + "-" + Math.random().toString(36).slice(2, 7);
		const pending: PendingTurn = {
			id,
			text: t,
			sentAt: Date.now(),
			turnsLengthAtSend: turnsLength,
			status: "sending",
		};
		store.addPendingTurn(sessionKey, pending);

		// Fire the mutation
		try {
			await tmuxSendText(paneId, t);
			store.patchPendingTurn(sessionKey, id, "sent");
		} catch (err) {
			error = (err as Error)?.message ?? String(err);
			toast.error(err);
			// On mutation failure, remove the optimistic bubble — message was never sent.
			store.removePendingTurn(sessionKey, id);
		}
	}

	function onKeydown(e: KeyboardEvent) {
		// Enter (without shift/cmd) sends. Shift+Enter inserts a newline.
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

<div class="composer-wrap flex flex-col gap-1 px-3 pt-2.5 border-t-[0.5px] border-line bg-surface">
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
			autocapitalize="sentences"
			autocomplete="off"
			spellcheck="true"
			enterkeyhint="send"
			{...{ autocorrect: "on" }}
			onfocus={() => {
				// iOS Safari with position:fixed shell won't auto-scroll the
				// textarea above the keyboard. Manually nudge it into view on
				// focus — small timeout lets the keyboard animation start so
				// scrollIntoView lands on the post-keyboard viewport.
				setTimeout(() => textarea?.scrollIntoView({ block: "end", behavior: "smooth" }), 100);
			}}
			class="flex-1 min-h-[36px] max-h-[200px] resize-none border-[0.5px] border-line bg-surface-2 text-fg rounded-lg px-2.5 py-2 text-[16px] leading-[1.4] outline-none focus:border-[color-mix(in_oklab,var(--color-accent)_60%,var(--color-line))]"
		></textarea>
		<button
			class="send-btn"
			onclick={send}
			disabled={!text.trim()}
			title="Send (Enter) · Shift+Enter for newline"
			aria-label="Send"
		>
			<Icon name="send" size={14} />
		</button>
	</div>
	<div class="composer-status mono">
		<span class="dimer">↵ send · ⇧↵ newline · {paneId}</span>
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
	.composer-status {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 10.5px;
		min-height: 14px;
		padding: 0 2px;
	}
	/* iOS safe-area bottom — owned by the composer, not the outer shell.
	   max(10px, env(...)) gives a consistent breathing room on non-notched
	   screens while honoring the home-indicator on iPhone X+. Without this,
	   the textarea was being clipped past the bottom of the viewport. */
	.composer-wrap {
		padding-bottom: max(10px, env(safe-area-inset-bottom, 0));
	}
</style>
