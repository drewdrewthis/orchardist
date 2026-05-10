/**
 * Houdini client. One transport pair into the local daemon at
 * `127.0.0.1:7777`, proxied through Vite at `/__daemon` so the Tauri
 * webview's `tauri://localhost` origin doesn't trip CORS.
 *
 * Reads (queries, mutations) go over HTTP; live updates ride a single
 * graphql-ws WebSocket. The cache itself is normalized — every entity
 * keyed by `__typename:id` — which is why subscriptions like
 * `tmuxSessionsChanged` patch precisely the affected nodes instead of
 * thrashing the whole snapshot.
 */
import { HoudiniClient, subscription } from "$houdini";
import { createClient as createWSClient } from "graphql-ws";

function wsUrl(): string {
	if (typeof window === "undefined") return "ws://127.0.0.1:7777/graphql";
	const proto = window.location.protocol === "https:" ? "wss" : "ws";
	return `${proto}://${window.location.host}/__daemon/graphql`;
}

function httpUrl(): string {
	if (typeof window === "undefined") return "http://127.0.0.1:7777/graphql";
	return `${window.location.origin}/__daemon/graphql`;
}

export default new HoudiniClient({
	url: httpUrl(),
	plugins: [
		subscription(() =>
			createWSClient({
				url: wsUrl(),
				lazy: true,
			}),
		),
	],
});
