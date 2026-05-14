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
import type { PaneCardT, SessionCardT, WorktreeEnrichment } from "./lenses/fragments";

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
 * Default branch names that aren't useful as a title — every repo has
 * one and they look identical across rows. When the branch is one of
 * these, fall through to the cwd basename so the user sees what
 * directory the session is in instead of a sea of "main" / "master".
 */
const GENERIC_BRANCHES = new Set(["main", "master", "trunk", "HEAD", "develop", "dev"]);

/**
 * Title derivation per B2. Order:
 *   1. agentName (when the session sets a non-empty one)
 *   2. customTitle (jsonl-defined)
 *   3. worktree branch — UNLESS the branch is generic (main/master/etc),
 *      in which case the cwd basename wins (more identifying)
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
	const branch = worktree?.branch;
	const cwd = session.process?.cwd;
	const cwdBase = cwd ? cwd.split("/").filter(Boolean).pop() : null;
	// Branch wins UNLESS it's a generic name like "main" — then prefer cwd.
	if (branch && !GENERIC_BRANCHES.has(branch)) return branch;
	if (cwdBase) return cwdBase;
	if (branch) return branch;
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

/**
 * Build a sidebar row for a tmux pane that has no attached Claude session.
 * Keyed by the pane's `paneId` so that two panes on the same worktree each
 * get their own row. Title comes from pane.title → pane.currentCommand →
 * pane.paneId. `state="no_claude"` drives the renderer (no state pill).
 *
 * @param pane - the tmux pane (PaneCard shape)
 * @param worktree - resolved worktree for the pane's process, if any
 * @param lastActivityMs - activity timestamp; 0 when not known
 */
export function buildTmuxOnlyPaneItem(
	pane: PaneCardT,
	worktree: WorktreeEnrichment | null,
	lastActivityMs: number = 0,
): SidebarItem {
	const title = pane.title || pane.currentCommand || pane.paneId;
	return {
		id: `pane:${pane.paneId}`,
		// Synthetic placeholder so the existing SidebarItem component renders
		// the row uniformly; no live Claude session exists for this pane.
		session: {
			id: `pane:${pane.paneId}`,
			sessionUuid: "",
			state: "no_claude",
			startedAt: null,
			lastActivityAt: null,
			rcEnabled: false,
			account: null,
			pane: {
				paneId: pane.paneId,
				title: pane.title,
				currentCommand: pane.currentCommand,
				window: pane.window,
			},
			process: pane.process ? { pid: pane.process.pid, cwd: pane.process.cwd } : null,
			worktree: worktree ?? null,
			conversation: null,
		} as unknown as SessionCardT,
		title,
		worktree,
		tmuxAddress: (() => {
			const w = pane.window;
			const sessionName = w?.session?.name;
			const windowIndex = w?.index;
			if (sessionName == null || windowIndex == null) return pane.paneId;
			return `${sessionName}:${windowIndex}.${pane.paneId.replace("%", "")}`;
		})(),
		pid: pane.process?.pid ?? null,
		state: "no_claude",
		lastActivityMs,
		reasons: [],
	};
}

/**
 * Build a sidebar row for a worktree with NO running Claude session.
 * Drew (2026-05-10): "if there is a live worktree, that means it should
 * be visible in the worktrees [lens]." A dormant row still shows host /
 * branch / PR / issue chips so the user can act on it (open a session,
 * destroy, etc.) — `state="no_claude"` drives the renderer.
 */
export function buildDormantWorktreeItem(
	worktree: WorktreeEnrichment,
): SidebarItem {
	const branch = worktree.branch;
	const cwdBase = worktree.path
		? worktree.path.split("/").filter(Boolean).pop()
		: null;
	const title =
		branch && !GENERIC_BRANCHES.has(branch)
			? branch
			: cwdBase || branch || worktree.path || "(worktree)";
	return {
		id: `wt:${worktree.host}:${worktree.path}`,
		// No live Claude session — render with a synthetic placeholder so
		// the existing SidebarItem component (which expects `session`) can
		// still render the row uniformly.
		session: {
			id: `wt:${worktree.host}:${worktree.path}`,
			sessionUuid: "",
			state: "no_claude",
			process: null,
			pane: null,
			conversation: null,
			lastActivityAt: null,
		} as unknown as SessionCardT,
		title,
		worktree,
		tmuxAddress: null,
		pid: null,
		state: "no_claude",
		lastActivityMs: 0,
		reasons: [],
	};
}
