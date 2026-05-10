/**
 * Lens registry. Each lens owns its Houdini store + projection and
 * is imported directly from its own module by the sidebar. The legacy
 * `fetchXxx` facades have been deleted — components subscribe to the
 * Houdini stores directly, no intermediate.
 */
export { attentionStore, buildAttentionRows, type AttentionRow, type AttentionTier } from "./attention";
export { recentStore, buildRecentRows, type RecentRow } from "./recent";
export { tmuxStore, buildTmuxSnapshot, type TmuxLensSnapshot, type TmuxSessionNode, type TmuxWindowNode } from "./tmux";
export { issueStore, buildIssueRows, type IssueRow } from "./issue";
export { createPanelStore, buildPanelData, type PanelData } from "./panel";
export type { SessionCardT, PaneCardT, WorktreeEnrichment, ClaudeStateRaw } from "./fragments";

export type LensId = "attention" | "recent" | "tmux" | "issue";
