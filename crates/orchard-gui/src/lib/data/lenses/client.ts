/**
 * Shared HTTP client for lens queries. The daemon's Vite proxy lives at
 * /__daemon, mirroring the legacy Dashboard query's transport so we
 * don't fight CORS in the Tauri webview.
 */
import { GraphQLClient } from "graphql-request";

let _http: GraphQLClient | null = null;

function endpoint(): string {
	if (typeof window === "undefined") return "http://127.0.0.1:7777/graphql";
	return `${window.location.origin}/__daemon/graphql`;
}

export function http(): GraphQLClient {
	if (!_http) _http = new GraphQLClient(endpoint());
	return _http;
}

/** Parse an RFC3339 timestamp into ms-since-epoch, or 0 when null/empty. */
export function parseTime(t: string | null | undefined): number {
	if (!t) return 0;
	return Date.parse(t) || 0;
}
