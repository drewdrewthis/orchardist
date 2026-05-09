/**
 * Recent-activity lens — anchor: claudeInstances, enriched with the
 * conversation transcript's lastSeenAt (the daemon's
 * ClaudeInstance.lastActivityAt is currently always null; the same
 * sessionUuid appears in `conversations` with the JSONL timestamp).
 * Sort: lastActivityMs desc.
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
		conversations {
			sessionUuid
			lastSeenAt
			messageCount
			open
			recap
		}
	}
`;

interface RecentResponse {
	claudeInstances: SessionCardT[];
	conversations: Array<{
		sessionUuid: string;
		lastSeenAt: string | null;
		messageCount: number;
		open: boolean;
		recap: string | null;
	}>;
}

export interface RecentRow {
	session: SessionCardT;
	/** Best-known activity timestamp: jsonl lastSeenAt > daemon lastActivityAt > 0. */
	lastActivityMs: number;
	messageCount: number;
	open: boolean;
	recap: string | null;
}

export async function fetchRecent(): Promise<RecentRow[]> {
	try {
		const data = await http().request<RecentResponse>(RECENT_QUERY);
		const convByUuid = new Map<string, RecentResponse["conversations"][number]>();
		for (const c of data.conversations) convByUuid.set(c.sessionUuid, c);
		return data.claudeInstances
			.map((s): RecentRow => {
				const conv = convByUuid.get(s.sessionUuid) || null;
				const fromConv = parseTime(conv?.lastSeenAt);
				const fromDaemon = parseTime(s.lastActivityAt);
				return {
					session: s,
					lastActivityMs: fromConv || fromDaemon,
					messageCount: conv?.messageCount ?? 0,
					open: conv?.open ?? false,
					recap: conv?.recap ?? null,
				};
			})
			.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
	} catch (err) {
		console.warn("[orchard-gui] recent lens fetch failed:", err);
		return [];
	}
}
