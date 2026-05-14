<!--
  Live attached terminal — xterm.js front, Tauri PTY back. The xterm
  instance is created once on mount and reused across pane swaps; only
  the PTY child is replaced.

  When the parent supplies a new `argv` (i.e. the user clicked a
  different sidebar row), the existing PTY is killed and a fresh one
  spawned in its place — but the xterm canvas, scrollback, and
  ResizeObserver stay live. This avoids the ~200ms full-remount flash
  and keeps the terminal feeling instant.

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
	import { toast } from "$lib/util/toast";

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

	// Mutable runtime — owned by onMount, mutated by $effect below.
	let term: import("xterm").Terminal | null = null;
	let fitAddon: import("xterm-addon-fit").FitAddon | null = null;
	let ptyId: number | null = null;
	let unsubData: UnlistenFn | null = null;
	let unsubExit: UnlistenFn | null = null;
	let cancelled = false;
	let lastArgvKey = "";
	let mounted = false;

	function argvKey(a: string[]): string {
		return JSON.stringify(a);
	}

	async function killCurrentPty() {
		try {
			unsubData?.();
		} catch {
			// intentional swallow: pty already dead during cleanup; unsub throws harmlessly
		}
		try {
			unsubExit?.();
		} catch {
			// intentional swallow: pty already dead during cleanup; unsub throws harmlessly
		}
		unsubData = null;
		unsubExit = null;
		if (ptyId != null) {
			const old = ptyId;
			ptyId = null;
			// intentional swallow: pty already dead during cleanup
			killPty(old).catch(() => {});
		}
	}

	async function startPty(currentArgv: string[]) {
		if (!term || !fitAddon) return;
		await killCurrentPty();
		// Wipe the visible buffer between attach cycles so the new pane
		// paints from a clean slate. Scrollback is the prior session;
		// keeping it would be confusing for a row swap.
		term.reset();
		try {
			fitAddon.fit();
		} catch {
			// host may be 0-sized (off-screen); tmux will redraw on resize.
		}
		const cols = term.cols;
		const rows = term.rows;
		status = "connecting";
		try {
			const handle = await spawnPty(currentArgv, { cwd, cols, rows });
			if (cancelled || lastArgvKey !== argvKey(currentArgv)) {
				// intentional swallow: stale PTY spawned after argv changed; kill is best-effort
				killPty(handle.id).catch(() => {});
				return;
			}
			ptyId = handle.id;
			status = "live";
			unsubData = await listenData(handle.dataEvent, (bytes) => {
				term?.write(bytes);
			});
			unsubExit = await listenExit(handle.exitEvent, (code) => {
				exitCode = code;
				status = "ended";
			});
		} catch (err) {
			if ((err as Error)?.message === PTY_UNSUPPORTED) {
				status = "unsupported";
			} else {
				errMsg = (err as Error)?.message ?? String(err);
				status = "error";
			}
		}
	}

	onMount(() => {
		if (!inTauri) return;
		let resizeObserver: ResizeObserver | null = null;
		(async () => {
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
				try {
					// Detect macOS: cmd-click is the platform convention; other
					// platforms use a plain click.
					const isMac =
						typeof navigator !== "undefined" &&
						navigator.platform.toUpperCase().includes("MAC");

					/**
					 * Open a URL using the Tauri shell opener when running inside
					 * the desktop app, falling back to window.open for browser dev.
					 */
					async function openUrl(uri: string): Promise<void> {
						if (inTauri) {
							const { openUrl: tauriOpen } = await import(
								"@tauri-apps/plugin-opener"
							);
							await tauriOpen(uri);
						} else {
							window.open(uri, "_blank", "noopener,noreferrer");
						}
					}

					/**
					 * activateCallback for WebLinksAddon.
					 * On macOS, only activate on cmd-click (metaKey).
					 * On other platforms, activate on a plain click.
					 */
					function handleLink(event: MouseEvent, uri: string): void {
						if (isMac && !event.metaKey) return;
						openUrl(uri).catch(toast.error);
					}

					term.loadAddon(new links.WebLinksAddon(handleLink));
				} catch (err) {
					toast.error(err);
				}
				term.open(host);
				fitAddon.fit();

				const enc = new TextEncoder();
				term.onData((data) => {
					// intentional swallow: keystrokes may arrive after PTY exits; drop silently
				if (ptyId != null) writePty(ptyId, enc.encode(data)).catch(() => {});
				});

				resizeObserver = new ResizeObserver(() => {
					try {
						fitAddon?.fit();
						// intentional swallow: PTY may have exited between resize check and IPC call
						if (ptyId != null && term) resizePty(ptyId, term.cols, term.rows).catch(() => {});
					} catch {
						// Window not visible / 0-sized — skip silently.
					}
				});
				resizeObserver.observe(host);
				term.focus();
				mounted = true;
				lastArgvKey = argvKey(argv);
				await startPty(argv);
			} catch (err) {
				errMsg = (err as Error)?.message ?? String(err);
				status = "error";
			}
		})();

		return () => {
			cancelled = true;
			resizeObserver?.disconnect();
			killCurrentPty();
			term?.dispose();
			term = null;
			fitAddon = null;
		};
	});

	// Re-attach when argv changes after mount (the user clicked a
	// different row, or the panel resolved a different pane).
	$effect(() => {
		const key = argvKey(argv);
		if (!mounted || key === lastArgvKey) return;
		lastArgvKey = key;
		startPty(argv);
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
