
type ValuesOf<T> = T[keyof T]
	
/** Aggregated CI status across all check runs and statuses on the PR head sha. */
export declare const CiStatus: {
    readonly FAILURE: "FAILURE";
    readonly PENDING: "PENDING";
    readonly SUCCESS: "SUCCESS";
    readonly UNKNOWN: "UNKNOWN";
}

/** Aggregated CI status across all check runs and statuses on the PR head sha. */
export type CiStatus$options = ValuesOf<typeof CiStatus>
 
/** Lifecycle states for Contract. */
export declare const ContractStatus: {
    readonly AWAITING_CANCEL_ACK: "AWAITING_CANCEL_ACK";
    readonly CANCELLED: "CANCELLED";
    readonly DELIVERED_PENDING_PARENT_VALIDATION: "DELIVERED_PENDING_PARENT_VALIDATION";
    readonly DELIVERED_PENDING_VALIDATION: "DELIVERED_PENDING_VALIDATION";
    readonly JUDGE_REJECTED_TERMINAL: "JUDGE_REJECTED_TERMINAL";
    readonly OPEN: "OPEN";
    readonly PENDING_DREW_APPROVAL: "PENDING_DREW_APPROVAL";
    readonly SATISFIED: "SATISFIED";
    readonly WAITING_EXTERNAL: "WAITING_EXTERNAL";
}

/** Lifecycle states for Contract. */
export type ContractStatus$options = ValuesOf<typeof ContractStatus>
 
export declare const DedupeMatchMode: {
    readonly Variables: "Variables";
    readonly Operation: "Operation";
    readonly None: "None";
}

export type DedupeMatchMode$options = ValuesOf<typeof DedupeMatchMode>
 
/** Lifecycle state of a HostService.

  - `active`        — running and healthy.
  - `inactive`      — stopped cleanly; not currently running.
  - `failed`        — exited with non-zero status or crashed.
  - `not_installed` — no corresponding service-manager unit on this host.
  - `unknown`       — service-manager returned output the daemon could
                      not interpret (parse failure, unexpected error). */
export declare const HostServiceState: {
    readonly active: "active";
    readonly failed: "failed";
    readonly inactive: "inactive";
    readonly not_installed: "not_installed";
    readonly unknown: "unknown";
}

/** Lifecycle state of a HostService.

  - `active`        — running and healthy.
  - `inactive`      — stopped cleanly; not currently running.
  - `failed`        — exited with non-zero status or crashed.
  - `not_installed` — no corresponding service-manager unit on this host.
  - `unknown`       — service-manager returned output the daemon could
                      not interpret (parse failure, unexpected error). */
export type HostServiceState$options = ValuesOf<typeof HostServiceState>
 
/** Lifecycle states for a Claude instance. */
export declare const InstanceState: {
    /** Tracked but the underlying process is gone. */
    readonly dead: "dead";
    /** Claude has finished its turn and is waiting for the next prompt. */
    readonly idle: "idle";
    /** Claude is paused and waiting for user input. */
    readonly input: "input";
    /** No Claude session has ever been observed for this pane/process. */
    readonly no_claude: "no_claude";
    /** The session has been alive but is no longer answering heartbeats. */
    readonly stalled: "stalled";
    /** Claude is actively executing a tool or generating a response. */
    readonly working: "working";
}

/** Lifecycle states for a Claude instance. */
export type InstanceState$options = ValuesOf<typeof InstanceState>
 
/** State filter for the issues query. */
export declare const IssueState: {
    readonly ALL: "ALL";
    readonly CLOSED: "CLOSED";
    readonly OPEN: "OPEN";
}

/** State filter for the issues query. */
export type IssueState$options = ValuesOf<typeof IssueState>
 
/** Whether GitHub considers the PR mergeable. UNKNOWN means GitHub is still computing. */
export declare const MergeableState: {
    readonly CONFLICTING: "CONFLICTING";
    readonly MERGEABLE: "MERGEABLE";
    readonly UNKNOWN: "UNKNOWN";
}

/** Whether GitHub considers the PR mergeable. UNKNOWN means GitHub is still computing. */
export type MergeableState$options = ValuesOf<typeof MergeableState>
 
/** State filter for the pullRequests query. */
export declare const PullRequestState: {
    readonly ALL: "ALL";
    readonly CLOSED: "CLOSED";
    readonly MERGED: "MERGED";
    readonly OPEN: "OPEN";
}

/** State filter for the pullRequests query. */
export type PullRequestState$options = ValuesOf<typeof PullRequestState>
 
/** Reviewer decision on a PR. Null when no review activity yet. */
export declare const ReviewDecisionEnum: {
    readonly APPROVED: "APPROVED";
    readonly CHANGES_REQUESTED: "CHANGES_REQUESTED";
    readonly COMMENTED: "COMMENTED";
    readonly DISMISSED: "DISMISSED";
    readonly REVIEW_REQUIRED: "REVIEW_REQUIRED";
}

/** Reviewer decision on a PR. Null when no review activity yet. */
export type ReviewDecisionEnum$options = ValuesOf<typeof ReviewDecisionEnum>
 
/** Sort key for `TmuxServer.sessions`. */
export declare const TmuxSessionSort: {
    /** Most-recently-active first (lastActivityAt DESC). Lex-lower name breaks ties; sessions with no activity rank below those with one. */
    readonly LAST_ACTIVITY: "LAST_ACTIVITY";
    /** Stable lex order by session name. */
    readonly NAME: "NAME";
}

/** Sort key for `TmuxServer.sessions`. */
export type TmuxSessionSort$options = ValuesOf<typeof TmuxSessionSort>
 