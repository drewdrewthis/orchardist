import { InstanceState } from "$houdini/graphql/enums";
import { IssueState } from "$houdini/graphql/enums";
import type { ValueOf } from "$houdini/runtime/lib/types";

export type WorktreeLens = {
    readonly "input": WorktreeLens$input;
    readonly "result": WorktreeLens$result | undefined;
};

export type WorktreeLens$result = {
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
                /**
                 * Tmux panes whose foreground-process cwd equals `path` exactly OR has `path + '/'` as a prefix (#511).
                Returns the matching pane list for every worktree, derived from a server-side join of pane.process.cwd
                against the worktree path. Ordered deterministically by paneId ascending.
                Returns `[]` when no panes match or when the tmux / ps providers are not wired.
                */
                readonly tmuxPanes: ({
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
                     * Pid of the foreground process. Null when tmux reports no current pid.
                    */
                    readonly currentPid: number | null;
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
                            /**
                             * Most recent activity timestamp across all panes/windows. RFC3339; null
                            if never observed.

                            Tracks tmux's `pane_activity` field, which fires on NEW pane content —
                            not on in-place redraws. A Claude REPL spinner ticking in place
                            ("Elucidating… 1m 11s") does NOT bump `lastActivityAt`, so a long
                            agentic turn can look identical to a hung pane (issue #506).

                            Do NOT use this field as a Claude-REPL stall signal. The canonical
                            signal is `Worktree.claudeInstances[].state` (working|idle|input|stalled,
                            issue #501), OR the heartbeat freshness exposed on
                            `ClaudeInstance` directly. `lastActivityAt` remains useful for plain
                            shell panes and for the lex-tie-breaker in `Worktree.tmuxSession`.
                            */
                            readonly lastActivityAt: string | null;
                        };
                    };
                    /**
                     * Claude instance running in this pane, if the foreground command is claude. Null until ws-b-claudeinstance wires it.
                    */
                    readonly claudeInstance: {
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
                        readonly " $fragments": {
                            SessionCard: {};
                        };
                    } | null;
                    /**
                     * OS-level Process for the pane's foreground pid (`pane_current_pid`).

                    Null when tmux reports no foreground pid (`currentPid == 0`) or when the
                    pid is no longer in the cached ps snapshot (process exited or just
                    spawned). Cache-only — never triggers a fresh `ps` shellout under a
                    request goroutine.

                    Selecting nested fields (e.g. `process { cwd }`) chains through the
                    standard Process resolvers; cwd resolution is the slow path described on
                    `Process.cwd` and only fires when the client selects it.
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
                        /**
                         * Command basename (e.g. 'sleep', 'claude'). Cheap, always populated.
                        */
                        readonly command: string;
                        /**
                         * Worktree this process is running inside, derived from cwd. Null until ws-b-git wires the lookup.
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
                    } | null;
                    readonly " $fragments": {
                        PaneCard: {};
                    };
                })[];
                readonly " $fragments": {
                    WorktreeEnrichment: {};
                };
            })[];
        })[];
    };
};

export type WorktreeLens$input = null;

export type WorktreeLens$artifact = {
    "name": "WorktreeLens";
    "kind": "HoudiniQuery";
    "hash": "14c0b0fbdfa15f8c0d7ce748a45e8d7724694ef41b0eb29bf58c1d47b553ba96";
    "raw": `query WorktreeLens {
  workView {
    repos {
      id
      slug
      worktrees {
        ...WorktreeEnrichment
        tmuxPanes {
          ...PaneCard
          id
        }
        id
      }
    }
  }
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
}

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
    id
  }
  process {
    pid
    cwd
    command
    worktree {
      ...WorktreeEnrichment
      id
    }
    id
  }
  id
  __typename
}

fragment SessionCard on ClaudeInstance {
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
                                                "tmuxPanes": {
                                                    "type": "TmuxPane";
                                                    "keyRaw": "tmuxPanes";
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
                                                            "currentPid": {
                                                                "type": "Int";
                                                                "keyRaw": "currentPid";
                                                                "nullable": true;
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
                                                                                    "lastActivityAt": {
                                                                                        "type": "String";
                                                                                        "keyRaw": "lastActivityAt";
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
                                                            "claudeInstance": {
                                                                "type": "ClaudeInstance";
                                                                "keyRaw": "claudeInstance";
                                                                "nullable": true;
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
                                                                    "fragments": {
                                                                        "SessionCard": {
                                                                            "arguments": {};
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
                                                                        "command": {
                                                                            "type": "String";
                                                                            "keyRaw": "command";
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
                                                        "fragments": {
                                                            "PaneCard": {
                                                                "arguments": {};
                                                            };
                                                        };
                                                    };
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