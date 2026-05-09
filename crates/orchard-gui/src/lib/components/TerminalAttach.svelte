<!--
  Live attached terminal — xterm.js front, Tauri PTY back. Mounts the PTY
  on first render, ferries bytes both ways, resizes on container resize,
  cleans up on unmount.

  Browser-only context renders a "desktop app required" placeholder so
  Vite dev sessions don't show a broken canvas.
-->
<script lang="ts">
	import { onMount } from "svelte";
	import {
		spawnPty,
		writePty,
		resizePty,
		killPty,
		listenData,
		listenExit,
		PTY_UNSUPPORTED,
	} from "$lib/data/pty";
	import type { UnlistenFn } from "@tauri-apps/api/event";

	type Props = {
		argv: string[];
		cwd?: string;
		/** Optional label rendered above the terminal (e.g. session name). */
		label?: string;
	};
	let { argv, cwd, label }: Props = $props();

	const inTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;

	let host: HTMLDivElement | undefined = $state();
	let status = $state<"idle" | "connecting" | "live" | "ended" | "unsupported" | "error">(
		inTauri ? "idle" : "unsupported",
	);
	let exitCode = $state<number | null>(null);
	let errMsg = $state<string | null>(null);

	onMount(() => {
		if (!inTauri) return;
		let id: number | null = null;
		let unsubData: UnlistenFn | null = null;
		let unsubExit: UnlistenFn | null = null;
		let term: import("xterm").Terminal | null = null;
		let fitAddon: import("xterm-addon-fit").FitAddon | null = null;
		let resizeObserver: ResizeObserver | null = null;
		let cancelled = false;

		(async () => {
			status = "connecting";
			try {
				const xterm = await import("xterm");
				const fit = await import("xterm-addon-fit");
				const links = await import("xterm-addon-web-links");
				await import("xterm/css/xterm.css");

				if (cancelled || !host) return;

				term = new xterm.Terminal({
					cursorBlink: true,
					fontFamily:
						"ui-monospace, 'Geist Mono', 'JetBrains Mono', 'SF Mono', Menlo, Consolas, monospace",
					fontSize: 12.5,
					theme: {
						background: "#0a0a0c",
						foreground: "#e9e9ee",
						cursor: "#e9e9ee",
					},
					convertEol: true,
					allowTransparency: false,
				});
				fitAddon = new fit.FitAddon();
				term.loadAddon(fitAddon);
				term.loadAddon(new links.WebLinksAddon());
				term.open(host);
				fitAddon.fit();

				const cols = term.cols;
				const rows = term.rows;
				const handle = await spawnPty(argv, { cwd, cols, rows });
				if (cancelled) {
					killPty(handle.id);
					return;
				}
				id = handle.id;
				status = "live";

				unsubData = await listenData(handle.dataEvent, (bytes) => {
					term?.write(bytes);
				});
				unsubExit = await listenExit(handle.exitEvent, (code) => {
					exitCode = code;
					status = "ended";
				});

				const enc = new TextEncoder();
				term.onData((data) => {
					if (id != null) writePty(id, enc.encode(data)).catch(() => {});
				});

				resizeObserver = new ResizeObserver(() => {
					try {
						fitAddon?.fit();
						if (id != null && term) resizePty(id, term.cols, term.rows).catch(() => {});
					} catch {
						// Window not visible / 0-sized — skip silently.
					}
				});
				resizeObserver.observe(host);
				term.focus();
			} catch (err) {
				if ((err as Error)?.message === PTY_UNSUPPORTED) {
					status = "unsupported";
				} else {
					errMsg = (err as Error)?.message ?? String(err);
					status = "error";
				}
			}
		})();

		return () => {
			cancelled = true;
			resizeObserver?.disconnect();
			unsubData?.();
			unsubExit?.();
			if (id != null) killPty(id).catch(() => {});
			term?.dispose();
		};
	});
</script>

<div class="term-attach">
	{#if label}
		<div class="term-attach-statusbar mono">
			<span class="dimer">tmux</span>
			<span class="dimest">·</span>
			<span>{label}</span>
			<span style="margin-left: auto;" class="dimer">
				{#if status === "live"}⏵ live{:else if status === "connecting"}… connecting{:else if status === "ended"}○ exited ({exitCode}){:else if status === "error"}⚠ {errMsg}{:else if status === "unsupported"}desktop app required{/if}
			</span>
		</div>
	{/if}
	{#if status === "unsupported"}
		<div class="term-attach-fallback">
			<div style="font-size: 13px; font-weight: 500; color: var(--fg-2);">
				Open in the Orchard desktop app to attach this terminal.
			</div>
			<div class="dimer mono" style="font-size: 11.5px; margin-top: 4px;">
				The browser preview can't host a PTY — keystrokes need a desktop shell.
			</div>
		</div>
	{:else if status === "error"}
		<div class="term-attach-fallback">
			<div style="font-size: 13px; color: var(--bad-fg);">Terminal failed to start.</div>
			<div class="dimer mono" style="font-size: 11.5px; margin-top: 4px;">{errMsg}</div>
		</div>
	{:else}
		<div class="term-attach-host" bind:this={host}></div>
	{/if}
</div>

<style>
	.term-attach {
		display: flex;
		flex-direction: column;
		height: 100%;
		min-height: 0;
		background: #0a0a0c;
		border-radius: 8px;
		overflow: hidden;
	}
	.term-attach-statusbar {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 6px 10px;
		font-size: 11.5px;
		color: #e9e9ee;
		border-bottom: 0.5px solid var(--line);
		background: #0e0e11;
	}
	.term-attach-host {
		flex: 1;
		min-height: 0;
		padding: 6px 8px;
	}
	.term-attach-host :global(.xterm) {
		height: 100%;
	}
	.term-attach-host :global(.xterm-viewport) {
		overflow-y: auto;
	}
	.term-attach-fallback {
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 4px;
		padding: 24px 16px;
		text-align: center;
	}
</style>
