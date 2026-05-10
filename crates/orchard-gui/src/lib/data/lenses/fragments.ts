/**
 * Shared GraphQL fragments for the four lenses.
 *
 * Every lens row needs the same enrichment to render in the sidebar.
 * Centralising it here is what makes the per-lens queries tractable —
 * each lens picks its anchor (claudeInstances / tmuxServer / projects),
 * then composes the same `SessionCard` / `PaneCard` chunks for display.
 *
 * No fabrication: every field below is verified against the live daemon
 * schema (introspection on 2026-05-09).
 */
import { gql } from "graphql-request";

/**
 * What a Claude session row needs to render. Anchor of the recent /
 * attention lenses; enrichment of the tmux pane row when a pane has a
 * Claude on it.
 */
export const SESSION_CARD_FRAGMENT = gql`
	fragment SessionCard on ClaudeInstance {
		id
		sessionUuid
		state
		startedAt
		lastActivityAt
		rcEnabled
		account {
			email
		}
		pane {
			paneId
			title
			currentCommand
			window {
				id
				index
				name
				active
				session {
					id
					name
					attached
					activeAttached
				}
			}
		}
		process {
			pid
			cwd
		}
		worktree {
			...WorktreeEnrichment
		}
		conversation {
			sessionUuid
			lastSeenAt
			agentName
			customTitle
		}
	}
`;

/**
 * What a tmux pane row needs in the tmux lens. The pane is the unit;
 * the claude instance + process are enrichment.
 */
export const PANE_CARD_FRAGMENT = gql`
	fragment PaneCard on TmuxPane {
		paneId
		title
		currentCommand
		currentPid
		window {
			id
			index
			name
			active
			session {
				id
				name
				attached
				activeAttached
				lastActivityAt
			}
		}
		claudeInstance {
			...SessionCard
		}
		process {
			pid
			cwd
			worktree {
				...WorktreeEnrichment
			}
		}
	}
`;

/** Daemon-visible Claude instance state. Only the literals the daemon emits today. */
export type ClaudeStateRaw = "working" | "no_claude" | (string & {});

/** Card-shape mirroring SessionCard for TS. */
export interface SessionCardT {
	id: string;
	sessionUuid: string;
	state: ClaudeStateRaw;
	// `startedAt` is `String` in the schema (nullable); the previous
	// non-null declaration was a TS lie that broke synthetic-card
	// construction in the tmux lens projection.
	startedAt: string | null;
	lastActivityAt: string | null;
	rcEnabled: boolean;
	account: { email: string } | null;
	pane: {
		paneId: string;
		title: string;
		currentCommand: string;
		window: {
			id: string;
			index: number;
			name: string;
			active: boolean;
			session: {
				id: string;
				name: string;
				attached: boolean;
				activeAttached: boolean;
			};
		};
	} | null;
	process: { pid: number; cwd: string | null } | null;
	/** Daemon-resolved worktree (cwd→path match). Null when no match. */
	worktree?: WorktreeEnrichment | null;
	/** Daemon-joined Conversation (sessionUuid lookup). Null when not yet observed. */
	conversation?: {
		sessionUuid: string;
		lastSeenAt: string | null;
		agentName: string | null;
		customTitle: string | null;
	} | null;
}

export interface PaneCardT {
	paneId: string;
	title: string;
	currentCommand: string;
	currentPid: number | null;
	window: {
		id: string;
		index: number;
		name: string;
		active: boolean;
		session: {
			id: string;
			name: string;
			attached: boolean;
			activeAttached: boolean;
			lastActivityAt: string | null;
		};
	};
	/** Full SessionCard via daemon's TmuxPane.claudeInstance edge. */
	claudeInstance: SessionCardT | null;
	process: {
		pid: number;
		cwd: string | null;
		worktree?: WorktreeEnrichment | null;
	} | null;
}

/** Worktree enrichment shape used by attention + issue lenses. */
export interface WorktreeEnrichment {
	id: string;
	path: string;
	branch: string;
	host: string;
	repo: string | null;
	pr: {
		number: number;
		state: string;
		statusCheckRollup: string | null;
		reviewDecision: string | null;
		mergeable: string | null;
		mergeStateStatus: string | null;
	} | null;
	issue: { number: number; state: string; title: string | null } | null;
}

export const WORKTREE_ENRICHMENT_FRAGMENT = gql`
	fragment WorktreeEnrichment on Worktree {
		id
		path
		branch
		host
		repo
		pr {
			number
			state
			statusCheckRollup
			reviewDecision
			mergeable
			mergeStateStatus
		}
		issue {
			number
			state
			title
		}
	}
`;
