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
	import { onMount } from "svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import {
		readTranscript,
		parseTranscript,
		TRANSCRIPT_UNSUPPORTED,
		type TranscriptTurn,
		type TranscriptBlock,
	} from "$lib/data/transcript";
	import { subscribeConversation } from "$lib/data/daemon";

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

	function toggleTool(id: string) {
		const next = new Set(expandedTools);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		expandedTools = next;
	}

	async function load() {
		try {
			const chunk = await readTranscript(path);
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

	$effect(() => {
		// Scroll to bottom on every new render when the user is anchored
		// there. We track stickToBottom from scroll events below.
		void turns.length;
		if (stickToBottom && scrollHost) {
			queueMicrotask(() => {
				if (scrollHost) scrollHost.scrollTop = scrollHost.scrollHeight;
			});
		}
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

<div class="transcript">
	{#if status === "loading"}
		<div class="transcript-empty"><span class="dimer">Loading transcript…</span></div>
	{:else if status === "unsupported"}
		<div class="transcript-empty">
			<div style="font-size: 13px; font-weight: 500; color: var(--fg-2);">
				Open in the Orchard desktop app to view this transcript.
			</div>
		</div>
	{:else if status === "error"}
		<div class="transcript-empty">
			<div style="font-size: 13px; color: var(--bad-fg);">Transcript failed to load.</div>
			<div class="dimer mono" style="font-size: 11.5px; margin-top: 4px;">{errMsg}</div>
		</div>
	{:else if status === "empty"}
		<div class="transcript-empty">
			<span class="dimer">No conversation turns parsed from {path}</span>
		</div>
	{:else}
		<div
			class="transcript-scroll"
			bind:this={scrollHost}
			onscroll={onScroll}
		>
			{#if truncated}
				<div class="transcript-trunc mono">
					… earlier turns omitted ({(totalSize / 1024).toFixed(0)}KB total)
				</div>
			{/if}
			{#each turns as turn (turn.uuid)}
				<div class="t-turn" data-role={turn.role} class:tool-feedback={turn.toolFeedback}>
					<div class="t-meta mono">
						<span class="t-role">{turn.role}</span>
						{#if turn.model}
							<span class="dimest">·</span>
							<span class="dimer">{turn.model}</span>
						{/if}
						{#if turn.timestamp}
							<span class="dimest">·</span>
							<span class="dimer">{timeStr(turn.timestamp)}</span>
						{/if}
					</div>
					{#each turn.blocks as block, i (i)}
						{#if block.kind === "text"}
							<div class="t-text">{block.text}</div>
						{:else if block.kind === "thinking"}
							<details class="t-thinking">
								<summary class="dimer mono">thinking</summary>
								<div class="t-text">{block.text}</div>
							</details>
						{:else if block.kind === "tool_use"}
							<div class="t-tool">
								<button
									class="t-tool-head mono"
									onclick={() => toggleTool(block.toolId || `${turn.uuid}-tu-${i}`)}
								>
									<Icon name="terminal" size={11} />
									<span class="t-tool-name">{block.name}</span>
									<span class="t-tool-summary dimer">{blockSummary(block)}</span>
									<span class="t-tool-chev dimer">{expandedTools.has(block.toolId || `${turn.uuid}-tu-${i}`) ? "▾" : "▸"}</span>
								</button>
								{#if expandedTools.has(block.toolId || `${turn.uuid}-tu-${i}`)}
									<pre class="t-tool-body mono">{JSON.stringify(block.input, null, 2)}</pre>
								{/if}
							</div>
						{:else if block.kind === "tool_result"}
							<div class="t-tool" class:err={block.isError}>
								<button
									class="t-tool-head mono"
									onclick={() => toggleTool(block.toolId || `${turn.uuid}-tr-${i}`)}
								>
									<Icon name={block.isError ? "alert" : "check"} size={11} />
									<span class="t-tool-name">{block.isError ? "tool error" : "tool result"}</span>
									<span class="t-tool-summary dimer">{blockSummary(block)}</span>
									<span class="t-tool-chev dimer">{expandedTools.has(block.toolId || `${turn.uuid}-tr-${i}`) ? "▾" : "▸"}</span>
								</button>
								{#if expandedTools.has(block.toolId || `${turn.uuid}-tr-${i}`)}
									<pre class="t-tool-body mono">{block.text}</pre>
								{/if}
							</div>
						{/if}
					{/each}
				</div>
			{/each}
		</div>
	{/if}
</div>

<style>
	.transcript {
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
		background: var(--surface-1);
	}
	.transcript-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding: 12px 14px 24px 14px;
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	.transcript-empty {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		padding: 32px;
		text-align: center;
	}
	.transcript-trunc {
		text-align: center;
		font-size: 11px;
		color: var(--fg-3);
		padding: 4px 0 8px 0;
		border-bottom: 0.5px dashed var(--line);
	}
	.t-turn {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	.t-turn[data-role="user"] {
		padding-left: 6px;
		border-left: 2px solid color-mix(in oklab, var(--accent, #6cf) 60%, transparent);
	}
	.t-turn[data-role="assistant"] {
		padding-left: 6px;
		border-left: 2px solid color-mix(in oklab, var(--ok-fg, #6fd391) 35%, transparent);
	}
	.t-turn.tool-feedback {
		opacity: 0.65;
	}
	.t-meta {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 11px;
		color: var(--fg-3);
	}
	.t-role {
		text-transform: lowercase;
		font-weight: 500;
		color: var(--fg-2);
	}
	.t-text {
		font-size: 13px;
		line-height: 1.55;
		white-space: pre-wrap;
		word-break: break-word;
		color: var(--fg);
	}
	.t-thinking {
		font-size: 12.5px;
	}
	.t-thinking summary {
		cursor: pointer;
		font-size: 11px;
		padding: 2px 0;
	}
	.t-thinking[open] {
		padding-left: 8px;
		border-left: 1px dashed var(--line);
	}
	.t-tool {
		border-radius: 6px;
		background: var(--surface-2);
		border: 0.5px solid var(--line);
		overflow: hidden;
	}
	.t-tool.err {
		border-color: color-mix(in oklab, var(--bad-fg, #f06), 50%, var(--line));
	}
	.t-tool-head {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		padding: 4px 8px;
		background: transparent;
		border: 0;
		font-size: 11.5px;
		text-align: left;
		cursor: pointer;
		color: var(--fg);
	}
	.t-tool-head:hover {
		background: color-mix(in oklab, var(--fg) 4%, transparent);
	}
	.t-tool-name {
		font-weight: 500;
	}
	.t-tool-summary {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.t-tool-chev {
		font-size: 10px;
	}
	.t-tool-body {
		margin: 0;
		padding: 8px 10px;
		font-size: 11.5px;
		line-height: 1.5;
		max-height: 320px;
		overflow: auto;
		background: var(--surface-1);
		border-top: 0.5px solid var(--line);
		white-space: pre-wrap;
		word-break: break-word;
	}
</style>
