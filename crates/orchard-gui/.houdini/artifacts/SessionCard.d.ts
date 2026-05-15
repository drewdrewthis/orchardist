import { IssueState } from "$houdini/graphql/enums";
import { InstanceState } from "$houdini/graphql/enums";
import type { ValueOf } from "$houdini/runtime/lib/types";
export type SessionCard$input = {};

export type SessionCard = {
    readonly "shape"?: SessionCard$data;
    readonly " $fragments": {
        "SessionCard": any;
    };
};

export type SessionCard$data = {
    /**
     * Stable orchard id — "ClaudeInstance:<host>:<claudePid>".
    */
    readonly id: string;
    /**
     * Claude session UUID. Mirrors the heartbeat's session_id field.
    */
    readonly sessionUuid: string | null;
    /**
     * Lifecycle state derived from the heartbeat file plus pid liveness.
    */
    readonly state: ValueOf<typeof InstanceState>;
    /**
     * RFC3339 timestamp the session was started.
    */
    readonly startedAt: string | null;
    /**
     * ISO8601 timestamp of the most recent activity for this Claude instance —
    derived from the heartbeat's last_activity field, falling back to
    TmuxPane.lastActivityAt, falling back to null when neither is available.
    */
    readonly lastActivityAt: string | null;
    /**
     * True when remote-control is enabled for this session.
    */
    readonly rcEnabled: boolean;
    /**
     * Claude CLI account this session is authenticated under.
    */
    readonly account: {
        /**
         * Email address `claude auth status` reports for the active session.
        */
        readonly email: string;
    } | null;
    /**
     * Tmux pane hosting this Claude process. Null if no pane could be matched.
    */
    readonly pane: {
        /**
         * tmux pane id, including the leading `%` (e.g. `%26`).
        */
        readonly paneId: string;
        /**
         * Pane title (tmux `pane_title`).
        */
        readonly title: string;
        /**
         * Foreground command name as reported by `tmux #{pane_current_command}`.
        This is **whatever string tmux's pane_current_command emits**, NOT a
        guaranteed basename. In practice that means:

        - For most processes: a basename like `'zsh'` or `'vim'`.
        - For Node-wrapped CLIs (claude, npx tools, etc.): often the wrapper's
          version string (`'2.1.126'`) instead of the friendly name. tmux reads
          this from /proc/$pid/comm or the macOS equivalent, which Node sets
          to its own runtime version when the script doesn't override argv[0].

        Operators filtering by command should not assume a stable name —
        `currentCommandIn: ["claude"]` will silently miss panes running the
        Claude CLI when its wrapper has set comm to a version string. To
        identify panes by program intent, walk to the foreground process via
        `Pane.process` (when wired — see #394/#395) and inspect that node's
        command/args instead.
        */
        readonly currentCommand: string;
        /**
         * Window this pane belongs to.
        */
        readonly window: {
            /**
             * Stable id `TmuxWindow:<host>:<sessionName>:<windowIndex>`.
            */
            readonly id: string;
            /**
             * Window index, zero-based as tmux numbers them when -base-index is 0.
            */
            readonly index: number;
            /**
             * Window name (tmux's `window_name`).
            */
            readonly name: string;
            /**
             * True when this window is the currently-focused window in its session.
            */
            readonly active: boolean;
            /**
             * Session this window belongs to.
            */
            readonly session: {
                /**
                 * Stable id `TmuxSession:<host>:<sessionName>`.
                */
                readonly id: string;
                /**
                 * Session name.
                */
                readonly name: string;
                /**
                 * True when at least one client is attached to this session.
                */
                readonly attached: boolean;
                /**
                 * True when at least one attached client has had activity within the freshness window (default 5m).
                */
                readonly activeAttached: boolean;
            };
        };
    } | null;
    /**
     * OS-level process. Null until the process can be matched by pid.
    */
    readonly process: {
        /**
         * Process id.
        */
        readonly pid: number;
        /**
         * Current working directory.

        Slow path — `lsof` on macOS, `/proc/<pid>/cwd` on Linux (Linux fallback
        not yet implemented; returns null silently). Resolution only fires when
        this field is in the selection set; the `cwdLoader` coalesces concurrent
        calls into a single batched `lsof -p <pids> -F n` shellout per request.

        The de-facto opt-in path from a tmux pane is `pane.process.cwd` — there
        is no separate flag or argument; selecting `cwd` IS the opt-in.
        */
        readonly cwd: string | null;
    } | null;
    /**
     * Worktree this Claude REPL is operating in — the deepest worktree whose
    `path` contains the resolved process cwd. Server-side join over the
    ps + git providers; null when no process can be matched or no worktree
    contains the cwd. Avoids duplicate cwd→path matching in clients.
    */
    readonly worktree: {
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
        readonly " $fragments": {
            WorktreeEnrichment: {};
        };
    } | null;
    /**
     * Conversation node for this Claude session — looked up by `sessionUuid`
    in the claudeprojects provider. Null when the Claude REPL has not yet
    written a JSONL record (or `sessionUuid` is null). Lets clients pull
    customTitle / agentName / lastSeenAt without a separate `conversations`
    query + uuid map.
    */
    readonly conversation: {
        /**
         * The session UUID embedded in the JSONL filename — globally unique across Claude Code.
        */
        readonly sessionUuid: string;
        /**
         * RFC 3339 timestamp of the last record in the JSONL.
        */
        readonly lastSeenAt: string | null;
        /**
         * Sub-agent name from the JSONL `type: "agent-name"` record. Null when the session has not yet recorded one.
        */
        readonly agentName: string | null;
        /**
         * User-set title from the JSONL `type: "custom-title"` record. Null when the session has not yet recorded one.
        */
        readonly customTitle: string | null;
    } | null;
};

export type SessionCard$artifact = {
    "name": "SessionCard";
    "kind": "HoudiniFragment";
    "hash": "bfa28546b6270088a2aa6bf928d0f8556b37d7bfb70f26cf7663212118a10207";
    "raw": `fragment SessionCard on ClaudeInstance {
  id
  sessionUuid
  state
  startedAt
  lastActivityAt
  rcEnabled
  account {
    email
    id
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
    id
  }
  process {
    pid
    cwd
    id
  }
  worktree {
    ...WorktreeEnrichment
    id
  }
  conversation {
    sessionUuid
    lastSeenAt
    agentName
    customTitle
    id
  }
  __typename
}

fragment WorktreeEnrichment on Worktree {
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
    "rootType": "ClaudeInstance";
    "stripVariables": [];
    "selection": {
        "fields": {
            "id": {
                "type": "ID";
                "keyRaw": "id";
                "visible": true;
            };
            "sessionUuid": {
                "type": "String";
                "keyRaw": "sessionUuid";
                "nullable": true;
                "visible": true;
            };
            "state": {
                "type": "InstanceState";
                "keyRaw": "state";
                "visible": true;
            };
            "startedAt": {
                "type": "String";
                "keyRaw": "startedAt";
                "nullable": true;
                "visible": true;
            };
            "lastActivityAt": {
                "type": "String";
                "keyRaw": "lastActivityAt";
                "nullable": true;
                "visible": true;
            };
            "rcEnabled": {
                "type": "Boolean";
                "keyRaw": "rcEnabled";
                "visible": true;
            };
            "account": {
                "type": "ClaudeAccount";
                "keyRaw": "account";
                "nullable": true;
                "selection": {
                    "fields": {
                        "email": {
                            "type": "String";
                            "keyRaw": "email";
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
            "pane": {
                "type": "TmuxPane";
                "keyRaw": "pane";
                "nullable": true;
                "selection": {
                    "fields": {
                        "paneId": {
                            "type": "String";
                            "keyRaw": "paneId";
                            "visible": true;
                        };
                        "title": {
                            "type": "String";
                            "keyRaw": "title";
                            "visible": true;
                        };
                        "currentCommand": {
                            "type": "String";
                            "keyRaw": "currentCommand";
                            "visible": true;
                        };
                        "window": {
                            "type": "TmuxWindow";
                            "keyRaw": "window";
                            "selection": {
                                "fields": {
                                    "id": {
                                        "type": "ID";
                                        "keyRaw": "id";
                                        "visible": true;
                                    };
                                    "index": {
                                        "type": "Int";
                                        "keyRaw": "index";
                                        "visible": true;
                                    };
                                    "name": {
                                        "type": "String";
                                        "keyRaw": "name";
                                        "visible": true;
                                    };
                                    "active": {
                                        "type": "Boolean";
                                        "keyRaw": "active";
                                        "visible": true;
                                    };
                                    "session": {
                                        "type": "TmuxSession";
                                        "keyRaw": "session";
                                        "selection": {
                                            "fields": {
                                                "id": {
                                                    "type": "ID";
                                                    "keyRaw": "id";
                                                    "visible": true;
                                                };
                                                "name": {
                                                    "type": "String";
                                                    "keyRaw": "name";
                                                    "visible": true;
                                                };
                                                "attached": {
                                                    "type": "Boolean";
                                                    "keyRaw": "attached";
                                                    "visible": true;
                                                };
                                                "activeAttached": {
                                                    "type": "Boolean";
                                                    "keyRaw": "activeAttached";
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
                        "id": {
                            "type": "ID";
                            "keyRaw": "id";
                            "visible": true;
                        };
                    };
                };
                "visible": true;
            };
            "process": {
                "type": "Process";
                "keyRaw": "process";
                "nullable": true;
                "selection": {
                    "fields": {
                        "pid": {
                            "type": "Int";
                            "keyRaw": "pid";
                            "visible": true;
                        };
                        "cwd": {
                            "type": "String";
                            "keyRaw": "cwd";
                            "nullable": true;
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
            "worktree": {
                "type": "Worktree";
                "keyRaw": "worktree";
                "nullable": true;
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
                    "fragments": {
                        "WorktreeEnrichment": {
                            "arguments": {};
                        };
                    };
                };
                "visible": true;
            };
            "conversation": {
                "type": "Conversation";
                "keyRaw": "conversation";
                "nullable": true;
                "selection": {
                    "fields": {
                        "sessionUuid": {
                            "type": "String";
                            "keyRaw": "sessionUuid";
                            "visible": true;
                        };
                        "lastSeenAt": {
                            "type": "Time";
                            "keyRaw": "lastSeenAt";
                            "nullable": true;
                            "visible": true;
                        };
                        "agentName": {
                            "type": "String";
                            "keyRaw": "agentName";
                            "nullable": true;
                            "visible": true;
                        };
                        "customTitle": {
                            "type": "String";
                            "keyRaw": "customTitle";
                            "nullable": true;
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