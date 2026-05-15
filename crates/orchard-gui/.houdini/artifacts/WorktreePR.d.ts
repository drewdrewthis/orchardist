import { MergeableState } from "$houdini/graphql/enums";
import { ReviewDecisionEnum } from "$houdini/graphql/enums";
import { CiStatus } from "$houdini/graphql/enums";
import { PullRequestState } from "$houdini/graphql/enums";
import type { ValueOf } from "$houdini/runtime/lib/types";
export type WorktreePR$input = {};

export type WorktreePR = {
    readonly "shape"?: WorktreePR$data;
    readonly " $fragments": {
        "WorktreePR": any;
    };
};

export type WorktreePR$data = {
    /**
     * PR whose headRef matches this worktree's branch.

    Selection precedence (issue #489): an OPEN PR is returned when one exists;
    otherwise the most-recent CLOSED/MERGED PR with the matching headRef is
    returned so the TUI stale-fade UX can render the post-merge cleanup
    affordance. The `state` field on the returned PullRequest disambiguates
    between live and post-merge.

    Null when no PR has ever existed for this branch in any state, when the
    branch is detached, or when the branch is the project's default branch.
    */
    readonly pr: {
        readonly number: number;
        readonly state: ValueOf<typeof PullRequestState>;
        readonly statusCheckRollup: ValueOf<typeof CiStatus>;
        readonly reviewDecision: ValueOf<typeof ReviewDecisionEnum> | null;
        readonly mergeable: ValueOf<typeof MergeableState>;
        readonly mergeStateStatus: string;
    } | null;
};

export type WorktreePR$artifact = {
    "name": "WorktreePR";
    "kind": "HoudiniFragment";
    "hash": "cf283a2ca007687118cce9aa35d16f195aa7fe78d6beea8564bddf373ed543d0";
    "raw": `fragment WorktreePR on Worktree {
  pr {
    number
    state
    statusCheckRollup
    reviewDecision
    mergeable
    mergeStateStatus
    id
  }
  id
  __typename
}`;
    "rootType": "Worktree";
    "stripVariables": [];
    "selection": {
        "fields": {
            "pr": {
                "type": "PullRequest";
                "keyRaw": "pr";
                "nullable": true;
                "selection": {
                    "fields": {
                        "number": {
                            "type": "Int";
                            "keyRaw": "number";
                            "visible": true;
                        };
                        "state": {
                            "type": "PullRequestState";
                            "keyRaw": "state";
                            "visible": true;
                        };
                        "statusCheckRollup": {
                            "type": "CiStatus";
                            "keyRaw": "statusCheckRollup";
                            "visible": true;
                        };
                        "reviewDecision": {
                            "type": "ReviewDecisionEnum";
                            "keyRaw": "reviewDecision";
                            "nullable": true;
                            "visible": true;
                        };
                        "mergeable": {
                            "type": "MergeableState";
                            "keyRaw": "mergeable";
                            "visible": true;
                        };
                        "mergeStateStatus": {
                            "type": "String";
                            "keyRaw": "mergeStateStatus";
                            "visible": true;
                        };
                        "id": {
                            "type": "ID";
                            "keyRaw": "id";
                            "visible": true;
                        };
                    };
                };
                "visible": true;
            };
            "id": {
                "type": "ID";
                "keyRaw": "id";
                "visible": true;
            };
            "__typename": {
                "type": "String";
                "keyRaw": "__typename";
                "visible": true;
            };
        };
    };
    "pluginData": {
        "houdini-svelte": {};
    };
};