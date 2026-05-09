/**
 * Transcript reader — pulls a Claude Code .jsonl conversation from the
 * filesystem via Tauri (`Conversation.jsonlPath` on the daemon points
 * at this file). The Rust side returns up to N bytes from the end of
 * the file; we parse the lines and shape them into renderable turns.
 *
 * Falls back to a structured error in the browser dev preview — the
 * renderer can show a "desktop app required" placeholder.
 */

import { invoke } from "@tauri-apps/api/core";

export interface TranscriptChunk {
	path: string;
	size: number;
	truncated: boolean;
	text: string;
}

export const TRANSCRIPT_UNSUPPORTED = "TRANSCRIPT_UNSUPPORTED";

function ensureTauri(): void {
	const inTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
	if (!inTauri) throw new Error(TRANSCRIPT_UNSUPPORTED);
}

export async function readTranscript(path: string, maxBytes?: number): Promise<TranscriptChunk> {
	ensureTauri();
	const raw = await invoke<{
		path: string;
		size: number;
		truncated: boolean;
		text: string;
	}>("read_transcript_jsonl", { path, maxBytes: maxBytes ?? null });
	return raw;
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
		if (blocks.length === 0 && typeof (row.message as { content?: unknown }).content === "string") {
			blocks.push({ kind: "text", text: (row.message as { content: string }).content });
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
