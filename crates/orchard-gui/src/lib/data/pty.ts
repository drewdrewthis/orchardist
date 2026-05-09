/**
 * Tauri PTY bridge — thin client over the Rust commands in src-tauri/src/pty.rs.
 *
 * The GUI calls `spawnPty` to start a tmux attach (or any other PTY child),
 * gets back a session id + the names of the per-id `pty-data-<id>` and
 * `pty-exit-<id>` events to listen for, then writes user keystrokes via
 * `writePty`. Resize on container resize, kill on unmount.
 *
 * Stays browser-safe: every entry point checks for the Tauri runtime and
 * throws a recognisable `PTY_UNSUPPORTED` error in plain-browser dev so
 * callers can render a "open in desktop app" placeholder.
 */

import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";

export interface PtySpawnResult {
	id: number;
	dataEvent: string;
	exitEvent: string;
}

export interface PtyDataPayload {
	id: number;
	b64: string;
}

export interface PtyExitPayload {
	id: number;
	exitCode: number;
}

export const PTY_UNSUPPORTED = "PTY_UNSUPPORTED";

function ensureTauri(): void {
	const inTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
	if (!inTauri) throw new Error(PTY_UNSUPPORTED);
}

export async function spawnPty(
	argv: string[],
	opts: { cwd?: string; cols?: number; rows?: number } = {},
): Promise<PtySpawnResult> {
	ensureTauri();
	const raw = await invoke<{
		id: number;
		data_event: string;
		exit_event: string;
	}>("pty_spawn", {
		args: { argv, cwd: opts.cwd ?? null, cols: opts.cols ?? null, rows: opts.rows ?? null },
	});
	return { id: raw.id, dataEvent: raw.data_event, exitEvent: raw.exit_event };
}

export async function writePty(id: number, bytes: Uint8Array): Promise<void> {
	ensureTauri();
	await invoke("pty_write", { id, b64: encodeBase64(bytes) });
}

export async function resizePty(id: number, cols: number, rows: number): Promise<void> {
	ensureTauri();
	await invoke("pty_resize", { id, cols, rows });
}

export async function killPty(id: number): Promise<void> {
	ensureTauri();
	try {
		await invoke("pty_kill", { id });
	} catch {
		// Best-effort cleanup; PTY may already be gone via natural exit.
	}
}

export async function listenData(
	dataEvent: string,
	onChunk: (bytes: Uint8Array) => void,
): Promise<UnlistenFn> {
	return await listen<PtyDataPayload>(dataEvent, (e) => {
		try {
			onChunk(decodeBase64(e.payload.b64));
		} catch (err) {
			console.warn("[pty] decode failed:", err);
		}
	});
}

export async function listenExit(
	exitEvent: string,
	onExit: (code: number) => void,
): Promise<UnlistenFn> {
	return await listen<PtyExitPayload>(exitEvent, (e) => onExit(e.payload.exitCode));
}

// --- base64 helpers ---------------------------------------------------------

const CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

function encodeBase64(bytes: Uint8Array): string {
	let out = "";
	for (let i = 0; i < bytes.length; i += 3) {
		const b0 = bytes[i];
		const b1 = bytes[i + 1] ?? 0;
		const b2 = bytes[i + 2] ?? 0;
		out += CHARS[b0 >> 2];
		out += CHARS[((b0 & 0b11) << 4) | (b1 >> 4)];
		out += i + 1 < bytes.length ? CHARS[((b1 & 0b1111) << 2) | (b2 >> 6)] : "=";
		out += i + 2 < bytes.length ? CHARS[b2 & 0b111111] : "=";
	}
	return out;
}

function decodeBase64(input: string): Uint8Array {
	const lookup = new Uint8Array(256).fill(255);
	for (let i = 0; i < CHARS.length; i++) lookup[CHARS.charCodeAt(i)] = i;
	const clean = input.replace(/[\r\n]/g, "");
	const out = new Uint8Array((clean.length / 4) * 3);
	let outIdx = 0;
	for (let i = 0; i < clean.length; i += 4) {
		const c0 = lookup[clean.charCodeAt(i)];
		const c1 = lookup[clean.charCodeAt(i + 1)];
		const c2 = clean.charCodeAt(i + 2) === 61 ? 0 : lookup[clean.charCodeAt(i + 2)];
		const c3 = clean.charCodeAt(i + 3) === 61 ? 0 : lookup[clean.charCodeAt(i + 3)];
		out[outIdx++] = (c0 << 2) | (c1 >> 4);
		if (clean.charCodeAt(i + 2) !== 61) out[outIdx++] = (c1 << 4) | (c2 >> 2);
		if (clean.charCodeAt(i + 3) !== 61) out[outIdx++] = (c2 << 6) | c3;
	}
	return out.slice(0, outIdx);
}
