/**
 * Recent-activity lens — anchor: ALL conversations known to the daemon
 * (not just live REPL processes). Drew (2026-05-15): "Recent doesn't
 * show this convo, it should show all convos." Previously this anchored
 * on `claudeInstances` which is the live-process surface — dormant /
 * historical sessions were filtered out.
 *
 * Sort: lastActivityMs desc. When a live ClaudeInstance exists for a
 * conversation's sessionUuid, lift the row's state/process/pane/worktree
 * from there; otherwise render a dormant row anchored on the
 * conversation alone.
 */
import { RecentLensStore, type RecentLens$result } from "$houdini";
import { parseTime } from "./client";
import type { SessionCardT, WorktreeEnrichment } from "./fragments";
import type { SidebarItem } from "$lib/data/sidebar-item";
import { buildSidebarItem } from "$lib/data/sidebar-item";

/** Singleton store for the recent lens. */
export const recentStore = new RecentLensStore();

type Data = NonNullable<RecentLens$result>;

/**
 * Build a synthetic SessionCardT shape for a dormant conversation (no
 * live ClaudeInstance). The row component expects a `session` field so
 * we satisfy the shape with state="no_claude" and pull the title hints
 * from the conversation itself.
 */
function dormantSessionFromConversation(
	conv: NonNullable<Data["conversations"]>[number],
): SessionCardT {
	return {
		// Conversation has no real ClaudeInstance id; use a synthetic id so
		// keyed-each stays stable. Prefix with `conv:` to avoid colliding
		// with live ClaudeInstance ids.
		id: `conv:${conv.sessionUuid}`,
		sessionUuid: conv.sessionUuid,
		state: "no_claude",
		lastActivityAt: conv.lastSeenAt ?? null,
		startedAt: conv.firstSeenAt ?? null,
		rcEnabled: false,
		account: null,
		pane: null,
		process: conv.cwd ? { pid: 0, cwd: conv.cwd } : null,
		worktree: null,
		conversation: {
			agentName: conv.agentName ?? null,
			customTitle: conv.customTitle ?? null,
			lastSeenAt: conv.lastSeenAt ?? null,
		},
	} as unknown as SessionCardT;
}

/**
 * Project all conversations into a flat, time-sorted SidebarItem[].
 * Pure — call inside `$derived` against `$recentStore.data`.
 */
export function buildRecentItems(
	data: Data | null | undefined,
): SidebarItem[] {
	if (!data) return [];

	// Build a lookup of live ClaudeInstance by sessionUuid for the enrichment overlay.
	const liveBySessionUuid = new Map<string, SessionCardT>();
	for (const ci of (data.claudeInstances ?? []) as unknown as SessionCardT[]) {
		if (ci.sessionUuid) liveBySessionUuid.set(ci.sessionUuid, ci);
	}

	type Row = { session: SessionCardT; worktree: WorktreeEnrichment | null; lastActivityMs: number; hints: { agentName: string | null; customTitle: string | null } | null };
	const rows: Row[] = [];

	for (const conv of data.conversations ?? []) {
		const live = liveBySessionUuid.get(conv.sessionUuid);
		const session = live ?? dormantSessionFromConversation(conv);
		// Activity time: conversation's jsonl lastSeenAt is authoritative.
		// Falls back to live's lastActivityAt when conv didn't record one.
		const lastActivityMs =
			parseTime(conv.lastSeenAt) || parseTime(live?.lastActivityAt);
		const worktree = (live?.worktree ?? null) as WorktreeEnrichment | null;
		const hints = {
			agentName: conv.agentName ?? null,
			customTitle: conv.customTitle ?? null,
		};
		rows.push({ session, worktree, lastActivityMs, hints });
	}

	rows.sort((a, b) => b.lastActivityMs - a.lastActivityMs);
	// `buildSidebarItem` keys items by `session.id`. A live ClaudeInstance
	// reused across multiple conversations (e.g., a resumed pid) would
	// generate identical ids → keyed-each crash. Override the id with
	// `conv:<sessionUuid>` for the keying so every conversation row is
	// globally unique. The underlying session reference stays untouched.
	const seen = new Set<string>();
	const out: SidebarItem[] = [];
	for (const r of rows) {
		const it = buildSidebarItem(r.session, r.worktree, r.lastActivityMs, [], r.hints);
		const uuid = r.session.sessionUuid;
		const id = uuid ? `conv:${uuid}` : it.id;
		if (seen.has(id)) continue;
		seen.add(id);
		out.push({ ...it, id });
	}
	return out;
}
