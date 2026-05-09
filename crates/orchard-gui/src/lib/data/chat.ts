/**
 * Chat plane client (research/038).
 *
 * Per research/038, the chat substrate is `~/.orchard/chat/*.jsonl` files
 * written by `chat-core` (Rust) and read by the daemon's Go chat provider.
 * The GUI doesn't touch the JSONL directly — it goes through the daemon's
 * GraphQL surface:
 *
 *   query: chatRoom(id) { messages { id, ts, sender, text, ... } }
 *   subscription: chatMessageAppended(room) { ... }
 *
 * That schema is **not yet shipped on the daemon** (the Go chat provider is
 * being built in parallel by side-thread @61). This module ships the client
 * shape now so:
 *
 *   1. UI components can plumb against a real interface today.
 *   2. When the daemon provider lands, only `query`/`subscription` strings
 *      and the response mapping change — call sites are unchanged.
 *
 * Until then, every method returns mock data that mirrors the v1 protocol.
 *
 * Sending: chat send is a Layer 2 stateful op (per research/037 §"Two-layer
 * write model"). It flows through the daemon's write protocol — NOT through
 * the JSONL files directly, NOT through Tauri/worktree-core. When the write
 * protocol decision lands (HTTP queue or gRPC, research/037 §1), this file
 * is the single place to update.
 */

import type { Message } from "./types";

export interface ChatRoom {
	id: string;
	title: string;
	topic: string | null;
	participants: string[];
}

export interface ChatBackend {
	/** Initial backfill of a room's messages. */
	loadRoom(roomId: string): Promise<Message[]>;
	/** Subscribe to new messages in a room. Returns an unsubscribe fn. */
	subscribeRoom(roomId: string, onMessage: (m: Message) => void): () => void;
	/** Submit a new message. Returns the assigned id once acked. */
	sendMessage(roomId: string, text: string, sender: string): Promise<string>;
	/** Whether the backend is currently reachable. */
	isReachable(): Promise<boolean>;
}

class StubBackend implements ChatBackend {
	async loadRoom(_roomId: string): Promise<Message[]> {
		return [];
	}
	subscribeRoom(_roomId: string, _onMessage: (m: Message) => void): () => void {
		return () => {};
	}
	async sendMessage(_roomId: string, _text: string, _sender: string): Promise<string> {
		throw new Error("chat-backend: send not yet wired (depends on research/038 daemon provider)");
	}
	async isReachable(): Promise<boolean> {
		return false;
	}
}

let _backend: ChatBackend = new StubBackend();

export function getChatBackend(): ChatBackend {
	return _backend;
}

/** Test/dev hook to swap the backend. */
export function setChatBackend(b: ChatBackend) {
	_backend = b;
}
