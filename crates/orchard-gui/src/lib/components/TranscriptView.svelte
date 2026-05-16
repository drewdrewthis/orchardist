<!--
  Render a Claude Code conversation transcript from a `.jsonl` file on
  disk. The path comes from `Conversation.jsonlPath` on the daemon —
  the GUI doesn't touch the file system except via the Tauri bridge.

  Tail-loaded: the Rust reader returns the last ~512KB, so very long
  conversations don't stall the renderer. The view subscribes to
  `Subscription.conversationChanged(sessionUuid:)` on the daemon and
  re-loads when the fsnotify watcher fires. No polling.

  Pending turns (from SessionComposer's optimistic inserts) are rendered
  below real turns with iMessage-style status indicators:
    sending…  — mutation in flight
    sent ✓    — tmux ack received
    received ✓✓ — subscription fired + turns grew
    seen      — first assistant turn appeared after this message
    ·waiting  — 90s elapsed in sent/sending without advancing (opacity-dim)

  Browser dev preview shows a placeholder.
-->
<script lang="ts">
	import { untrack } from "svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import { createVirtualizer } from "@tanstack/svelte-virtual";
	import {
		readTranscript,
		parseTranscript,
		TRANSCRIPT_UNSUPPORTED,
		type TranscriptTurn,
		type TranscriptBlock,
	} from "$lib/data/transcript";
	import { subscribeConversation } from "$lib/data/daemon";
	import { renderMarkdown } from "$lib/util/markdown";
	import { getStore, type PendingTurn } from "$lib/store.svelte";
	import { playPing, pulseReplPill, fireWebNotification } from "$lib/notifications";

	type Props = {
		path: string;
		sessionUuid?: string;
		/** Session key used to look up (and mutate) pending turns in the store. */
		sessionKey?: string;
		/**
		 * Bindable: updated whenever `turns` changes so SessionPane can pass
		 * the current count to SessionComposer as `turnsLengthAtSend`.
		 */
		turnsLength?: number;
		/**
		 * Human-readable session title for Web Notification body heading.
		 * Falls back to sessionUuid prefix when absent.
		 */
		sessionTitle?: string;
	};
	let { path, sessionUuid, sessionKey, turnsLength = $bindable(0), sessionTitle }: Props = $props();

	const store = getStore();

	let turns = $state<TranscriptTurn[]>([]);
	let totalSize = $state(0);
	let truncated = $state(false);
	let status = $state<"loading" | "ok" | "empty" | "unsupported" | "error">("loading");
	let errMsg = $state<string | null>(null);
	let scrollHost: HTMLDivElement | undefined = $state();
	let stickToBottom = $state(true);
	let expandedTools = $state<Set<string>>(new Set());

	/** Pending turns from the store for this session. */
	const pendingTurns = $derived(
		sessionKey ? (store.pendingTurns[sessionKey] ?? []) : [],
	);

	// Virtualizer — @tanstack/svelte-virtual store. count is captured at
	// setOptions() call time (it's spread, not a getter), so we must call
	// setOptions whenever turns.length changes. Wrap in untrack() so the
	// effect ONLY tracks `turns.length` and not the virtualizer store
	// reads inside setOptions — otherwise we get effect_update_depth_exceeded.
	// estimateSize tuned for tool-heavy turns (~240 close to real avg).
	const virtualizer = createVirtualizer<HTMLDivElement, HTMLDivElement>({
		count: 0,
		getScrollElement: () => scrollHost ?? null,
		estimateSize: () => 240,
		overscan: 4,
	});
	$effect(() => {
		const count = turns.length;
		untrack(() => {
			$virtualizer.setOptions({
				count,
				getScrollElement: () => scrollHost ?? null,
				estimateSize: () => 240,
				overscan: 4,
			});
		});
	});

	function toggleTool(id: string) {
		const next = new Set(expandedTools);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		expandedTools = next;
	}

	async function load() {
		try {
			// Pass sessionUuid so the reader can hit the daemon's /v1/conversations/<uuid>/jsonl?lastN=
			// endpoint directly — skips the Tauri filesystem round-trip and works in the
			// browser dev preview through the Vite proxy.
			const chunk = await readTranscript(path, undefined, sessionUuid);
			const parsed = parseTranscript(chunk.text);
			turns = parsed;
			totalSize = chunk.size;
			truncated = chunk.truncated;
			status = parsed.length === 0 ? "empty" : "ok";
		} catch (err) {
			if ((err as Error)?.message === TRANSCRIPT_UNSUPPORTED) {
				status = "unsupported";
			} else {
				errMsg = (err as Error)?.message ?? String(err);
				status = "error";
			}
		}
	}

	$effect(() => {
		void path;
		void sessionUuid;
		status = "loading";
		turns = [];
		errMsg = null;
		stickToBottom = true;
		lastScrolledLen = 0;
		load();
	});

	// Keep turnsLength bindable prop in sync so SessionPane can thread it
	// into SessionComposer as turnsLengthAtSend.
	$effect(() => {
		turnsLength = turns.length;
	});

	/**
	 * Advance pending turn states when a subscription push arrives.
	 *
	 * Resolution rules (iMessage model):
	 *   1. Walk pending turns oldest-first.
	 *   2. For each "sent" turn where newTurns.length > turnsLengthAtSend:
	 *      flip to "received". Assign the earliest new user turn to oldest pending.
	 *   3. Once "received", look for the first assistant turn AFTER that send
	 *      time in the latest turns array → flip to "seen".
	 *
	 * The subscription fires after `turns` is updated by `load()`, so we
	 * always compare against the fresh array.
	 */
	function advancePendingStates(key: string, freshTurns: TranscriptTurn[]) {
		const pending = store.pendingTurns[key];
		if (!pending || pending.length === 0) return;

		// Count new turns since each send (multi-send within one tick: walk oldest first).
		for (const p of pending) {
			if (p.status === "sent" && freshTurns.length > p.turnsLengthAtSend) {
				store.patchPendingTurn(key, p.id, "received");
			}
		}

		// After patching to "received", look for first assistant turn after sentAt.
		const refreshed = store.pendingTurns[key] ?? [];
		for (const p of refreshed) {
			if (p.status !== "received") continue;
			// Find the first assistant turn whose timestamp >= sentAt.
			const assistantAfter = freshTurns.find(
				(t) => t.role === "assistant" && t.timestamp >= p.sentAt,
			);
			if (assistantAfter) {
				store.patchPendingTurn(key, p.id, "seen");
				// Fire "Claude responded" notifications — Flavor 1 + 2.
				_onReplySeen(assistantAfter);
				// Fade out "seen" bubbles after 2s — they've served their purpose.
				setTimeout(() => {
					store.removePendingTurn(key, p.id);
				}, 2000);
			}
		}
	}

	/**
	 * Fire the "Claude responded" ping when a pending turn transitions to "seen".
	 *
	 * Flavor 1: audio tick + REPL pill pulse (foreground, unless muted).
	 * Flavor 2: Web Notification (backgrounded tab, if chatNotify opted in).
	 */
	function _onReplySeen(assistantTurn: TranscriptTurn): void {
		if (!store.chatMute) {
			// Flavor 1a: audio tick.
			playPing();
			// Flavor 1b: pulse the REPL pill via a bubbling DOM event.
			pulseReplPill(scrollHost);
		}

		if (store.chatNotify) {
			// Flavor 2: Web Notification when tab is backgrounded.
			// Extract text from the first text block of the assistant turn.
			const textBlock = assistantTurn.blocks.find((b): b is { kind: "text"; text: string } => b.kind === "text");
			const body = textBlock ? textBlock.text : "";
			const notifTitle = sessionTitle ?? sessionUuid?.slice(0, 8) ?? "Claude";
			fireWebNotification({
				sessionUuid: sessionUuid ?? "unknown",
				title: notifTitle,
				body,
			});
		}
	}

	$effect(() => {
		// Subscribe to conversationChanged for this session — the daemon
		// fsnotify watcher already debounces fs events, so each push
		// corresponds to a real JSONL append. No polling.
		if (!sessionUuid) return;
		const key = sessionKey ?? sessionUuid;
		const unsub = subscribeConversation(
			sessionUuid,
			async () => {
				await load();
				// After load() updates `turns`, advance pending states.
				advancePendingStates(key, turns);
			},
			(err) => console.warn("[transcript] subscription error:", err),
		);
		return () => unsub();
	});

	/** 90s stall timer — fires for each pending turn in "sent" state. */
	$effect(() => {
		const key = sessionKey;
		if (!key) return;
		const pending = store.pendingTurns[key] ?? [];
		const sentTurns = pending.filter((p) => p.status === "sent" || p.status === "sending");
		if (sentTurns.length === 0) return;

		const timers: ReturnType<typeof setTimeout>[] = [];
		for (const p of sentTurns) {
			const elapsed = Date.now() - p.sentAt;
			const remaining = 90_000 - elapsed;
			if (remaining <= 0) {
				// Already past 90s — mark stalled now.
				store.patchPendingTurn(key, p.id, "stalled");
			} else {
				const t = setTimeout(() => {
					// Only stall if still in sent/sending (not yet received/seen).
					const current = (store.pendingTurns[key] ?? []).find((x) => x.id === p.id);
					if (current && (current.status === "sent" || current.status === "sending")) {
						store.patchPendingTurn(key, p.id, "stalled");
					}
				}, remaining);
				timers.push(t);
			}
		}
		return () => timers.forEach((t) => clearTimeout(t));
	});

	let lastScrolledLen = $state(0);
	$effect(() => {
		// Scroll to bottom when turn count grows OR pending turns appear/resolve.
		const len = turns.length + pendingTurns.length;
		if (!stickToBottom || len === 0 || len === lastScrolledLen) return;
		lastScrolledLen = len;
		setTimeout(() => {
			// intentional swallow: virtualizer may not be mounted yet during fast turn bursts; scroll retried on next tick
			try { $virtualizer.scrollToIndex(turns.length - 1, { align: "end" }); } catch {}
			// Also scroll the host directly to bottom to catch pending turns below the virtualizer.
			if (scrollHost) scrollHost.scrollTop = scrollHost.scrollHeight;
		}, 0);
	});

	function onScroll() {
		if (!scrollHost) return;
		const dist = scrollHost.scrollHeight - scrollHost.scrollTop - scrollHost.clientHeight;
		stickToBottom = dist < 40;
	}

	function timeStr(ts: number): string {
		if (!ts) return "";
		const d = new Date(ts);
		return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
	}

	/**
	 * Shorten Anthropic model strings to a recognizable tag:
	 *   claude-opus-4-7        → opus-4.7
	 *   claude-sonnet-4-6      → sonnet-4.6
	 *   claude-haiku-4-5-…     → haiku-4.5
	 * Anything else passes through.
	 */
	function modelLabel(m: string | null | undefined): string {
		if (!m) return "";
		const match = m.match(/^claude-(opus|sonnet|haiku)-(\d)-(\d)(?:-|$)/i);
		if (match) return `${match[1].toLowerCase()}-${match[2]}.${match[3]}`;
		return m;
	}

	/**
	 * Should the role header (role · model · time) be shown for this turn?
	 * Suppressed when this turn is from the same role as the previous one
	 * AND within a 5-minute window — gives the transcript a chat-grouped
	 * shape instead of a flat log.
	 */
	function showRoleHeader(idx: number): boolean {
		if (idx === 0) return true;
		const cur = turns[idx];
		const prev = turns[idx - 1];
		if (!cur || !prev) return true;
		if (cur.role !== prev.role) return true;
		if (cur.timestamp && prev.timestamp) {
			if (cur.timestamp - prev.timestamp > 5 * 60_000) return true;
		}
		return false;
	}

	function blockSummary(b: TranscriptBlock): string {
		if (b.kind === "text" || b.kind === "thinking") return "";
		if (b.kind === "tool_use") {
			const input = b.input as Record<string, unknown> | null;
			if (b.name === "Bash" && input && typeof input.command === "string") return input.command as string;
			if ((b.name === "Read" || b.name === "Edit" || b.name === "Write") && input && typeof input.file_path === "string") {
				return input.file_path as string;
			}
			if (input) return Object.keys(input).slice(0, 2).join(", ");
			return "";
		}
		if (b.kind === "tool_result") {
			const trimmed = b.text.trim();
			return trimmed.split("\n")[0]?.slice(0, 200) ?? "";
		}
		return "";
	}

	/** Human-readable indicator label for a pending turn status. */
	function pendingIndicator(p: PendingTurn): string {
		switch (p.status) {
			case "sending":  return "sending…";
			case "sent":     return "sent ✓";
			case "received": return "received ✓✓";
			case "seen":     return "seen";
			case "stalled":  return "·waiting";
		}
	}
</script>

<div class="flex-1 min-h-0 flex flex-col bg-surface">
	{#if status === "loading"}
		<div class="flex-1 flex flex-col items-center justify-center p-8 text-center">
			<span class="dimer">Loading transcript…</span>
		</div>
	{:else if status === "unsupported"}
		<div class="flex-1 flex flex-col items-center justify-center p-8 text-center">
			<div class="text-[13px] font-medium text-fg-2">
				Open in the Orchard desktop app to view this transcript.
			</div>
		</div>
	{:else if status === "error"}
		<div class="flex-1 flex flex-col items-center justify-center p-8 text-center">
			<div class="text-[13px] text-bad-fg">Transcript failed to load.</div>
			<div class="dimer mono text-[11.5px] mt-1">{errMsg}</div>
		</div>
	{:else if status === "empty" && pendingTurns.length === 0}
		<div class="flex-1 flex flex-col items-center justify-center p-8 text-center">
			<span class="dimer">No conversation turns parsed from {path}</span>
		</div>
	{:else}
		<div
			class="transcript-scroll flex-1 min-h-0 overflow-y-auto px-3.5 pt-3 pb-6"
			bind:this={scrollHost}
			onscroll={onScroll}
		>
			{#if truncated}
				<div class="mono text-center text-[11px] text-fg-3 pt-1 pb-2 border-b-[0.5px] border-dashed border-line">
					… earlier turns omitted ({(totalSize / 1024).toFixed(0)}KB total)
				</div>
			{/if}
			<div class="relative w-full" style="height: {$virtualizer.getTotalSize()}px;">
				{#each $virtualizer.getVirtualItems() as vRow (vRow.key)}
					{@const turn = turns[vRow.index]}
					{#if turn}
					<div
						class="turn absolute top-0 left-0 right-0"
						class:opacity-65={turn.toolFeedback}
						class:turn--user={turn.role === "user"}
						class:turn--assistant={turn.role === "assistant"}
						class:turn--grouped={!showRoleHeader(vRow.index)}
						data-role={turn.role}
						data-index={vRow.index}
						use:$virtualizer.measureElement
						style="transform: translateY({vRow.start}px);"
					>
						{#if showRoleHeader(vRow.index)}
							<div class="turn-header mono">
								<span class="turn-header__role">{turn.role}</span>
								{#if turn.model}
									<span class="dimest">·</span>
									<span class="dimer">{modelLabel(turn.model)}</span>
								{/if}
								{#if turn.timestamp}
									<span class="dimest">·</span>
									<span class="dimer">{timeStr(turn.timestamp)}</span>
								{/if}
							</div>
						{/if}
						{#each turn.blocks as block, i (i)}
							{#if block.kind === "text"}
								<div class="turn-bubble prose-chat">{@html renderMarkdown(block.text)}</div>
							{:else if block.kind === "thinking"}
								<details class="turn-thinking text-[12.5px]">
									<summary class="dimer mono cursor-pointer text-[11px] py-0.5">thinking</summary>
									<div class="text-[13px] leading-[1.55] break-words text-fg prose-chat pt-1">{@html renderMarkdown(block.text)}</div>
								</details>
							{:else if block.kind === "tool_use"}
								<div class="rounded-md bg-surface-2 border-[0.5px] border-line overflow-hidden min-w-0 w-full">
									<button
										class="mono flex items-center gap-1.5 w-full px-2 py-1 bg-transparent border-0 text-[11.5px] text-left cursor-pointer text-fg hover:bg-fg/[0.04]"
										onclick={() => toggleTool(block.toolId || `${turn.uuid}-tu-${i}`)}
									>
										<Icon name="terminal" size={11} />
										<span class="font-medium">{block.name}</span>
										<span class="dimer flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">{blockSummary(block)}</span>
										<span class="dimer text-[10px]">{expandedTools.has(block.toolId || `${turn.uuid}-tu-${i}`) ? "▾" : "▸"}</span>
									</button>
									{#if expandedTools.has(block.toolId || `${turn.uuid}-tu-${i}`)}
										<pre class="mono m-0 px-2.5 py-2 text-[11.5px] leading-[1.5] max-h-80 overflow-auto bg-surface border-t-[0.5px] border-line whitespace-pre-wrap break-words">{JSON.stringify(block.input, null, 2)}</pre>
									{/if}
								</div>
							{:else if block.kind === "tool_result"}
								<div
									class="rounded-md bg-surface-2 border-[0.5px] overflow-hidden min-w-0 w-full"
									class:border-line={!block.isError}
									class:border-[color-mix(in_oklab,var(--color-bad-fg),50%,var(--color-line))]={block.isError}
								>
									<button
										class="mono flex items-center gap-1.5 w-full px-2 py-1 bg-transparent border-0 text-[11.5px] text-left cursor-pointer text-fg hover:bg-fg/[0.04]"
										onclick={() => toggleTool(block.toolId || `${turn.uuid}-tr-${i}`)}
									>
										<Icon name={block.isError ? "alert" : "check"} size={11} />
										<span class="font-medium">{block.isError ? "tool error" : "tool result"}</span>
										<span class="dimer flex-1 min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">{blockSummary(block)}</span>
										<span class="dimer text-[10px]">{expandedTools.has(block.toolId || `${turn.uuid}-tr-${i}`) ? "▾" : "▸"}</span>
									</button>
									{#if expandedTools.has(block.toolId || `${turn.uuid}-tr-${i}`)}
										<pre class="mono m-0 px-2.5 py-2 text-[11.5px] leading-[1.5] max-h-80 overflow-auto bg-surface border-t-[0.5px] border-line whitespace-pre-wrap break-words">{block.text}</pre>
									{/if}
								</div>
							{/if}
						{/each}
					</div>
					{/if}
				{/each}
			</div>

			<!-- Pending turns rendered BELOW the real virtualizer list -->
			{#if pendingTurns.length > 0}
				<div class="pending-turns-section">
					{#each pendingTurns as p (p.id)}
						<div
							class="turn turn--user turn--pending"
							class:turn--stalled={p.status === "stalled"}
							data-pending-id={p.id}
							data-pending-status={p.status}
						>
							<div class="turn-header mono">
								<span class="turn-header__role">user</span>
							</div>
							<div class="turn-bubble prose-chat">{p.text}</div>
							<div class="pending-indicator mono" data-status={p.status}>
								{pendingIndicator(p)}
							</div>
						</div>
					{/each}
				</div>
			{/if}
		</div>
	{/if}
</div>


<style>
	/* Fluid scroll on mobile + desktop:
	   - -webkit-overflow-scrolling:touch enables iOS momentum scrolling
	   - overscroll-behavior:contain prevents the page-level rubber-band
	     from fighting the transcript scroll
	   - scroll-behavior:smooth makes programmatic scrolls (stick-to-bottom)
	     glide instead of teleport
	   - scrollbar-gutter:stable avoids width-shift on macOS Safari */
	.transcript-scroll {
		-webkit-overflow-scrolling: touch;
		overscroll-behavior: contain;
		scroll-behavior: smooth;
		scrollbar-gutter: stable;
	}

	/* Chat-shaped turn layout. User turns right-align with an accent
	   bubble; assistant turns left-align without a bubble (more
	   conversational). Consecutive same-role turns inside a 5min window
	   are grouped — only the first shows the role header, the rest sit
	   tighter beneath it.

	   min-width:0 on the flex column AND its children is critical:
	   tool-call blocks contain long single-line strings (Bash commands,
	   file paths) that otherwise push the column wider than the
	   transcript host. */
	.turn {
		display: flex;
		flex-direction: column;
		gap: 5px;
		padding-bottom: 10px;
		min-width: 0;
		max-width: 100%;
	}
	.turn > * {
		min-width: 0;
		max-width: 100%;
	}
	.turn--grouped {
		padding-top: 0;
		margin-top: -4px;
	}

	.turn-header {
		display: flex;
		align-items: center;
		gap: 5px;
		font-size: 10px;
		color: var(--color-fg-3, #6c707a);
		letter-spacing: 0.04em;
		padding: 6px 0 1px;
	}
	.turn-header__role {
		text-transform: lowercase;
		font-weight: 500;
		color: var(--color-fg-2, #aab0bb);
	}
	.turn--user .turn-header {
		justify-content: flex-end;
	}

	.turn-bubble {
		font-size: 14px;
		line-height: 1.5;
		word-break: break-word;
		color: var(--color-fg, #e4e6eb);
	}

	/* USER turns: right-aligned bubble with accent background. */
	.turn--user {
		align-items: flex-end;
	}
	.turn--user .turn-bubble {
		background: color-mix(in oklab, var(--color-accent, #6366f1) 14%, transparent);
		border: 0.5px solid color-mix(in oklab, var(--color-accent, #6366f1) 30%, transparent);
		border-radius: 14px 14px 4px 14px;
		padding: 8px 12px;
		max-width: 85%;
	}

	/* ASSISTANT turns: left-aligned, no bubble. */
	.turn--assistant {
		align-items: flex-start;
	}
	.turn--assistant .turn-bubble {
		max-width: 95%;
		padding: 2px 0;
	}

	/* System / tool-result-only turns: flat. */
	.turn:not(.turn--user):not(.turn--assistant) .turn-bubble {
		opacity: 0.85;
		font-size: 12.5px;
	}

	.turn-thinking {
		opacity: 0.75;
		padding-left: 6px;
		border-left: 1px dashed var(--color-line, rgba(255,255,255,0.08));
	}

	/* Pending turn section — floats below the virtualizer. */
	.pending-turns-section {
		padding-top: 4px;
	}

	/* Pending turn: same shape as a real user turn, but not positioned
	   absolute (it's in flow, below the virtualizer container). */
	.turn--pending {
		position: relative;
	}

	/* Stalled turns dim to signal they might not have landed. */
	.turn--stalled {
		opacity: 0.6;
	}

	/**
	 * iMessage-style indicator below the last pending bubble.
	 * Small grey text, right-aligned under the bubble.
	 */
	.pending-indicator {
		font-size: 10px;
		color: var(--color-fg-3, #6c707a);
		letter-spacing: 0.02em;
		padding-right: 2px;
		animation: indicator-in 150ms ease-out;
	}
	.pending-indicator[data-status="sending"] {
		color: var(--color-fg-3, #6c707a);
	}
	.pending-indicator[data-status="sent"] {
		color: var(--color-fg-2, #aab0bb);
	}
	.pending-indicator[data-status="received"] {
		color: #6fd391;
	}
	.pending-indicator[data-status="seen"] {
		color: #6fd391;
		animation: indicator-fade-out 1800ms ease-out forwards;
	}
	.pending-indicator[data-status="stalled"] {
		color: var(--color-fg-3, #6c707a);
		font-style: italic;
	}
	@keyframes indicator-in {
		from { opacity: 0; transform: translateY(2px); }
		to   { opacity: 1; transform: translateY(0); }
	}
	@keyframes indicator-fade-out {
		0%, 50% { opacity: 1; }
		100%    { opacity: 0; }
	}
</style>
