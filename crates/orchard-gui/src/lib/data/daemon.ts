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
