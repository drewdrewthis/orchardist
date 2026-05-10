/**
 * Lens registry. Each lens owns its Houdini store + projection and is
 * imported directly from its own module by the sidebar. The legacy
 * `fetchXxx` facades have been deleted — components subscribe to the
 * Houdini stores directly, no intermediate.
 *
 * Daemon-side joins (Worktree.claudeInstances, ClaudeInstance.worktree,
 * ClaudeInstance.conversation) replaced the per-lens cwd→worktree and
 * sessionUuid→conversation maps that used to live in this layer.
 */
export { attentionStore, buildAttentionSections, type AttentionTier } from "./attention";
export { recentStore, buildRecentItems } from "./recent";
export { tmuxStore, buildTmuxSnapshot, buildTmuxSections, type TmuxLensSnapshot, type TmuxSessionNode, type TmuxWindowNode } from "./tmux";
export { issueStore, buildIssueSections } from "./issue";
export { worktreeStore, buildWorktreeSections } from "./worktree";
export { createPanelStore, buildPanelData, type PanelData } from "./panel";
export type { SessionCardT, PaneCardT, WorktreeEnrichment, ClaudeStateRaw } from "./fragments";

export type LensId = "attention" | "recent" | "tmux" | "issue" | "worktree";
