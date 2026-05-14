/**
 * Chat plane client — Tauri bridge to chat-core (research/038, #495).
 *
 * v1 path (this module): the GUI talks directly to chat-core via the
 * Tauri commands wired in src-tauri/src/chat.rs. A background watcher
 * in the Tauri shell tails ~/.orchard/chat/*.jsonl and fires a
 * `chat-message-appended` Tauri event for every new line; we listen
 * for those here and re-publish to subscribers.
 *
 * v2 path (deferred): daemon-mediated reads via GraphQL (#498). The
 * `ChatBackend` interface is the seam — switching from local-file to
 * daemon is a backend swap, not a call-site rewrite.
 */

import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import type { Message, SendStatus } from "./types";

export interface ChatRoomSummary {
	id: string;
	messageCount: number;
	memberCount: number;
}

export interface ChatRoomFull {
	id: string;
	messages: ChatCoreMessage[];
	members: ChatCoreMember[];
}

/** Mirror of chat-core's `Message` struct. */
export interface ChatCoreMessage {
	id: string;
	ts: string;
	sender: string;
	sender_machine: string;
	text: string;
	source: string;
}

/** Mirror of chat-core's `Member` struct. */
export interface ChatCoreMember {
	handle: string;
	machine: string;
	tmux_session: string;
	joined_at: string;
}

export type AppendedPayload =
	| { kind: "message"; room: string; line: ChatCoreMessage }
	| { kind: "member_joined"; room: string; line: ChatCoreMember }
	| { kind: "member_left"; room: string; handle: string };

export interface FanoutOutcomeView {
	kind: "delivered" | "byte_only" | "failed" | "skipped";
	recipient: string;
	scrollback_verified_at?: string;
	reason?: string;
	error?: string;
}

export interface SendOutcomeView {
	message_id: string;
	room: string;
	fanout: FanoutOutcomeView[];
}

export interface ChatBackend {
	listRooms(): Promise<ChatRoomSummary[]>;
	loadRoom(roomId: string): Promise<ChatRoomFull>;
	sendMessage(target: string, text: string, sender?: string): Promise<SendOutcomeView>;
	subscribeAppends(onPayload: (p: AppendedPayload) => void): Promise<UnlistenFn>;
	isReachable(): Promise<boolean>;
}

class TauriBackend implements ChatBackend {
	async listRooms(): Promise<ChatRoomSummary[]> {
		try {
			const rs = await invoke<
				Array<{ id: string; message_count: number; member_count: number }>
			>("chat_list_rooms");
			return rs.map((r) => ({
				id: r.id,
				messageCount: r.message_count,
				memberCount: r.member_count,
			}));
		} catch {
			// intentional swallow: chat-core IPC unavailable (daemon not running or non-Tauri env); caller degrades to empty list
			return [];
		}
	}

	async loadRoom(roomId: string): Promise<ChatRoomFull> {
		return await invoke<ChatRoomFull>("chat_load_room", { room: roomId });
	}

	async sendMessage(
		target: string,
		text: string,
		sender?: string,
	): Promise<SendOutcomeView> {
		return await invoke<SendOutcomeView>("chat_send", {
			target,
			text,
			sender: sender ?? null,
		});
	}

	async subscribeAppends(onPayload: (p: AppendedPayload) => void): Promise<UnlistenFn> {
		return await listen<AppendedPayload>("chat-message-appended", (e) => onPayload(e.payload));
	}

	async isReachable(): Promise<boolean> {
		try {
			await invoke("chat_list_rooms");
			return true;
		} catch {
			// intentional swallow: reachability probe — failure means unreachable, callers receive false
			return false;
		}
	}
}

class StubBackend implements ChatBackend {
	async listRooms(): Promise<ChatRoomSummary[]> {
		return [];
	}
	async loadRoom(roomId: string): Promise<ChatRoomFull> {
		return { id: roomId, messages: [], members: [] };
	}
	async sendMessage(_target: string, _text: string): Promise<SendOutcomeView> {
		throw new Error("chat-backend: not running in a Tauri shell");
	}
	async subscribeAppends(_onPayload: (p: AppendedPayload) => void): Promise<UnlistenFn> {
		return async () => {};
	}
	async isReachable(): Promise<boolean> {
		return false;
	}
}

let _backend: ChatBackend | null = null;

export function getChatBackend(): ChatBackend {
	if (_backend) return _backend;
	const inTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
	_backend = inTauri ? new TauriBackend() : new StubBackend();
	return _backend;
}

export function setChatBackend(b: ChatBackend) {
	_backend = b;
}

/** Convert a chat-core Message into the GUI's Message type. */
export function chatCoreToGuiMessage(m: ChatCoreMessage, selfHandle?: string): Message {
	const isSelf = selfHandle != null && m.sender === selfHandle;
	return {
		id: m.id,
		role: isSelf ? "user" : "agent",
		agentId: isSelf ? undefined : m.sender,
		status: "read" as SendStatus,
		ts: Date.parse(m.ts) || Date.now(),
		text: m.text,
	};
}

/**
 * Resolve the local user's chat handle. Cached for the session — derived
 * once from the Tauri shell so we don't pay the IPC on every message.
 */
let _selfHandle: string | null = null;
export async function getSelfHandle(): Promise<string | null> {
	if (_selfHandle != null) return _selfHandle;
	try {
		const inTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
		if (!inTauri) return null;
		_selfHandle = await invoke<string>("chat_self_handle");
	} catch {
		// intentional swallow: handle discovery is best-effort; null means messages render without a self-attribution
		_selfHandle = null;
	}
	return _selfHandle;
}
