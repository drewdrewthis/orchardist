export type WorktreesList = {
    readonly "input": WorktreesList$input;
    readonly "result": WorktreesList$result | undefined;
};

export type WorktreesList$result = {
    /**
     * Composite "what's running, where, on what branch?" view (#469 F6).
    Walks the local repos → worktrees graph and joins each worktree
    to its open PR, linked issue, processes, and tmux sessions in a
    single round trip. The fields underneath reuse existing resolvers,
    so semantics match per-field queries.
    */
    readonly workView: {
        /**
         * Repos in this daemon's config, with worktrees, sessions, claude, and PR/issue joins eagerly walked.
        */
        readonly repos: ({
            /**
             * Stable identifier derived from `slug`. Survives re-registration.
            */
            readonly id: string;
            /**
             * GitHub-style `owner/repo` slug. The repo's identity.
            */
            readonly slug: string;
            /**
             * Worktrees discovered for this repo — the main checkout plus everything under `.git/worktrees/`.
            */
            readonly worktrees: ({
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
                 * True when HEAD references a deleted branch or otherwise fails to resolve to a commit. Bare worktrees still appear in the list — they just have no live branch.
                */
                readonly bare: boolean;
                /**
                 * Hostname this worktree was discovered on. v1: always 'local'. Workstream F populates per-peer.
                */
                readonly host: string;
                /**
                 * owner/repo slug derived from origin remote. Null when origin is not a GitHub URL.
                */
                readonly repo: string | null;
            })[];
        })[];
    };
};

export type WorktreesList$input = null;

export type WorktreesList$artifact = {
    "name": "WorktreesList";
    "kind": "HoudiniQuery";
    "hash": "962e203c9aaddb741765b8de930643d6b0cd21d0907e9d2e1374b7c2a351dfeb";
    "raw": `query WorktreesList {
  workView {
    repos {
      id
      slug
      worktrees {
        id
        path
        branch
        bare
        host
        repo
      }
    }
  }
}`;
    "rootType": "Query";
    "stripVariables": [];
    "selection": {
        "fields": {
            "workView": {
                "type": "WorkView";
                "keyRaw": "workView";
                "selection": {
                    "fields": {
                        "repos": {
                            "type": "Repo";
                            "keyRaw": "repos";
                            "selection": {
                                "fields": {
                                    "id": {
                                        "type": "ID";
                                        "keyRaw": "id";
                                        "visible": true;
                                    };
                                    "slug": {
                                        "type": "String";
                                        "keyRaw": "slug";
                                        "visible": true;
                                    };
                                    "worktrees": {
                                        "type": "Worktree";
                                        "keyRaw": "worktrees";
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
                                                "bare": {
                                                    "type": "Boolean";
                                                    "keyRaw": "bare";
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
                                            };
                                        };
                                        "visible": true;
                                    };
                                };
                            };
                            "visible": true;
                        };
                    };
                };
                "visible": true;
            };
        };
    };
    "pluginData": {
        "houdini-svelte": {};
    };
    "policy": "CacheAndNetwork";
    "partial": false;
};