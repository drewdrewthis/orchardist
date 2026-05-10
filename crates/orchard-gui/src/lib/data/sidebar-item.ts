/**
 * SidebarItem — the unified row shape per #540 / ADR-015.
 *
 * Drew (2026-05-10): "the sidebar should show items, an item should show
 * a claude session derived from the jsonl file that exists. How we get
 * there and which ones we see depends on the lens, but the item is the
 * same. The sections are what are different."
 *
 * Every lens projects its native query result into `SidebarItem[]`
 * grouped into sections. A `SidebarItem` always represents one Claude
 * session (the jsonl on disk), enriched with whatever context the
 * daemon could attach — worktree, tmux address, PR/issue, lifecycle
 * state, last-active timestamp.
 *
 * Title rules (B2/B3): one place, applied uniformly across every lens.
 * No more pane-id-as-title in the tmux lens; no more lens-specific
 * label divergence.
 */
import type { SessionCardT, WorktreeEnrichment } from "./lenses/fragments";

/**
 * One sidebar section — a label plus the items that belong to it.
 * Empty sections still surface so the user sees "yes I have a worktree
 * here, no sessions running" (per #540 B0).
 *
 * Lives here (alongside `SidebarItem`) so per-lens projection files
 * import the section type from a neutral location instead of
 * cross-coupling to a sibling lens (e.g. issue.ts → attention.ts).
 */
export interface SidebarSection {
	id: string;
	label: string;
	items: SidebarItem[];
}

/**
 * The unified sidebar item. Carries every field a `<SidebarItem>`
 * component needs to render — the lens supplies the projection; the
 * component is pure rendering.
 */
export interface SidebarItem {
	/** Stable id for selection / list keying. Globally unique. */
	id: string;

	/**
	 * Claude session the row represents. Always present — items without
	 * a Claude session are dropped at projection time so the rendering
	 * code never has to branch on absence.
	 */
	session: SessionCardT;

	/**
	 * Derived title per B2 — agentName (when present), customTitle (when
	 * present), then a stable fallback (worktree branch, cwd, or a short
	 * uuid prefix). Computed by `deriveItemTitle` so all lenses share
	 * the rule.
	 */
	title: string;

	/**
	 * Worktree enrichment when the session's cwd resolved to a known
	 * worktree. Carries branch, host, path, and PR/issue join data the
	 * row renders alongside the title.
	 */
	worktree: WorktreeEnrichment | null;

	/**
	 * Tmux address shown as secondary metadata (B3): `session:window.pane`.
	 * Null when the session has no live pane.
	 */
	tmuxAddress: string | null;

	/** Foreground process pid of the Claude REPL, if the daemon resolved it. */
	pid: number | null;

	/** Lifecycle state — working / idle / input / stalled / dead / no_claude. */
	state: SessionCardT["state"];

	/**
	 * Last-active milliseconds since epoch. Per the daemon model:
	 *   1. jsonl tail mtime (`Conversation.lastSeenAt`) when known
	 *   2. else `ClaudeInstance.lastActivityAt`
	 *   3. else 0 (no signal — render as blank)
	 */
	lastActivityMs: number;

	/**
	 * Optional lens-supplied reason chips ("CI failing", "review changes
	 * requested", "idle 12m"). Rendered after the metadata row.
	 */
	reasons: string[];
}

/**
 * Conversation-derived title overrides. Both fields come from the
 * Conversation node in the daemon (jsonl-derived `agent-name` and
 * `custom-title` records). Either may be null until the session
 * records one.
 */
export interface ConversationTitleHints {
	agentName: string | null;
	customTitle: string | null;
}

/**
 * Title derivation per B2. Order:
 *   1. agentName (when the session sets a non-empty one)
 *   2. customTitle (jsonl-defined)
 *   3. worktree branch
 *   4. cwd basename
 *   5. uuid prefix (last 8 chars)
 *
 * Pure — only depends on the inputs. Used by every lens projection so
 * the rule is the same everywhere.
 *
 * @param session - the Claude session anchoring the row
 * @param worktree - resolved worktree, when the cwd matched
 * @param hints - agentName/customTitle pulled from the matching Conversation
 */
export function deriveItemTitle(
	session: SessionCardT,
	worktree: WorktreeEnrichment | null,
	hints: ConversationTitleHints | null = null,
): string {
	if (hints?.agentName) return hints.agentName;
	if (hints?.customTitle) return hints.customTitle;
	if (worktree?.branch) return worktree.branch;
	const cwd = session.process?.cwd;
	if (cwd) {
		const basename = cwd.split("/").filter(Boolean).pop();
		if (basename) return basename;
	}
	return session.sessionUuid.slice(0, 8);
}

/**
 * Tmux address derivation: `session:window.pane`. Returns null when the
 * session has no live pane (offline / standalone / pre-attach).
 */
export function deriveTmuxAddress(session: SessionCardT): string | null {
	const pane = session.pane;
	if (!pane) return null;
	const sessionName = pane.window?.session?.name;
	const windowIndex = pane.window?.index;
	if (sessionName == null || windowIndex == null) return pane.paneId;
	return `${sessionName}:${windowIndex}.${pane.paneId.replace("%", "")}`;
}

/**
 * Build a `SidebarItem` from the raw session + enrichment. Centralises
 * the projection so every lens calls one factory.
 *
 * @param session - the Claude session
 * @param worktree - resolved worktree (null when cwd didn't match)
 * @param lastActivityMs - lens-derived ms since epoch (jsonl > daemon > 0)
 * @param reasons - lens-supplied reason chips
 * @param hints - agentName/customTitle from the matching Conversation node
 */
export function buildSidebarItem(
	session: SessionCardT,
	worktree: WorktreeEnrichment | null,
	lastActivityMs: number,
	reasons: string[] = [],
	hints: ConversationTitleHints | null = null,
): SidebarItem {
	return {
		id: session.id,
		session,
		title: deriveItemTitle(session, worktree, hints),
		worktree,
		tmuxAddress: deriveTmuxAddress(session),
		pid: session.process?.pid ?? null,
		state: session.state,
		lastActivityMs,
		reasons,
	};
}
