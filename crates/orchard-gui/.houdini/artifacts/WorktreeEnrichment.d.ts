import { IssueState } from "$houdini/graphql/enums";
import type { ValueOf } from "$houdini/runtime/lib/types";
export type WorktreeEnrichment$input = {};

export type WorktreeEnrichment = {
    readonly "shape"?: WorktreeEnrichment$data;
    readonly " $fragments": {
        "WorktreeEnrichment": any;
    };
};

export type WorktreeEnrichment$data = {
    /**
     * Stable identifier formatted as `<project_id>:<worktree_name>`. The main checkout uses the worktree name `main`.
    */
    readonly id: string;
    /**
     * Absolute filesystem path to the worktree.
    */
    readonly path: string;
    /**
     * Branch the worktree currently has checked out. Empty string for detached HEAD or bare worktrees.
    */
    readonly branch: string;
    /**
     * Hostname this worktree was discovered on. v1: always 'local'. Workstream F populates per-peer.
    */
    readonly host: string;
    /**
     * owner/repo slug derived from origin remote. Null when origin is not a GitHub URL.
    */
    readonly repo: string | null;
    /**
     * Issue linked from the worktree's branch (issue<N>/... convention). Null when the branch doesn't carry an issue number.
    */
    readonly issue: {
        readonly number: number;
        readonly state: ValueOf<typeof IssueState>;
        readonly title: string;
    } | null;
};

export type WorktreeEnrichment$artifact = {
    "name": "WorktreeEnrichment";
    "kind": "HoudiniFragment";
    "hash": "bf17ac5c42fb96972a6ae6b795017b1032f767da100889a6f89a9fb8911f05ad";
    "raw": `fragment WorktreeEnrichment on Worktree {
  id
  path
  branch
  host
  repo
  issue {
    number
    state
    title
    id
  }
  __typename
}`;
    "rootType": "Worktree";
    "stripVariables": [];
    "selection": {
        "fields": {
            "id": {
                "type": "ID";
                "keyRaw": "id";
                "visible": true;
            };
            "path": {
                "type": "String";
                "keyRaw": "path";
                "visible": true;
            };
            "branch": {
                "type": "String";
                "keyRaw": "branch";
                "visible": true;
            };
            "host": {
                "type": "String";
                "keyRaw": "host";
                "visible": true;
            };
            "repo": {
                "type": "String";
                "keyRaw": "repo";
                "nullable": true;
                "visible": true;
            };
            "issue": {
                "type": "Issue";
                "keyRaw": "issue";
                "nullable": true;
                "selection": {
                    "fields": {
                        "number": {
                            "type": "Int";
                            "keyRaw": "number";
                            "visible": true;
                        };
                        "state": {
                            "type": "IssueState";
                            "keyRaw": "state";
                            "visible": true;
                        };
                        "title": {
                            "type": "String";
                            "keyRaw": "title";
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