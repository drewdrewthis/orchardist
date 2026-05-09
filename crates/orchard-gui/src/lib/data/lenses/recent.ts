/**
 * Recent-activity lens — anchor: claudeInstances, sort: lastActivityAt desc.
 *
 * No filtering by state. Every Claude session the daemon knows about
 * appears, freshest first. The row component handles dim/dead styling
 * for stale ones.
 */
import { gql } from "graphql-request";
import { http, parseTime } from "./client";
import { SESSION_CARD_FRAGMENT, type SessionCardT } from "./fragments";

const RECENT_QUERY = gql`
	${SESSION_CARD_FRAGMENT}
	query RecentLens {
		claudeInstances {
			...SessionCard
		}
	}
`;

export interface RecentRow {
	session: SessionCardT;
	lastActivityMs: number;
}

export async function fetchRecent(): Promise<RecentRow[]> {
	try {
		const data = await http().request<{ claudeInstances: SessionCardT[] }>(RECENT_QUERY);
		return data.claudeInstances
			.map((s) => ({ session: s, lastActivityMs: parseTime(s.lastActivityAt) }))
			.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
	} catch (err) {
		console.warn("[orchard-gui] recent lens fetch failed:", err);
		return [];
	}
}
