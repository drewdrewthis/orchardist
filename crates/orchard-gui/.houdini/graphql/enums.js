/** Aggregated CI status across all check runs and statuses on the PR head sha. */
export const CiStatus = {
    "FAILURE": "FAILURE",
    "PENDING": "PENDING",
    "SUCCESS": "SUCCESS",
    "UNKNOWN": "UNKNOWN"
};

/** Lifecycle states for Contract. */
export const ContractStatus = {
    "AWAITING_CANCEL_ACK": "AWAITING_CANCEL_ACK",
    "CANCELLED": "CANCELLED",
    "DELIVERED_PENDING_PARENT_VALIDATION": "DELIVERED_PENDING_PARENT_VALIDATION",
    "DELIVERED_PENDING_VALIDATION": "DELIVERED_PENDING_VALIDATION",
    "JUDGE_REJECTED_TERMINAL": "JUDGE_REJECTED_TERMINAL",
    "OPEN": "OPEN",
    "PENDING_DREW_APPROVAL": "PENDING_DREW_APPROVAL",
    "SATISFIED": "SATISFIED",
    "WAITING_EXTERNAL": "WAITING_EXTERNAL"
};

/** Lifecycle state of a HostService.

  - `active`        — running and healthy.
  - `inactive`      — stopped cleanly; not currently running.
  - `failed`        — exited with non-zero status or crashed.
  - `not_installed` — no corresponding service-manager unit on this host.
  - `unknown`       — service-manager returned output the daemon could
                      not interpret (parse failure, unexpected error). */
export const HostServiceState = {
    "active": "active",
    "failed": "failed",
    "inactive": "inactive",
    "not_installed": "not_installed",
    "unknown": "unknown"
};

/** Lifecycle states for a Claude instance. */
export const InstanceState = {
    /**
     * Tracked but the underlying process is gone.
    */
    "dead": "dead",

    /**
     * Claude has finished its turn and is waiting for the next prompt.
    */
    "idle": "idle",

    /**
     * Claude is paused and waiting for user input.
    */
    "input": "input",

    /**
     * No Claude session has ever been observed for this pane/process.
    */
    "no_claude": "no_claude",

    /**
     * The session has been alive but is no longer answering heartbeats.
    */
    "stalled": "stalled",

    /**
     * Claude is actively executing a tool or generating a response.
    */
    "working": "working"
};

/** State filter for the issues query. */
export const IssueState = {
    "ALL": "ALL",
    "CLOSED": "CLOSED",
    "OPEN": "OPEN"
};

/** Whether GitHub considers the PR mergeable. UNKNOWN means GitHub is still computing. */
export const MergeableState = {
    "CONFLICTING": "CONFLICTING",
    "MERGEABLE": "MERGEABLE",
    "UNKNOWN": "UNKNOWN"
};

/** State filter for the pullRequests query. */
export const PullRequestState = {
    "ALL": "ALL",
    "CLOSED": "CLOSED",
    "MERGED": "MERGED",
    "OPEN": "OPEN"
};

/** Reviewer decision on a PR. Null when no review activity yet. */
export const ReviewDecisionEnum = {
    "APPROVED": "APPROVED",
    "CHANGES_REQUESTED": "CHANGES_REQUESTED",
    "COMMENTED": "COMMENTED",
    "DISMISSED": "DISMISSED",
    "REVIEW_REQUIRED": "REVIEW_REQUIRED"
};

/** Sort key for `TmuxServer.sessions`. */
export const TmuxSessionSort = {
    /**
     * Most-recently-active first (lastActivityAt DESC). Lex-lower name breaks ties; sessions with no activity rank below those with one.
    */
    "LAST_ACTIVITY": "LAST_ACTIVITY",

    /**
     * Stable lex order by session name.
    */
    "NAME": "NAME"
};

export const DedupeMatchMode = {
    "Variables": "Variables",
    "Operation": "Operation",
    "None": "None"
};