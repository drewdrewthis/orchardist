/**
 * Lens registry. Four lenses, four anchors, four queries. Each lens
 * fetches its own snapshot — no reshuffling of a unified Item[].
 *
 * The store calls fetchLens(active) when the active lens changes; the
 * subscription fan-out re-fetches on tmuxSessionsChanged / processes /
 * pullRequestChanged via the existing daemon WS channel.
 */
export { fetchRecent, type RecentRow } from "./recent";
export {
	fetchTmux,
	type TmuxLensSnapshot,
	type TmuxSessionNode,
	type TmuxWindowNode,
} from "./tmux";
export {
	fetchAttention,
	type AttentionRow,
	type AttentionTier,
} from "./attention";
export { fetchIssues, type IssueRow } from "./issue";
export { fetchPanel, type PanelData } from "./panel";
export type {
	SessionCardT,
	PaneCardT,
	WorktreeEnrichment,
	ClaudeStateRaw,
} from "./fragments";

export type LensId = "recent" | "tmux" | "attention" | "issue";
