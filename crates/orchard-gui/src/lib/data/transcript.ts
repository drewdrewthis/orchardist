/**
 * Transcript reader — pulls a Claude Code .jsonl conversation from the
 * **daemon** (`GET /v1/conversations/<sessionUuid>/jsonl?lastN=<N>`).
 *
 * The daemon's tail-N mode reads only the bytes needed to land on the
 * Nth-from-last newline (worst case ~chunk*ceil(N/avg-line-length) bytes
 * touched), then streams from there to EOF. A multi-MB transcript serves
 * its last 200 turns in well under 100ms — instant in the renderer.
 *
 * Falls back to the legacy Tauri filesystem read when the daemon is
 * unreachable (e.g. dev shell with no daemon up); browser dev preview
 * works through the Vite `/__daemon` proxy → daemon, no Tauri required.
 */

import { invoke } from "@tauri-apps/api/core";

export interface TranscriptChunk {
	path: string;
	size: number;
	truncated: boolean;
	text: string;
}

export const TRANSCRIPT_UNSUPPORTED = "TRANSCRIPT_UNSUPPORTED";

/**
 * Resolve the transcript URL for the daemon's HTTP endpoint. In Tauri
 * we hit 127.0.0.1:7777 directly; in browser dev we go through Vite's
 * `/__daemon` proxy (configured in vite.config.js).
 */
function transcriptURL(sessionUuid: string, lastN?: number): string {
	const inTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
	const base = inTauri ? "http://127.0.0.1:7777" : "/__daemon";
	const q = lastN != null ? `?lastN=${lastN}` : "";
	return `${base}/v1/conversations/${encodeURIComponent(sessionUuid)}/jsonl${q}`;
}

/**
 * Read the last N records of a transcript via the daemon HTTP endpoint.
 * Default lastN=200 keeps the renderer responsive on huge transcripts.
 *
 * Falls back to the Tauri filesystem reader when the daemon is unreachable
 * AND we're inside Tauri. Browser dev with no daemon → throws.
 */
export async function readTranscript(
	pathOrSession: string,
	maxBytes?: number,
	sessionUuid?: string,
): Promise<TranscriptChunk> {
	const lastN = 200;
	const uuid = sessionUuid ?? guessUUIDFromPath(pathOrSession);
	if (uuid) {
		try {
			const url = transcriptURL(uuid, lastN);
			const res = await fetch(url, { headers: { Accept: "application/x-ndjson" } });
			if (res.ok) {
				const text = await res.text();
				const startOffset = Number(res.headers.get("X-Orchard-StartOffset") ?? "0");
				const fullSize = Number(res.headers.get("Content-Length") ?? text.length) + startOffset;
				return {
					path: pathOrSession,
					size: fullSize,
					truncated: startOffset > 0,
					text,
				};
			}
		} catch {
			// Network/proxy down — fall through to Tauri.
		}
	}
	// Last-resort Tauri fallback (dev shell with daemon down).
	const inTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
	if (!inTauri) throw new Error(TRANSCRIPT_UNSUPPORTED);
	const raw = await invoke<{
		path: string;
		size: number;
		truncated: boolean;
		text: string;
	}>("read_transcript_jsonl", { path: pathOrSession, maxBytes: maxBytes ?? null });
	return raw;
}

/**
 * Claude Code names every JSONL by its sessionUuid: `<uuid>.jsonl`. When
 * the caller passes a path instead of a uuid, recover the uuid from the
 * filename so we can hit the daemon endpoint without forcing every call
 * site to thread the uuid through.
 */
function guessUUIDFromPath(path: string): string | null {
	const m = path.match(/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$/i);
	return m ? m[1] : null;
}

/**
 * A renderable turn distilled from one JSONL row. We deliberately keep
 * this thin — only what the chat renderer needs. Tool calls / results
 * are flattened into per-block summaries the renderer can lay out.
 */
export type TranscriptBlock =
	| { kind: "text"; text: string }
	| { kind: "tool_use"; name: string; input: unknown; toolId: string }
	| { kind: "tool_result"; toolId: string; text: string; isError: boolean }
	| { kind: "thinking"; text: string };

export interface TranscriptTurn {
	uuid: string;
	parentUuid: string | null;
	role: "user" | "assistant" | "system";
	timestamp: number;
	model: string | null;
	blocks: TranscriptBlock[];
	/** True when this turn is purely tool-result feedback (no text). */
	toolFeedback: boolean;
}

interface RawLine {
	type?: string;
	uuid?: string;
	parentUuid?: string | null;
	timestamp?: string;
	message?: {
		role?: string;
		model?: string;
		content?: Array<{
			type: string;
			text?: string;
			thinking?: string;
			id?: string;
			name?: string;
			input?: unknown;
			tool_use_id?: string;
			content?: unknown;
			is_error?: boolean;
		}>;
	};
}

const SKIP_TYPES = new Set(["agent-name", "permission-mode", "pr-link", "summary"]);

export function parseTranscript(text: string): TranscriptTurn[] {
	const turns: TranscriptTurn[] = [];
	for (const line of text.split("\n")) {
		if (!line.trim()) continue;
		let row: RawLine;
		try {
			row = JSON.parse(line);
		} catch {
			continue;
		}
		if (row.type && SKIP_TYPES.has(row.type)) continue;
		if (!row.message || !row.uuid) continue;

		const role = (row.message.role || row.type || "user") as TranscriptTurn["role"];
		const blocks: TranscriptBlock[] = [];

		const content = Array.isArray(row.message.content) ? row.message.content : [];
		for (const c of content) {
			if (c.type === "text" && typeof c.text === "string") {
				blocks.push({ kind: "text", text: c.text });
			} else if (c.type === "thinking" && typeof c.thinking === "string") {
				blocks.push({ kind: "thinking", text: c.thinking });
			} else if (c.type === "tool_use") {
				blocks.push({
					kind: "tool_use",
					name: c.name || "tool",
					input: c.input,
					toolId: c.id || "",
				});
			} else if (c.type === "tool_result") {
				const txt = stringifyToolResult(c.content);
				blocks.push({
					kind: "tool_result",
					toolId: c.tool_use_id || "",
					text: txt,
					isError: !!c.is_error,
				});
			}
		}

		// User turns sometimes carry plain string content rather than an array.
		const rawContent = (row.message as { content?: unknown }).content;
		if (blocks.length === 0 && typeof rawContent === "string") {
			blocks.push({ kind: "text", text: rawContent });
		}

		if (blocks.length === 0) continue;

		turns.push({
			uuid: row.uuid,
			parentUuid: row.parentUuid ?? null,
			role,
			timestamp: row.timestamp ? Date.parse(row.timestamp) : 0,
			model: row.message.model ?? null,
			blocks,
			toolFeedback:
				role === "user" &&
				blocks.length > 0 &&
				blocks.every((b) => b.kind === "tool_result"),
		});
	}
	return turns;
}

function stringifyToolResult(content: unknown): string {
	if (typeof content === "string") return content;
	if (Array.isArray(content)) {
		return content
			.map((c) => {
				if (typeof c === "string") return c;
				if (c && typeof c === "object" && "text" in c && typeof (c as { text: unknown }).text === "string") {
					return (c as { text: string }).text;
				}
				return JSON.stringify(c);
			})
			.join("\n");
	}
	if (content == null) return "";
	return JSON.stringify(content);
}
