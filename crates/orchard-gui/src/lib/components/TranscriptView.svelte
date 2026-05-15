<!--
  Render a Claude Code conversation transcript from a `.jsonl` file on
  disk. The path comes from `Conversation.jsonlPath` on the daemon —
  the GUI doesn't touch the file system except via the Tauri bridge.

  Tail-loaded: the Rust reader returns the last ~512KB, so very long
  conversations don't stall the renderer. The view subscribes to
  `Subscription.conversationChanged(sessionUuid:)` on the daemon and
  re-loads when the fsnotify watcher fires. No polling.

  Browser dev preview shows a placeholder.
-->
<script lang="ts">
	import { onMount, untrack } from "svelte";
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

	type Props = {
		path: string;
		sessionUuid?: string;
	};
	let { path, sessionUuid }: Props = $props();

	let turns = $state<TranscriptTurn[]>([]);
	let totalSize = $state(0);
	let truncated = $state(false);
	let status = $state<"loading" | "ok" | "empty" | "unsupported" | "error">("loading");
	let errMsg = $state<string | null>(null);
	let scrollHost: HTMLDivElement | undefined = $state();
	let stickToBottom = $state(true);
	let expandedTools = $state<Set<string>>(new Set());

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

	$effect(() => {
		// Subscribe to conversationChanged for this session — the daemon
		// fsnotify watcher already debounces fs events, so each push
		// corresponds to a real JSONL append. No polling.
		if (!sessionUuid) return;
		const unsub = subscribeConversation(
			sessionUuid,
			() => load(),
			(err) => console.warn("[transcript] subscription error:", err),
		);
		return () => unsub();
	});

	let lastScrolledLen = $state(0);
	$effect(() => {
		// Scroll to bottom only when turn count actually grows AND the user
		// is anchored. Reading turns.length is fine; we DO NOT call into the
		// virtualizer store inside this effect — scrollToIndex is fired off
		// the reactive frame via setTimeout to avoid re-triggering measure
		// pass writes during the same flush.
		const len = turns.length;
		if (!stickToBottom || len === 0 || len === lastScrolledLen) return;
		lastScrolledLen = len;
		setTimeout(() => {
			// intentional swallow: virtualizer may not be mounted yet during fast turn bursts; scroll retried on next tick
			try { $virtualizer.scrollToIndex(len - 1, { align: "end" }); } catch {}
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
	{:else if status === "empty"}
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
</style>
