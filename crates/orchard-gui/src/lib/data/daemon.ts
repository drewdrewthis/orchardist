/**
 * The thin slice of daemon access not yet on Houdini: a single
 * non-data subscription that the transcript view uses to know when
 * to retail the JSONL on disk. Everything else flows through Houdini's
 * normalized cache (`$houdini` queries + the WS link in `client.ts`).
 *
 * `subscribeConversation` doesn't return a payload we want to render
 * directly — it's a wakeup signal. The handler is responsible for
 * re-reading the file via Tauri. Keeping this on `graphql-ws` directly
 * avoids the round-trip of materializing a Houdini live query for
 * something the cache wouldn't otherwise be holding.
 */

import { gql } from "graphql-request";
import { createClient as createWsClient, type Client as WsClient } from "graphql-ws";

function endpoints(): { ws: string } {
	if (typeof window === "undefined") {
		return { ws: "ws://127.0.0.1:7777/graphql" };
	}
	const wsProto = window.location.protocol === "https:" ? "wss:" : "ws:";
	return { ws: `${wsProto}//${window.location.host}/__daemon/graphql` };
}

/** Same-origin daemon GraphQL HTTP endpoint (via the Vite/boxd `/__daemon` proxy). */
function httpEndpoint(): string {
	if (typeof window === "undefined" || !window.location) {
		return "http://127.0.0.1:7777/graphql";
	}
	return `${window.location.origin}/__daemon/graphql`;
}

let _ws: WsClient | null = null;
function ws(): WsClient {
	if (!_ws) _ws = createWsClient({ url: endpoints().ws, lazy: true });
	return _ws;
}

export type Unsub = () => void;

const CONVERSATION_CHANGED = gql`
	subscription ConversationChanged($sessionUuid: String!) {
		conversationChanged(sessionUuid: $sessionUuid) {
			sessionUuid
			lastSeenAt
			messageCount
		}
	}
`;

/**
 * Push subscription for a single Claude conversation, keyed by sessionUuid.
 * Backed by `Subscription.conversationChanged(sessionUuid:)` on the daemon,
 * which fires every time the claudeprojects fsnotify watcher invalidates
 * the matching JSONL. The handler receives no payload — call back into
 * the file reader to pick up the new tail.
 */
export function subscribeConversation(
	sessionUuid: string,
	onChange: () => void,
	onErr?: (e: unknown) => void,
): Unsub {
	return ws().subscribe(
		{ query: CONVERSATION_CHANGED, variables: { sessionUuid } },
		{
			next: () => onChange(),
			error: (e) => onErr?.(e),
			complete: () => {},
		},
	);
}

export interface LaunchSessionInput {
	/** Absolute working directory (a worktree path) to launch the session in. */
	cwd: string;
	/** Optional tmux session name; daemon derives + de-dupes one when absent. */
	name?: string;
	/** Optional model alias/full name passed to `claude --model`. */
	model?: string;
	/** Optional first prompt — Claude starts on it immediately. */
	prompt?: string;
}

export interface LaunchSessionResult {
	sessionName: string;
	paneId: string;
	sessionUuid: string;
	cwd: string;
}

const LAUNCH_SESSION = `
	mutation LaunchSession($input: LaunchSessionInput!) {
		launchSession(input: $input) {
			sessionName
			paneId
			sessionUuid
			cwd
		}
	}
`;

/**
 * Create a new Claude REPL via the daemon's `launchSession` mutation —
 * the "create" counterpart to sendTextToPane (chat). The daemon creates a
 * detached tmux session at `cwd` and boots `claude` inside it with a
 * pre-assigned session UUID, returned here so the caller can open the
 * session (paneId for chat sends, sessionUuid for the transcript
 * subscription) without waiting for the daemon's next poll.
 *
 * Plain fetch (not Houdini): this is an imperative action, not cached
 * read state — same rationale as sendTextToPane in $lib/tauri.ts.
 */
export async function launchSession(input: LaunchSessionInput): Promise<LaunchSessionResult> {
	const res = await fetch(httpEndpoint(), {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ query: LAUNCH_SESSION, variables: { input } }),
	});
	if (!res.ok) {
		throw new Error(`launchSession HTTP ${res.status}`);
	}
	const body = await res.json();
	if (body.errors && body.errors.length > 0) {
		throw new Error(body.errors[0].message ?? "launchSession failed");
	}
	const launched = body.data?.launchSession;
	if (!launched) {
		throw new Error("launchSession returned no data");
	}
	return launched as LaunchSessionResult;
}
